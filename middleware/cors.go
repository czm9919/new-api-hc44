package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// CORS replaces the historical allow-anything policy with a whitelist
// (version spec B02). Behaviour:
//   - Origin absent            → nothing to do (same-origin / non-browser).
//   - whitelist contains "*"   → legacy allow-all, but without credentials.
//   - origin in whitelist      → echo it back, credentials allowed.
//   - otherwise                → preflight is rejected with 403 and simple
//     requests get no CORS headers, so the browser blocks the response.
//
// The whitelist comes from the CorsAllowOrigins option / env and is re-parsed
// on every request so a change applies without a restart. Empty means
// same-origin only, which is the safe default for the bundled dashboard.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin == "" {
			c.Next()
			return
		}
		normalized, err := common.NormalizeOrigin(origin)
		if err != nil {
			// "null" origins, CR/LF injection attempts and malformed values
			// are never echoed back.
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}

		allowAll := false
		matched := false
		for _, entry := range common.ParseOriginList(common.CorsAllowOrigins) {
			if entry == "*" {
				allowAll = true
				break
			}
			if entry == normalized {
				matched = true
				break
			}
		}

		if !allowAll && !matched {
			if c.Request.Method == http.MethodOptions {
				// A browser preflight from an unlisted origin: refuse it so
				// the real request is never sent.
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}

		header := c.Writer.Header()
		header.Add("Vary", "Origin")
		if allowAll {
			header.Set("Access-Control-Allow-Origin", "*")
		} else {
			header.Set("Access-Control-Allow-Origin", normalized)
			header.Set("Access-Control-Allow-Credentials", "true")
		}
		header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		requested := c.GetHeader("Access-Control-Request-Headers")
		if requested == "" {
			requested = "Authorization, Content-Type, New-Api-User"
		}
		header.Set("Access-Control-Allow-Headers", requested)
		header.Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func Version() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-New-Api-Version", common.Version)
		c.Next()
	}
}
