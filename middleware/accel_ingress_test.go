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

// hc56：CDN 回源改写 Host 后的域名识别。
// 生产上 CDN 常把 Host 改成源站 IP/源站域名，真实域名只在 X-Forwarded-Host
// （或 X-Original-Host）里。只认 Host 会让加速机制静默失效，用户看到的是
// 「链接打不开 / 都是 404」。这里锁住三种来源的识别，以及主站不被误伤。
func TestIsAccelIngressRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name    string
		host    string
		headers map[string]string
		want    bool
	}{
		{name: "host 直接命中", host: "accel.example.com", want: true},
		{name: "host 带端口", host: "accel.example.com:8443", want: true},
		{name: "host 大小写", host: "ACCEL.Example.com", want: true},
		{name: "CDN 回源 IP + XFH", host: "203.0.113.9:3000",
			headers: map[string]string{"X-Forwarded-Host": "accel.example.com"}, want: true},
		{name: "XFH 多级链取第一个", host: "203.0.113.9",
			headers: map[string]string{"X-Forwarded-Host": "accel.example.com, edge.internal"}, want: true},
		{name: "X-Original-Host", host: "10.0.0.2",
			headers: map[string]string{"X-Original-Host": "accel.example.com"}, want: true},
		{name: "CDN 回源 IP 且无任何头", host: "203.0.113.9:3000", want: false},
		{name: "主域名 + 无关 XFH", host: "api.example.com",
			headers: map[string]string{"X-Forwarded-Host": "other.example.com"}, want: false},
		{name: "相似域名不算命中", host: "accel.example.com.evil.io", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c.Request.Host = tc.host
			for k, v := range tc.headers {
				c.Request.Header.Set(k, v)
			}
			if got := isAccelIngressRequest(c, "accel.example.com"); got != tc.want {
				t.Fatalf("isAccelIngressRequest(host=%q, headers=%v) = %v, want %v",
					tc.host, tc.headers, got, tc.want)
			}
		})
	}
}

// hc56：SDK 拼 base_url 常见多出一个斜杠（/<prefix>//v1/...），
// 不合并就会掉到前端兜底，客户端拿到 HTML 报解析失败。
func TestCollapseSlashes(t *testing.T) {
	cases := []struct{ in, want string }{
		{in: "/v1/chat/completions", want: "/v1/chat/completions"},
		{in: "//v1/chat/completions", want: "/v1/chat/completions"},
		{in: "/v1//chat//completions", want: "/v1/chat/completions"},
		{in: "/", want: "/"},
		{in: "", want: ""},
	}
	for _, tc := range cases {
		if got := collapseSlashes(tc.in); got != tc.want {
			t.Fatalf("collapseSlashes(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// hc56：重路由后 RequestURI 必须跟着改。
// 前端兜底用 RequestURI 的前缀判断「这是不是 API 路径」，不改就会把
// JSON 404 变成 index.html（客户端只能看到「响应解析失败」）。
func TestAccelIngressRewriteUpdatesRequestURI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	operation_setting.GetAccelSetting().IngressHost = "accel.example.com"
	defer func() { operation_setting.GetAccelSetting().IngressHost = "" }()

	r := gin.New()
	r.Use(AccelIngressRewrite(r))
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		c.String(http.StatusOK, "uri=%s path=%s", c.Request.RequestURI, c.Request.URL.Path)
	})

	// 双斜杠 + query：剥前缀、合并斜杠、query 原样保留。
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/a3f2b91c//v1/chat/completions?stream=true", nil)
	req.Host = "accel.example.com"
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("double slash must be collapsed and rewritten, got status %d", w.Code)
	}
	if want := "uri=/v1/chat/completions?stream=true path=/v1/chat/completions"; w.Body.String() != want {
		t.Fatalf("got %q, want %q", w.Body.String(), want)
	}

	// CDN 改写 Host：靠 X-Forwarded-Host 也能剥前缀。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/a3f2b91c/v1/chat/completions", nil)
	req.Host = "203.0.113.9:3000"
	req.Header.Set("X-Forwarded-Host", "accel.example.com")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CDN rewritten host must still rewrite, got status %d", w.Code)
	}
	if want := "uri=/v1/chat/completions path=/v1/chat/completions"; w.Body.String() != want {
		t.Fatalf("got %q, want %q", w.Body.String(), want)
	}
}
