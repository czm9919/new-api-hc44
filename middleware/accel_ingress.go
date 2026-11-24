package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// 加速域名接入（hc51 守卫 + hc52 路径水印升级）
// ============================================================================
//
// 加速的本质是「用户 → 站点」这一段走专用域名，这段发生在请求到达站点之前，
// 渠道调度和分组都控制不了。所以付费门槛卡在入口，分两层：
//
//   1. AccelIngressRewrite（全局，路由匹配前）：加速域名下 /<prefix>/ 开头的
//      路径剥掉水印前缀后重新路由。前缀本身不在这里校验（还没有用户信息）。
//   2. AccelIngressGuard（relay 组内，TokenAuth 后）：校验前缀有效、
//      前缀对应用户与令牌一致、订阅生效。三关任一不过直接 403。
//
// 水印的意义（hc52）：一人一前缀，泄露即可溯源、可单点封禁；
// 拿别人的前缀配自己的令牌也过不了绑定校验。
//
// fail-open 边界：ingress 未配置、或 Host 对不上（有人拿 IP 直连）时
// 一律放行走主链路。守卫的职责是把「占便宜」堵住，不是把配错的域名
// 变成全站故障。
// ============================================================================

const accelReroutedFlag = "accel_ingress_rerouted"

// accelIngressCtx 前缀与重路由标记跨 HandleContext 传递的载体。
// gin 的 HandleContext 会 c.reset() 清空 gin.Context 的 keys，
// 所以不能 c.Set / c.GetBool，必须挂在 *http.Request 的 context 上
// （HandleContext 只换 handlers 链，不动 Request）。
type accelIngressCtxKey int

const (
	accelPrefixCtxKey   accelIngressCtxKey = 0
	accelReroutedCtxKey accelIngressCtxKey = 1
)

func accelPrefixOf(c *gin.Context) string {
	v, _ := c.Request.Context().Value(accelPrefixCtxKey).(string)
	return v
}

// AccelIngressPrefix 返回本次请求从加速链接带来的水印前缀，主站请求为空。
// 供前端兜底路由判断「未匹配的 API 路径要不要回 JSON 而不是 index.html」。
func AccelIngressPrefix(c *gin.Context) string { return accelPrefixOf(c) }

func withAccelIngressCtx(c *gin.Context, prefix string) {
	ctx := c.Request.Context()
	ctx = context.WithValue(ctx, accelReroutedCtxKey, true)
	ctx = context.WithValue(ctx, accelPrefixCtxKey, prefix)
	c.Request = c.Request.WithContext(ctx)
}

// ingressAllowed / ingressDenied 守卫放行/拦截计数（hc52 · 运营监控）。
// 拦截数突然暴涨 ≈ 域名或前缀被人拿去试了。
var (
	ingressAllowed atomic.Int64
	ingressDenied  atomic.Int64
)

// AccelIngressStats 管理端读取计数快照。
func AccelIngressStats() (allowed int64, denied int64) {
	return ingressAllowed.Load(), ingressDenied.Load()
}

