package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// AdminIpWhitelist restricts the admin API to an allowlisted set of source
// IPs / CIDRs (version spec B75). An empty whitelist disables the check so a
// fresh deployment keeps working; the list is re-parsed per request so an
// option change applies immediately.
//
// It is mounted next to AdminAuth, which means ordinary user routes and the
// relay API are unaffected.
func AdminIpWhitelist() gin.HandlerFunc {
	return func(c *gin.Context) {
		networks := common.ParseCIDRList(common.AdminIpWhitelist)
		if len(networks) == 0 {
			c.Next()
			return
		}
		if common.IPInNetworks(c.ClientIP(), networks) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"success": false,
			"code":    "ADMIN_IP_FORBIDDEN",
			"message": "request source address is not in the admin IP whitelist",
		})
	}
}
