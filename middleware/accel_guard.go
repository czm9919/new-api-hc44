package middleware

import (
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// EnterAccelChannel 加速专线守卫（hc48）。
//
// 只在请求落在「加速套餐绑定的分组」时才做额外检查，普通分组直接放行，
// 所以对 default/vip 的请求几乎没有额外开销（命中一次套餐缓存就返回）。
//
// 检查三件事，任一不过就拒绝：
//  1. 订阅存在且分组匹配 —— 防的是「订阅了 A 套餐却去用 B 分组」；
//  2. 订阅未过期 —— 到期瞬间兜底，不依赖前端提示；
//  3. 并发未超上限 —— 防一个账号开几十个并发把专线带宽吃满。
//
// 返回的 release 必须在请求结束时调用；流式响应被客户端中断时
// gin 仍会走到 defer，所以不会泄漏槽位。
func EnterAccelChannel(c *gin.Context) (func(), bool) {
	noop := func() {}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "" || group == "auto" {
		return noop, true
	}
	plan, err := service.AccelPlanGroup(group)
	if err != nil {
		abortWithOpenAiMessage(c, http.StatusInternalServerError, i18n.T(c, i18n.MsgDatabaseError))
		return noop, false
	}
	if plan == nil {
		return noop, true
	}

	userId := c.GetInt("id")
	sub, err := service.GetActiveSubscription(userId)
	if err != nil {
		abortWithOpenAiMessage(c, http.StatusInternalServerError, i18n.T(c, i18n.MsgDatabaseError))
		return noop, false
	}
	if sub == nil || sub.GroupName != group {
		abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgAccelSubscriptionRequired))
		return noop, false
	}
	if !sub.Active(time.Now().Unix()) {
		abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgAccelSubscriptionExpired))
		return noop, false
	}
	release, ok := service.AcquireAccelSlot(userId, plan.Concurrency)
	if !ok {
		abortWithOpenAiMessage(c, http.StatusTooManyRequests, i18n.T(c, i18n.MsgAccelConcurrencyLimit))
		return noop, false
	}
	return release, true
}