// normalizeIngressHost 归一化用于比较的域名：去 scheme、去路径、去端口、小写。
func normalizeIngressHost(raw string) string {
	host := strings.TrimSpace(raw)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {
		host = host[:i]
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

// isAccelIngressRequest 判断本次请求是不是从加速域名进来的（hc56）。
//
// 只认 c.Request.Host 在 CDN 后面必然失效：CDN 回源时 Host 常被改写成
// 源站 IP 或源站域名，真实域名留在 X-Forwarded-Host（Cloudflare / 腾讯云 /
// 阿里云都支持），部分 CDN 用 X-Original-Host。Host 一旦对不上，整个加速
// 机制就静默降级——前缀不被剥离，路径原样落到前端兜底，客户端拿到的是
// 一坨 HTML，表现就是「链接打不开 / 都是 404」。
//
// 安全性：多带这两个头只会让请求进入更严格的加速分支（要求有效水印前缀 +
// 本人令牌 + 生效订阅），伪造它们只会把自己挡在门外，不存在越权。
func isAccelIngressRequest(c *gin.Context, ingress string) bool {
	if ingress == "" {
		return false
	}
	if normalizeIngressHost(c.Request.Host) == ingress {
		return true
	}
	for _, header := range []string{"X-Forwarded-Host", "X-Original-Host"} {
		raw := c.GetHeader(header)
		if raw == "" {
			continue
		}
		// 多级代理会给出逗号分隔的链，最左侧是客户端视角的原始 Host。
		if i := strings.IndexByte(raw, ','); i >= 0 {
			raw = raw[:i]
		}
		if normalizeIngressHost(raw) == ingress {
			return true
		}
	}
	return false
}

// collapseSlashes 合并连续斜杠。
// 很多 SDK 在拼接 base_url 时会多出「/<prefix>//v1/...」这种双斜杠，
// 路由表里没有对应条目，会一路掉到前端兜底返回 HTML。
func collapseSlashes(path string) string {
	if !strings.Contains(path, "//") {
		return path
	}
	out := make([]byte, 0, len(path))
	prevSlash := false
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			if prevSlash {
				continue
			}
			prevSlash = true
		} else {
			prevSlash = false
		}
		out = append(out, path[i])
	}
	return string(out)
}

// splitAccelPrefix 判断路径是否以 /<8位hex前缀>/ 开头。
// 返回 (前缀, 剥掉前缀后的路径, 是否命中)。
// 只对 Host == 加速域名的请求调用（rewrite 内部保证），主域名不受影响。
func splitAccelPrefix(path string) (string, string, bool) {
	// 最短合法形态：/xxxxxxxx/（10 字符）；不带尾斜杠的 /xxxxxxxx 不算前缀。
	if len(path) < 10 || path[0] != '/' {
		return "", path, false
	}
	seg := path[1:9]
	for i := 0; i < len(seg); i++ {
		r := seg[i]
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex {
			return "", path, false
		}
	}
	if path[9] != '/' {
		return "", path, false
	}
	return seg, path[9:], true
}

// AccelIngressRewrite 加速域名的水印前缀剥离与重路由（hc52）。
//
// 挂在所有全局中间件之前：gin 的路由匹配发生在中间件之后不可改写，
// 所以这里改完路径要用 HandleContext 重新走一次完整中间件链。
// rerouted 标记保证重入时直接放行——其余中间件（解压/统计/日志）
// 只执行一次，请求体不会二次解压。
func AccelIngressRewrite(engine *gin.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		if v, _ := c.Request.Context().Value(accelReroutedCtxKey).(bool); v {
			c.Next()
			return
		}
		ingress := normalizeIngressHost(operation_setting.GetAccelSetting().IngressHost)
		if !isAccelIngressRequest(c, ingress) {
			c.Next()
			return
		}
		prefix, rest, ok := splitAccelPrefix(c.Request.URL.Path)
		if !ok {
			// 加速域名下不带前缀的路径：如果恰好命中 /v1 等注册路由，
			// 会由 relay 组内的 AccelIngressGuard 拦截；其余交给前端兜底。
			c.Next()
			return
		}
		rest = collapseSlashes(rest)
		withAccelIngressCtx(c, prefix)
		c.Request.URL.Path = rest
		c.Request.URL.RawPath = ""
		// RequestURI 必须同步重写（hc56）：NoRoute 兜底里用 RequestURI 前缀
		// 判断「是不是 API 路径」，重路由后它仍是 /<prefix>/v1/...，
		// 判断失配就会把 JSON 404 变成一坨 index.html。
		if c.Request.URL.RawQuery != "" {
			c.Request.RequestURI = rest + "?" + c.Request.URL.RawQuery
		} else {
			c.Request.RequestURI = rest
		}
		c.Abort()
		engine.HandleContext(c)
	}
}

