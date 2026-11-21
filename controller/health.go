package controller

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/prommetrics"

	"github.com/gin-gonic/gin"
)

// Healthz 是给 K8s / LB 用的存活探针（v1.1.0 M1 · B08）。
//
// 刻意不查数据库、不查 Redis：存活探针要回答的是「进程还活着、HTTP 栈还能
// 出响应」。把数据库可用性塞进存活探针会让一次库抖动触发全量重启（把本来
// 只是「暂时不可用」的实例全部打挂）。需要判断依赖是否就绪，看 /metrics
// 里的 go_* 指标与业务告警，或自行探 /api/status。
func Healthz(c *gin.Context) {
	uptime := int64(0)
	if common.StartTime > 0 {
		uptime = int64(time.Since(time.Unix(common.StartTime, 0)).Seconds())
	}
	c.JSON(http.StatusOK, gin.H{
		"status":         "ok",
		"version":        common.Version,
		"uptime_seconds": uptime,
		"timestamp":      common.GetTimestamp(),
	})
}

// Metrics 暴露 Prometheus 指标（v1.1.0 M1 · B08）。
//
// 开关 MetricsEnabled 关闭时直接 404（当作端点不存在，不给扫描器留指纹）；
// 配置了 MetricsToken 时要求 `Authorization: Bearer <token>`，用常数时间
// 比较避免逐字节试探。默认无 token：指标里只有路由模板与状态码，不含业务
// 数据，通常已由内网/网关限制访问面。
func Metrics(c *gin.Context) {
	if !common.MetricsEnabled {
		c.Status(http.StatusNotFound)
		return
	}
	if token := strings.TrimSpace(common.MetricsToken); token != "" {
		provided := strings.TrimSpace(c.GetHeader("Authorization"))
		if len(provided) >= 7 && strings.EqualFold(provided[:7], "Bearer ") {
			provided = strings.TrimSpace(provided[7:])
		}
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			c.Header("WWW-Authenticate", `Bearer realm="metrics"`)
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"code":    "METRICS_UNAUTHORIZED",
				"message": "metrics endpoint requires a bearer token",
			})
			return
		}
	}
	prommetrics.Handler().ServeHTTP(c.Writer, c.Request)
}
