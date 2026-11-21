package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// TestCacheControlByAssetKind 覆盖 B16 的三条分支：入口页不缓存、带哈希的
// 构建产物 immutable 长缓存、其余静态文件保持一周。
func TestCacheControlByAssetKind(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		path     string
		expected string
	}{
		{"/", "no-cache"},
		{"/static/js/index.0a5161b638.js", "public, max-age=31536000, immutable"},
		{"/static/css/index.9f3ab2107c.css", "public, max-age=31536000, immutable"},
		{"/static/js/async/3127.575ee10180.js", "public, max-age=31536000, immutable"},
		{"/static/svg/custom.svg", "max-age=604800"},
		{"/logo.png", "max-age=604800"},
		{"/unknown", "max-age=604800"},
	}

	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("GET", tc.path, nil)
		Cache()(c)
		assert.Equal(t, tc.expected, recorder.Header().Get("Cache-Control"), "path=%s", tc.path)
	}
}
