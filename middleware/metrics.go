package middleware

import (
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/pkg/prommetrics"

	"github.com/gin-gonic/gin"
)

// MetricsRecorder 记录每个 HTTP 请求的方法、路由模板、状态码与耗时
// （v1.1.0 M1 · B08）。挂在全局中间件链最前面，覆盖 api/relay/web 所有
// 出口；路由模板取 gin 的 FullPath（注册时的模板），未匹配路由统一记为
// "unmatched"，具体业务 id 不进标签，避免时间序列基数爆炸。
//
// 本中间件只做观测，不改变任何请求行为；SSE/流式响应的耗时按响应结束
// 计算（与日志中间件口径一致）。
func MetricsRecorder() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		method := c.Request.Method
		status := strconv.Itoa(c.Writer.Status())
		prommetrics.HTTPRequestsTotal.WithLabelValues(method, route, status).Inc()
		prommetrics.HTTPRequestDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
	}
}
