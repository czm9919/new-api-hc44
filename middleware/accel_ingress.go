package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// 加速域名订阅守卫（hc51）
// ============================================================================
//
// 加速的本质是「用户 → 站点」这一段走 CDN，这段发生在请求到达站点之前，
// 渠道调度和分组都控制不了它。所以付费门槛只能卡在入口：
// 请求的 Host 等于配置的加速域名、且用户没有生效订阅时，直接 403。
//
// 故意 fail-open：ingress 未配置、或 Host 对不上（比如 CDN 回源没保留 Host、
// 有人拿 IP 直连）时一律放行走主链路。守卫的职责是把「占便宜」堵住，
// 不是把配错的 CDN 变成全站故障。
// ============================================================================

// normalizeIngressHost 归一化用于比较的域名：去 scheme、去路径、去端口、小写。
func normalizeIngressHost(raw string) string {
	host := strings.TrimSpace(raw)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {
		host = host[:i]
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

// AccelIngressGuard 拦下「从加速域名进来但没有生效订阅」的 relay 请求。
// 必须挂在 TokenAuth 之后（需要 userId），Distribute 之前（省掉无用调度）。
func AccelIngressGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		ingress := normalizeIngressHost(operation_setting.GetAccelSetting().IngressHost)
		if ingress == "" {
			c.Next()
			return
		}
		if normalizeIngressHost(c.Request.Host) != ingress {
			c.Next()
			return
		}
		if userId := c.GetInt("id"); userId > 0 {
			sub, err := service.GetActiveSubscription(userId)
			if err == nil && sub != nil && sub.Active(time.Now().Unix()) {
				c.Next()
				return
			}
		}
		// OpenAI 错误格式，客户端 SDK 可以直接解析展示。
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"message": "加速线路需要订阅加速套餐后使用 (ACCEL_SUBSCRIPTION_REQUIRED)",
				"type":    "new_api_error",
				"code":    "ACCEL_SUBSCRIPTION_REQUIRED",
			},
		})
		c.Abort()
	}
}
