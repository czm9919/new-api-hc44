package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
		if ingress == "" || normalizeIngressHost(c.Request.Host) != ingress {
			c.Next()
			return
		}
		prefix, rest, ok := splitAccelPrefix(c.Request.URL.Path)
		if !ok {
			// 加速域名下不带前缀的路径：如果恰好命中 /v1 等注册路由，
			// 会由 relay 组内的 AccelIngressGuard 拦截；其余交给 404。
			c.Next()
			return
		}
		withAccelIngressCtx(c, prefix)
		c.Request.URL.Path = rest
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
		if normalizeIngressHost(c.Request.Host) != ingress {
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
					"message": "加速线路需要订阅加速套餐后使用 (ACCEL_SUBSCRIPTION_REQUIRED)",
					"type":    "new_api_error",
					"code":    "ACCEL_SUBSCRIPTION_REQUIRED",
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
					},
				})
				c.Abort()
				return
			}
			release = globalRelease
		}

		// 专属渠道生效点（hc53 修复）：请求进来时令牌/用户还是原分组
		// （如 default），EnterAccelChannel 在 TokenAuth 阶段看到的也是旧
		// 分组——守卫只验票不切分组的话，调度照样落普通渠道，「加速链接」
		// 等于白给。这里把 UsingGroup 切到订阅绑定的分组，让 Distribute
		// 用加速分组的渠道，并补占分组并发槽位。
		prevGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
		if prevGroup != sub.GroupName {
			plan, perr := service.AccelPlanGroup(sub.GroupName)
			if perr != nil {
				release()
				reject("accel plan lookup failed: " + perr.Error())
				return
			}
			if plan == nil {
				release()
				reject("subscription group has no accel plan")
				return
			}
			common.SetContextKey(c, constant.ContextKeyUsingGroup, sub.GroupName)
			slotRelease, ok := service.AcquireAccelSlot(prefixUserId, plan.Concurrency)
			if !ok {
				release()
				ingressDenied.Add(1)
				common.SysLog(common.LogText(
					"accel ingress plan limit: user=%d ip=%s concurrency=%d",
					prefixUserId, c.ClientIP(), plan.Concurrency))
				c.JSON(http.StatusTooManyRequests, gin.H{
					"error": gin.H{
						"message": "加速线路当前繁忙，请稍后重试 (ACCEL_LINE_BUSY)",
						"type":    "new_api_error",
						"code":    "ACCEL_LINE_BUSY",
					},
				})
				c.Abort()
				return
			}
			prevRelease := release
			release = func() {
				slotRelease()
				prevRelease()
			}
		}

		ingressAllowed.Add(1)
		common.SysLog(common.LogText(
			"accel ingress allowed: user=%d prefix=%q group=%q ip=%s ua=%q",
			prefixUserId, prefix, sub.GroupName, c.ClientIP(), c.Request.UserAgent()))
		c.Next()
		// c.Next() 返回 = relay handler 已完成（含流式写完），此刻释放。
		release()
	}
}
