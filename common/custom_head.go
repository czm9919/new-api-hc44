package common

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
)

// =============================================================================
// 自定义 Head 代码（hc24）：把管理员填写的 HTML 片段注入到首页 index.html 的
// </head> 之前。
//
// 为什么需要它：
//  - 「系统设置 → 站点 → 首页内容」(HomePageContent) 渲染的是页面**正文区**的
//    Markdown，写进去的 <meta>/<script> 既进不了 <head>，也不会被执行，
//    所以拿来做站长验证、统计代码、第三方挂件一律无效；
//  - 上游只支持环境变量 UMAMI_WEBSITE_ID / GOOGLE_ANALYTICS_ID 两种统计，
//    且必须重启进程，覆盖不了「平台要求把一段标签放在 </head> 前」这类需求。
//
// 实现要点：
//  - 服务端在返回 index.html 时注入，天然对所有 SPA 路由生效（它们共用同一个页面）；
//  - 结果按「片段内容 + 基础页长度」缓存，配置不变时不重复拼接；
//  - 注入点用 LastIndex 找最后一个 </head>，避免被页面脚本里的字符串误导；
//  - 配置改了立即生效（下次请求就用新值），不需要重启。
// =============================================================================

// CustomHeadHTMLOptionKey 自定义 Head 代码的 option key。
const CustomHeadHTMLOptionKey = "CustomHeadHTML"

// customHeadHTMLMaxBytes 片段长度上限，防止把整份 HTML 或超大脚本塞进配置表。
const customHeadHTMLMaxBytes = 64 * 1024

var (
	customHeadCacheMu  sync.RWMutex
	customHeadCacheKey string
	customHeadCacheVal []byte
)

// GetCustomHeadHTML 返回管理员配置的自定义 Head 片段（已去首尾空白）。
func GetCustomHeadHTML() string {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	return strings.TrimSpace(OptionMap[CustomHeadHTMLOptionKey])
}

// IndexPageHTML 把自定义 Head 片段注入到 index.html 后返回。
// 未配置片段、或页面里没有 </head> 时原样返回，保证不会因为配置问题白屏。
func IndexPageHTML(base []byte) []byte {
	snippet := GetCustomHeadHTML()
	// 片段的空/非空切换必须重新计算，所以缓存键里带上片段内容；
	// 再带上基础页长度，避免不同 base（测试场景）互相污染。
	key := strconv.Itoa(len(base)) + "\x00" + snippet

	customHeadCacheMu.RLock()
	if customHeadCacheVal != nil && customHeadCacheKey == key {
		cached := customHeadCacheVal
		customHeadCacheMu.RUnlock()
		return cached
	}
	customHeadCacheMu.RUnlock()

	out := InjectBeforeHeadClose(base, snippet)

	customHeadCacheMu.Lock()
	customHeadCacheKey = key
	customHeadCacheVal = out
	customHeadCacheMu.Unlock()
	return out
}

// InjectBeforeHeadClose 把片段插到最后一个 </head> 之前。
// 找不到 </head>（例如测试用的占位内容）时原样返回。
func InjectBeforeHeadClose(base []byte, snippet string) []byte {
	snippet = strings.TrimSpace(snippet)
	if snippet == "" {
		return base
	}
	idx := bytes.LastIndex(base, []byte("</head>"))
	if idx < 0 {
		return base
	}
	out := make([]byte, 0, len(base)+len(snippet)+2)
	out = append(out, base[:idx]...)
	// 让注入内容独占一行，方便查看源码时定位
	if idx > 0 && base[idx-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, snippet...)
	out = append(out, '\n')
	out = append(out, base[idx:]...)
	return out
}

// ValidateCustomHeadHTML 校验自定义 Head 片段。
// 只拦「明显会破坏文档结构」的写法，其余内容按管理员填写原样输出。
func ValidateCustomHeadHTML(value string) error {
	if len(value) > customHeadHTMLMaxBytes {
		return NewMessage("自定义 Head 代码过长（上限 64KB），请精简后再保存")
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "<html") {
		return NewMessage("只需填写要插入 <head> 内部的片段，不要包含 <html> 整个文档")
	}
	if strings.Contains(lower, "</head>") {
		return NewMessage("片段会自动插入到 </head> 之前，无需自己再写 </head>")
	}
	return nil
}
