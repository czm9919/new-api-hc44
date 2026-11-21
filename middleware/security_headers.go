package middleware

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const cspNonceContextKey = "csp_nonce"

func newCSPNonce() string {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// CSPNonce returns the nonce minted for this request. HTML renderers must
// stamp it onto inline scripts, otherwise the policy blocks them.
func CSPNonce(c *gin.Context) string {
	return c.GetString(cspNonceContextKey)
}

// SecurityHeaders is the baseline response-hardening layer: anti-framing,
// MIME sniffing, referrer leakage, transport enforcement and CSP. It runs on
// every response (API and HTML alike) so a single `curl -I` on the entry page
// shows the full set.
//
// HSTS is emitted unconditionally: browsers ignore it on plain-HTTP origins,
// so it cannot break an unconfigured reverse proxy, but a later TLS
// termination immediately gains the protection.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		nonce := newCSPNonce()
		if nonce != "" {
			c.Set(cspNonceContextKey, nonce)
		}
		header := c.Writer.Header()
		header.Set("X-Frame-Options", "SAMEORIGIN")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		header.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		header.Set("Content-Security-Policy", common.BuildCSP(nonce))
		c.Next()
	}
}
