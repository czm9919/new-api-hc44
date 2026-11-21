package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// CSRFOriginCheck is the general second line of defence against cross-site
// request forgery (version spec B04). SameSite=Strict cookies and the
// SessionCookieOriginGuard already protect the cookie-carried endpoints; this
// middleware additionally verifies the Origin/Referer of every state-changing
// browser request that presents one, so a hostile page cannot drive the
// dashboard API even if a future cookie loses its SameSite attribute.
//
// Requests without Origin and Referer (curl, SDKs, server-to-server webhooks)
// pass untouched: browsers always attach at least one of the two on
// cross-site writes, which is exactly the threat this check exists for.
// Origins listed in the CORS whitelist are accepted so split-frontend
// deployments keep working.
func CSRFOriginCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if csrfExemptPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		origin, ok := requestBrowserOrigin(c.Request)
		if !ok {
			c.Next()
			return
		}
		if isAllowedWriteOrigin(c.Request, origin) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"success": false,
			"code":    "CSRF_ORIGIN_FORBIDDEN",
			"message": "request origin is not allowed for this endpoint",
		})
	}
}

// csrfExemptPath lists the POST entry points that legitimate third parties
// drive directly: OAuth flows may be launched from provider pages and payment
// providers post their webhooks from their own servers. Server-to-server
// calls never carry Origin/Referer and would pass anyway, but the OAuth
// pages (e.g. Telegram WebApp) do carry a foreign origin.
func csrfExemptPath(path string) bool {
	return strings.HasPrefix(path, "/api/oauth/") ||
		strings.HasSuffix(path, "/webhook") ||
		strings.HasPrefix(path, "/api/waffo-pancake/webhook/") ||
		strings.HasPrefix(path, "/api/setup")
}

func isAllowedWriteOrigin(request *http.Request, origin string) bool {
	if request.Host != "" {
		// Compare against what the client used to reach us. Behind a TLS
		// terminator the forwarded proto header decides the scheme.
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		} else if proto := request.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = strings.ToLower(strings.TrimSpace(proto))
		}
		selfOrigin, err := common.NormalizeOrigin(scheme + "://" + request.Host)
		if err == nil && origin == selfOrigin {
			return true
		}
	}
	for _, entry := range common.ParseOriginList(common.CorsAllowOrigins) {
		if entry == origin {
			return true
		}
	}
	return false
}
