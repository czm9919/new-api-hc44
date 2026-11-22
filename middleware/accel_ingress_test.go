package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// 守卫的核心风险是「误伤」：主域名请求、未配置 ingress、CDN 忘了保留 Host，
// 这三种情况都必须放行，否则一个配置失误就是全站故障。
// Host 命中加速域名且无订阅（含匿名 userId=0）必须 403。
// 有订阅放行的路径在真机探针里验证（需要 DB 与订阅数据）。
func TestAccelIngressGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newRouter := func() *gin.Engine {
		r := gin.New()
		r.Use(AccelIngressGuard())
		r.POST("/v1/chat/completions", func(c *gin.Context) {
			c.String(http.StatusOK, "passed")
		})
		return r
	}
	do := func(host string) int {
		r := newRouter()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Host = host
		r.ServeHTTP(w, req)
		return w.Code
	}

	operation_setting.GetAccelSetting().IngressHost = "accel.example.com"
	defer func() { operation_setting.GetAccelSetting().IngressHost = "" }()

	// fail-open：不该拦的请求（未配置 / Host 不匹配）一律放行。
	for _, host := range []string{"api.example.com", "api.example.com.", "accel.example.com.evil.io"} {
		if code := do(host); code != http.StatusOK {
			t.Fatalf("guard must fail open for host %q, got status %d", host, code)
		}
	}

	operation_setting.GetAccelSetting().IngressHost = ""
	if code := do("accel.example.com"); code != http.StatusOK {
		t.Fatalf("guard must fail open when ingress is empty, got status %d", code)
	}

	// 命中加速域名 + 无订阅（匿名 userId=0）→ 403，且是 OpenAI 错误格式。
	operation_setting.GetAccelSetting().IngressHost = "accel.example.com"
	code := do("accel.example.com")
	if code != http.StatusForbidden {
		t.Fatalf("guard must 403 an anonymous request on the accel host, got status %d", code)
	}
	// 归一化的几种等价写法也必须命中（scheme/端口/大小写只是管理员书写差异）。
	for _, host := range []string{"accel.example.com:8443", "ACCEL.EXAMPLE.COM"} {
		if c2 := do(host); c2 != http.StatusForbidden {
			t.Fatalf("guard must 403 host %q on the accel host, got status %d", host, c2)
		}
	}
}

func TestNormalizeIngressHost(t *testing.T) {
	cases := []struct{ raw, want string }{
		{raw: "", want: ""},
		{raw: "  ", want: ""},
		{raw: "accel.example.com", want: "accel.example.com"},
		{raw: "https://accel.example.com/", want: "accel.example.com"},
		{raw: "http://accel.example.com:8080", want: "accel.example.com"},
		{raw: "ACCEL.Example.COM.", want: "accel.example.com"},
		{raw: "https://accel.example.com/v1/path?q=1", want: "accel.example.com"},
	}
	for _, tc := range cases {
		if got := normalizeIngressHost(tc.raw); got != tc.want {
			t.Fatalf("normalizeIngressHost(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
