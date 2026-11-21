package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setMetricsOptions(enabled bool, token string) (bool, string) {
	previousEnabled, previousToken := common.MetricsEnabled, common.MetricsToken
	common.MetricsEnabled = enabled
	common.MetricsToken = token
	return previousEnabled, previousToken
}

// B08：/healthz 存活探针不带任何依赖检查；/metrics 输出 Prometheus 文本，
// 禁用后表现为端点不存在，配置 token 后凭据门禁用常数时间比较。
func TestHealthzAndMetricsHandlers(t *testing.T) {
	previousEnabled, previousToken := setMetricsOptions(true, "")
	t.Cleanup(func() {
		common.MetricsEnabled = previousEnabled
		common.MetricsToken = previousToken
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/healthz", Healthz)
	router.GET("/metrics", Metrics)

	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, health.Code)
	assert.Contains(t, health.Body.String(), `"status":"ok"`)
	assert.Contains(t, health.Body.String(), `"version"`)

	metrics := httptest.NewRecorder()
	router.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, metrics.Code)
	// CounterVec/HistogramVec 在没有子项时不会输出任何样本行，所以这里断言
	// 恒定存在的 build_info 与 uptime；请求量指标由 middleware 侧测试覆盖。
	assert.Contains(t, metrics.Body.String(), "newapi_build_info")
	assert.Contains(t, metrics.Body.String(), "newapi_uptime_seconds")

	// 禁用后 behaves like a nonexistent endpoint。
	common.MetricsEnabled = false
	disabled := httptest.NewRecorder()
	router.ServeHTTP(disabled, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusNotFound, disabled.Code)

	// token 门禁：无凭证 401，错凭证 401，对凭证放行。
	common.MetricsEnabled = true
	common.MetricsToken = "s3cret"
	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	wrongToken := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer wrong")
	router.ServeHTTP(wrongToken, request)
	assert.Equal(t, http.StatusUnauthorized, wrongToken.Code)

	authorized := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer s3cret")
	router.ServeHTTP(authorized, request)
	assert.Equal(t, http.StatusOK, authorized.Code)
	assert.True(t, strings.Contains(authorized.Body.String(), "newapi_build_info"))
}
