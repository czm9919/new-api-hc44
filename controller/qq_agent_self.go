package controller

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// QQ 机器人的自助查询（hc45）
//
// 之前用户想知道余额、用量、签到状态，只能打开面板。群里问一句「我还有多少」
// 是最高频的动作，把这三件事搬到 QQ，用户才会持续用。
//
// 这三个接口和 verify 一样，靠 X-QQ-Agent-Token 鉴权，不接受用户会话；
// 机器人出口 IP 固定，路由层已有 QQAgentRateLimit 兜底。
//
// 所有接口都只读或只影响调用者自己，不提供任何跨用户写操作。

// guardQQAgent 统一鉴权；返回 false 表示已经写好响应。
func guardQQAgent(c *gin.Context) bool {
	token := strings.TrimSpace(c.GetHeader("X-QQ-Agent-Token"))
	if token == "" {
		token = c.Query("token")
	}
	if !service.QQAgentEnabled() {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable,
			common.NewMessage("QQ agent is not configured"), gin.H{"code": "QQ_AGENT_UNAVAILABLE"})
		return false
	}
	if !service.CheckQQAgentToken(token) {
		common.ApiErrorStatus(c, http.StatusUnauthorized,
			common.NewMessage("invalid agent token"), gin.H{"code": "QQ_AGENT_UNAUTHORIZED"})
		return false
	}
	return true
}

// resolveQQUser 先归一化 QQ 再查账号；未绑定返回 (nil, nil)，非法 QQ 返回错误。
func resolveQQUser(rawQQ string) (*model.User, error) {
	qq, err := model.NormalizeQQ(rawQQ)
	if err != nil {
		return nil, err
	}
	user, err := model.GetUserByQQ(qq)
	if err != nil {
		return nil, nil
	}
	return user, nil
}

// QQAgentMe GET /api/qq/agent/me?qq=123456
// 返回余额、今日用量、今日额度上限、签到状态。未绑定时 bound=false。
func QQAgentMe(c *gin.Context) {
	if !guardQQAgent(c) {
		return
	}
	rawQQ := strings.TrimSpace(c.Query("qq"))
	if rawQQ == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("qq is required"), gin.H{"code": "QQ_AGENT_BAD_REQUEST"})
		return
	}
	user, err := resolveQQUser(rawQQ)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid qq"), gin.H{"code": "QQ_AGENT_BAD_REQUEST"})
		return
	}
	checkinSetting := operation_setting.GetCheckinSetting()
	if user == nil {
		common.ApiSuccess(c, gin.H{
			"bound":           false,
			"checkin_enabled": checkinSetting.Enabled,
		})
		return
	}
	if user.Status != common.UserStatusEnabled {
		common.ApiErrorStatus(c, http.StatusForbidden,
			common.NewMessage("account is disabled"), gin.H{"code": "QQ_AGENT_ACCOUNT_DISABLED"})
		return
	}

	// 直接读库：签到会立刻改余额，走缓存可能刚签完还是旧值。
	quota, err := model.GetUserQuota(user.Id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	usedToday, dailyLimit, _ := service.DailyQuotaStatus(user.Id, user.Group)
	checkedIn, _ := model.HasCheckedInToday(user.Id)
	usedTotal, _ := model.GetUserUsedQuota(user.Id)

	common.ApiSuccess(c, gin.H{
		"bound":            true,
		"username":         user.Username,
		"group":            user.Group,
		"quota":            quota,
		"quota_usd":        roundPricingFloat(quotaToUSD(float64(quota))),
		"used_total":       usedTotal,
		"used_today":       usedToday,
		"daily_limit":      dailyLimit,
		"checked_in_today": checkedIn,
		"checkin_enabled":  checkinSetting.Enabled,
		"checkin_min":      checkinSetting.MinQuota,
		"checkin_max":      checkinSetting.MaxQuota,
	})
}

// QQAgentCheckin POST /api/qq/agent/checkin {"qq":"123456"}
// 代用户签到。重复签到返回 already=true 而不是报错，机器人侧好写文案。
func QQAgentCheckin(c *gin.Context) {
	if !guardQQAgent(c) {
		return
	}
	var req struct {
		QQ string `json:"qq"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.QQ) == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("qq is required"), gin.H{"code": "QQ_AGENT_BAD_REQUEST"})
		return
	}
	user, err := resolveQQUser(req.QQ)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid qq"), gin.H{"code": "QQ_AGENT_BAD_REQUEST"})
		return
	}
	if user == nil {
		common.ApiSuccess(c, gin.H{"bound": false})
		return
	}
	if user.Status != common.UserStatusEnabled {
		common.ApiErrorStatus(c, http.StatusForbidden,
			common.NewMessage("account is disabled"), gin.H{"code": "QQ_AGENT_ACCOUNT_DISABLED"})
		return
	}
	if !operation_setting.GetCheckinSetting().Enabled {
		common.ApiErrorStatus(c, http.StatusForbidden,
			common.NewMessage("check-in feature is not enabled"), gin.H{"code": "QQ_AGENT_CHECKIN_DISABLED"})
		return
	}

	checkin, err := model.UserCheckin(user.Id)
	if err != nil {
		if checked, _ := model.HasCheckedInToday(user.Id); checked {
			common.ApiSuccess(c, gin.H{"bound": true, "already": true})
			return
		}
		common.ApiError(c, err)
		return
	}
	quota, _ := model.GetUserQuota(user.Id, true)
	common.ApiSuccess(c, gin.H{
		"bound":         true,
		"already":       false,
		"quota_awarded": checkin.QuotaAwarded,
		"checkin_date":  checkin.CheckinDate,
		"quota":         quota,
		"quota_usd":     roundPricingFloat(quotaToUSD(float64(quota))),
	})
}

// QQAgentRank GET /api/qq/agent/rank?limit=10
// 今日消耗排行，用户名打码。纯展示，不含任何账号标识。
func QQAgentRank(c *gin.Context) {
	if !guardQQAgent(c) {
		return
	}
	limit := 10
	if raw := c.Query("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 30 {
			limit = parsed
		}
	}
	rows, err := model.GetTopConsumersSince(model.StartOfTodayUnix(), limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for i, row := range rows {
		items = append(items, gin.H{
			"rank":     i + 1,
			"username": model.MaskUsername(row.Username),
			"quota":    int64(row.Quota),
			"requests": row.Requests,
		})
	}
	common.ApiSuccess(c, gin.H{
		"date":  time.Now().Format("2006-01-02"),
		"count": len(items),
		"items": items,
	})
}
