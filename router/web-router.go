package router

import (
	"embed"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// WebAssets holds the embedded dashboard frontend assets.
type WebAssets struct {
	BuildFS   embed.FS
	IndexPage []byte
}

func SetWebRouter(router *gin.Engine, assets WebAssets, pluginDispatcher gin.HandlerFunc) {
	frontendFS := common.EmbedFolder(assets.BuildFS, "web/dist")
	compress := gzip.Gzip(gzip.DefaultCompression)

	router.NoRoute(
		pluginDispatcher,
		middleware.RouteTag("web"),
		func(c *gin.Context) {
			// ServeFrontendFiles sends build files already compressed.
			if !frontendFS.Exists("/", c.Request.URL.Path) {
				compress(c)
			}
		},
		middleware.AccessTokenAudit(),
		middleware.GlobalWebRateLimit(frontendFS),
		middleware.Cache(),
		// 直接访问 /index.html 时，静态文件分支不会走下面的注入逻辑，
		// 而部分站长平台/验证爬虫恰恰会请求这个路径，所以单独兜一层。
		func(c *gin.Context) {
			if c.Request.URL.Path != "/index.html" {
				c.Next()
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8",
				common.IndexPageHTMLWithNonce(assets.IndexPage, middleware.CSPNonce(c)))
			c.Abort()
		},
		middleware.ServeFrontendFiles(frontendFS),
		func(c *gin.Context) {
			if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") || strings.HasPrefix(c.Request.RequestURI, "/assets") {
				controller.RelayNotFound(c)
				return
			}
			c.Header("Cache-Control", "no-cache")
			// 自定义 Head 代码在这里注入：所有前台路由共用同一份 index.html，
			// 因此改一次配置就对全站生效（详见 common/custom_head.go）。
			// CSP nonce 最后一步注入（v1.1.0 M1 · B139）：内联脚本按本次请求
			// 的 nonce 放行，自定义 Head 里的内联脚本也一并覆盖。
			c.Data(http.StatusOK, "text/html; charset=utf-8",
				common.IndexPageHTMLWithNonce(assets.IndexPage, middleware.CSPNonce(c)))
		},
	)
}