// AccelIngressGuard 拦下「从加速域名进来但没有生效订阅」的 relay 请求。
// 必须挂在 TokenAuth 之后（需要 userId），Distribute 之前（省掉无用调度）。
func AccelIngressGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		ingress := normalizeIngressHost(operation_setting.GetAccelSetting().IngressHost)
		if ingress == "" {
			c.Next()
			return
		}
		if !isAccelIngressRequest(c, ingress) {
			c.Next()
			return
		}

		reject := func(reason string) {
			ingressDenied.Add(1)
			// 留痕（hc52 ⑦）：被拒的请求记一行，出问题时有据可查。
			common.SysLog(common.LogText(
				"accel ingress denied: user=%d prefix=%q ip=%s ua=%q reason=%s",
				c.GetInt("id"), accelPrefixOf(c),
				c.ClientIP(), c.Request.UserAgent(), reason))
			c.JSON(http.StatusForbidden, gin.H{
				"error": gin.H{
					"message": "加速线路需要订阅加速套餐后使用，订阅后到「加速」页复制专属接入地址 (ACCEL_SUBSCRIPTION_REQUIRED)",
					"type":    "new_api_error",
					"code":    "ACCEL_SUBSCRIPTION_REQUIRED",
					// 自助引导（hc55）：客户端把这段原样展示出来时，用户能直接找到入口。
					"upgrade_url": "/accel",
				},
			})
			c.Abort()
		}

		prefix := accelPrefixOf(c)
		if prefix == "" {
			// 裸域名直连（不带水印前缀）：hc51 下发过的旧地址就此作废。
			reject("missing access prefix")
			return
		}
		prefixUserId, err := service.AccelUserIdByPrefix(prefix)
		if err != nil {
			reject("unknown access prefix")
			return
		}
		if userId := c.GetInt("id"); userId == 0 || userId != prefixUserId {
			// 水印绑定：前缀和令牌必须是同一个人，借别人的前缀无效。
			reject("prefix does not match token user")
			return
		}
		sub, err := service.GetActiveSubscription(prefixUserId)
		if err != nil {
			// 查库失败 fail-closed：与订阅服务口径一致，不放行未确认的付费通道。
			reject("subscription lookup failed: " + err.Error())
			return
		}
		if sub == nil || !sub.Active(time.Now().Unix()) {
			reject("no active subscription")
			return
		}

		// 全局并发兜底（hc52 · ⑥）：所有订阅用户共享上限，域名被传播滥用时
		// 损失封顶。挂在订阅校验之后——白嫖请求在这里已经全被 403，
		// 槽位只服务真正的付费流量。
		//
		// hc54 定位修正：加速链接 = 套了 CDN 的门牌，调度/渠道/计费与主站
		// 完全一致（老板明确要求，不要加速分组）。守卫只验票，绝不碰
		// UsingGroup——请求走什么渠道由令牌/用户的原分组决定。
		release := func() {}
		if limit := operation_setting.GetAccelSetting().GlobalConcurrency; limit > 0 {
			globalRelease, ok := service.AcquireGlobalAccelSlot(limit)
			if !ok {
				ingressDenied.Add(1)
				common.SysLog(common.LogText(
					"accel ingress global limit: user=%d ip=%s limit=%d",
					prefixUserId, c.ClientIP(), limit))
				c.JSON(http.StatusTooManyRequests, gin.H{
					"error": gin.H{
						"message": "加速线路当前繁忙，请稍后重试 (ACCEL_LINE_BUSY)",
						"type":    "new_api_error",
						"code":    "ACCEL_LINE_BUSY",
						// 429 不引导升级（用户已经付费），只提示稍后重试。
						"retry_after_seconds": 3,
					},
				})
				c.Abort()
				return
			}
			release = globalRelease
		}

		ingressAllowed.Add(1)
		common.SysLog(common.LogText(
			"accel ingress allowed: user=%d prefix=%q ip=%s ua=%q",
			prefixUserId, prefix, c.ClientIP(), c.Request.UserAgent()))
		c.Next()
		// c.Next() 返回 = relay handler 已完成（含流式写完），此刻释放。
		release()
	}
}
