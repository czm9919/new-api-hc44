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

// 水印前缀剥离（hc52）：只认「8 位 hex + 紧跟斜杠」的形态。
// 非 hex 段（/dashboard 等）和不足 8 位（/v1、/ab12）都不能误剥，
// 否则加速域名下的普通路径会被错误重写。
func TestSplitAccelPrefix(t *testing.T) {
	cases := []struct {
		path      string
		wantPfx   string
		wantRest  string
		wantMatch bool
	}{
		{path: "/a3f2b91c/v1/chat/completions", wantPfx: "a3f2b91c", wantRest: "/v1/chat/completions", wantMatch: true},
		{path: "/00000000/", wantPfx: "00000000", wantRest: "/", wantMatch: true},
		{path: "/A3F2B91C/v1/models", wantPfx: "A3F2B91C", wantRest: "/v1/models", wantMatch: true},
		{path: "/a3f2b91c/", wantPfx: "a3f2b91c", wantRest: "/", wantMatch: true},
		// 不命中的形态：原样返回路径（pfx 为空串）。
		{path: "/v1/chat/completions", wantPfx: "", wantRest: "/v1/chat/completions", wantMatch: false}, // 太短
		{path: "/a3f2b91/v1", wantPfx: "", wantRest: "/a3f2b91/v1", wantMatch: false},                   // 7 位
		{path: "/a3f2b91cx/v1", wantPfx: "", wantRest: "/a3f2b91cx/v1", wantMatch: false},               // 第 8 位后不是斜杠
		{path: "/dashboar/v1", wantPfx: "", wantRest: "/dashboar/v1", wantMatch: false},                 // 非 hex
		{path: "/a3f2b91c", wantPfx: "", wantRest: "/a3f2b91c", wantMatch: false},                       // 不足 10 字符（无尾斜杠）
	}
	for _, tc := range cases {
		pfx, rest, ok := splitAccelPrefix(tc.path)
		if ok != tc.wantMatch || pfx != tc.wantPfx || rest != tc.wantRest {
			t.Fatalf("splitAccelPrefix(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.path, pfx, rest, ok, tc.wantPfx, tc.wantRest, tc.wantMatch)
		}
	}
}

// rewrite 层的行为边界：主域名永不剥前缀；加速域名 + 带前缀 → 重路由后
// 到达处理器时路径已剥掉前缀、ctx 里带原始前缀。
func TestAccelIngressRewrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	operation_setting.GetAccelSetting().IngressHost = "accel.example.com"
	defer func() { operation_setting.GetAccelSetting().IngressHost = "" }()

	r := gin.New()
	r.Use(AccelIngressRewrite(r))
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		c.String(http.StatusOK, "prefix=%s path=%s",
			accelPrefixOf(c), c.Request.URL.Path)
	})

	// 主域名：即使路径形如 /<prefix>/v1/... 也不剥（落到 404，由前端路由处理）。
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/a3f2b91c/v1/chat/completions", nil)
	req.Host = "api.example.com"
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("main host must not strip prefix, got status %d", w.Code)
	}

	// 加速域名：剥前缀 → 重新路由命中 /v1 处理器，ctx 保留原始前缀。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/a3f2b91c/v1/chat/completions", nil)
	req.Host = "accel.example.com"
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("accel host with prefix must rewrite to handler, got status %d", w.Code)
	}
	if w.Body.String() != "prefix=a3f2b91c path=/v1/chat/completions" {
		t.Fatalf("unexpected rewritten request: %s", w.Body.String())
	}
}
