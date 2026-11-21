package router

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const customHeadTestIndex = `<!doctype html><html><head><title>New API</title></head><body><div id="root"></div></body></html>`

func setCustomHeadForTest(t *testing.T, snippet string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, hadPrevious := common.OptionMap[common.CustomHeadHTMLOptionKey]
	common.OptionMap[common.CustomHeadHTMLOptionKey] = snippet
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadPrevious {
			common.OptionMap[common.CustomHeadHTMLOptionKey] = previous
		} else {
			delete(common.OptionMap, common.CustomHeadHTMLOptionKey)
		}
		common.OptionMapRWMutex.Unlock()
	})
}

// 平台要求把 <meta>/<script> 放到 </head> 之前：改完配置后，前台任意路由返回的
// index.html 都必须带上这段内容，而不是只有 / 生效。
func TestWebFallbackInjectsCustomHeadIntoEveryFrontendRoute(t *testing.T) {
	snippet := `<meta name="verify-platform" content="hc24" />`
	setCustomHeadForTest(t, snippet)

	outer := gin.New()
	SetWebRouter(outer, WebAssets{IndexPage: []byte(customHeadTestIndex)}, func(c *gin.Context) { c.Next() })

	for _, path := range []string{"/", "/users", "/enhanced-settings", "/index.html"} {
		t.Run(path, func(t *testing.T) {
			response := performPluginRequest(outer, http.MethodGet, path)
			if response.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", response.Code)
			}
			body := response.Body.String()
			if !strings.Contains(body, snippet) {
				t.Fatalf("custom head snippet missing from %s:\n%s", path, body)
			}
			if strings.Index(body, snippet) > strings.Index(body, "</head>") {
				t.Fatalf("snippet must appear before </head> on %s:\n%s", path, body)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-cache" {
				t.Fatalf("injected page must not be cached, got Cache-Control=%q", got)
			}
		})
	}
}

func TestWebFallbackServesIndexPageUnchangedWithoutCustomHead(t *testing.T) {
	setCustomHeadForTest(t, "")

	outer := gin.New()
	SetWebRouter(outer, WebAssets{IndexPage: []byte(customHeadTestIndex)}, func(c *gin.Context) { c.Next() })

	response := performPluginRequest(outer, http.MethodGet, "/")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	if response.Body.String() != customHeadTestIndex {
		t.Fatalf("page should be served as-is without custom head:\n%s", response.Body.String())
	}
}
