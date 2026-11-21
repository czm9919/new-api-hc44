package middleware

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// hashedAssetPattern 匹配构建产物里带内容哈希的文件名：index.0a5161b638.js、
// 3127.575ee10180.js、lib-react.318395377d.js 这类。哈希进了文件名，内容一变
// URL 就变，所以可以长期缓存。
var hashedAssetPattern = regexp.MustCompile(`[.-][0-9a-f]{8,}\.(?:js|mjs|css|woff2?|ttf|otf|eot|svg|png|jpe?g|gif|webp|avif|ico|wasm|map)$`)

// isImmutableAssetPath 判断请求是否命中「内容寻址」的静态资源
// （v1.1.0 M2 · B16）。只对 /static/ 与 /assets/ 下的哈希文件放长缓存：
// 未带哈希的静态文件（如 /logo.png）内容可能被原地替换，仍按一周缓存。
func isImmutableAssetPath(path string) bool {
	if !strings.HasPrefix(path, "/static/") && !strings.HasPrefix(path, "/assets/") {
		return false
	}
	return hashedAssetPattern.MatchString(path)
}

func Cache() func(c *gin.Context) {
	return func(c *gin.Context) {
		switch {
		case c.Request.RequestURI == "/":
			c.Header("Cache-Control", "no-cache")
		case isImmutableAssetPath(c.Request.URL.Path):
			// 文件名带内容哈希：内容变更必然换 URL，可以 immutable 一年。
			// 之前统一 max-age=604800，用户每周都要重新校验一次，白白多一次
			// 往返；immutable 让浏览器连条件请求都不发。
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		default:
			c.Header("Cache-Control", "max-age=604800") // one week
		}
		c.Header("Cache-Version", "b688f2fb5be447c25e5aa3bd063087a83db32a288bf6a4f35f2d8db310e40b14")
		c.Next()
	}
}
