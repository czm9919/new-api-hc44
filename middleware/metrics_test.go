package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/pkg/prommetrics"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// B08：MetricsRecorder 按方法/路由模板/状态码计数，未匹配路由记为
// unmatched；耗时直方图同步更新。
func TestMetricsRecorderCountsRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(MetricsRecorder())
	router.POST("/api/echo", func(c *gin.Context) { c.String(http.StatusTeapot, "brew") })

	before := testutil.ToFloat64(prommetrics.HTTPRequestsTotal.WithLabelValues(http.MethodPost, "/api/echo", "418"))
	unmatchedBefore := testutil.ToFloat64(prommetrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "unmatched", "404"))

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/echo", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/nope", nil))

	assert.Equal(t, before+1, testutil.ToFloat64(prommetrics.HTTPRequestsTotal.WithLabelValues(http.MethodPost, "/api/echo", "418")))
	assert.Equal(t, unmatchedBefore+1, testutil.ToFloat64(prommetrics.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "unmatched", "404")))
	// HistogramVec 的每个子项会展开成 _bucket/_sum/_count 多条序列。
	assert.GreaterOrEqual(t, testutil.CollectAndCount(prommetrics.HTTPRequestDuration, "newapi_http_request_duration_seconds"), 1)
}
