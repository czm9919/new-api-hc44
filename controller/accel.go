package controller

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// 加速专线（hc48）
//
// 用户侧只做三件事：看套餐、开通、开关自动续费。
// 管理端负责套餐 CRUD 与订阅查看，护栏都在 service.ValidateAccelPlanGroup。

type accelSubscriptionView struct {
	Id              int     `json:"id"`
	PlanId          int     `json:"plan_id"`
	PlanName        string  `json:"plan_name"`
	GroupName       string  `json:"group_name"`
	StartAt         int64   `json:"start_at"`
	ExpireAt        int64   `json:"expire_at"`
	AutoRenew       bool    `json:"auto_renew"`
	Status          string  `json:"status"`
	Active          bool    `json:"active"`
	DaysLeft        int     `json:"days_left"`
	PricePerMonth   int64   `json:"price_per_month"`
	PriceUSD        float64 `json:"price_usd"`
	TotalCharged    int64   `json:"total_charged"`
	TotalChargedUSD float64 `json:"total_charged_usd"`
	LastChargeError string  `json:"last_charge_error"`
	UserId          int     `json:"user_id,omitempty"`
	Username        string  `json:"username,omitempty"`
}

type accelLimitsView struct {
	MaxPriceUSD    float64 `json:"max_price_usd"`
	PriceStepUSD   float64 `json:"price_step_usd"`
	MaxConcurrency int     `json:"max_concurrency"`
	MaxExtendDays  int     `json:"max_extend_days"`
}

// accelIngressView 入站加速接入信息。Host 为空表示管理员还没配加速域名，
// 前端应整块隐藏而不是显示一个空地址；Locked=true 表示已配置但该用户没有
// 生效订阅——域名是付费门槛（hc52 守卫会拦无订阅/前缀不匹配的请求），
// 所以不下发地址。BaseURL 带个人水印前缀（hc52），一人一份、泄露可溯源。
type accelIngressView struct {
	Host   string `json:"host"`
	Prefix string `json:"prefix"`
	// BaseURL 站点根地址（https://host/<prefix>）。有些客户端会让你自己拼 /v1。
	BaseURL string `json:"base_url"`
	// OpenAIBaseURL OpenAI 兼容口径（https://host/<prefix>/v1）。
	// 绝大多数 SDK 的 base_url 都期望带上 /v1，单独给一条避免用户
	// 把根地址直接填进去后请求落到 /chat/completions 上得到 404。
	OpenAIBaseURL string `json:"openai_base_url"`
	Example       string `json:"example"`
	Locked        bool   `json:"locked"`
}

type accelUserView struct {
	Plans          []model.AccelPlan      `json:"plans"`
	Subscription   *accelSubscriptionView `json:"subscription"`
	UserGroup      string                 `json:"user_group"`
	PaidBalance    int64                  `json:"paid_balance"`
	PaidBalanceUSD float64                `json:"paid_balance_usd"`
	// Usage 本期用量（当前订阅周期内），TodayUsage 今日用量。
	// 两者都按分组从消费日志聚合，没有订阅时为 nil。
	Usage      *model.AccelUsage `json:"usage"`
	TodayUsage *model.AccelUsage `json:"today_usage"`
	Limits     accelLimitsView   `json:"limits"`
	// Ingress 入站加速接入地址（hc50），未配置时 host 为空。
	Ingress accelIngressView `json:"ingress"`
	// SlotBackend 并发计数后端：redis（多实例共享）/ memory（进程内）。
	SlotBackend string `json:"slot_backend"`
	ServerTime  int64  `json:"server_time"`
}

// buildAccelIngress 把配置的域名拼成可直接粘贴的接入地址（带个人水印前缀）。
// 允许管理员填带协议头的写法（https://accel.example.com/），这里统一归一化，
// 否则前端拼出来的示例会出现 https://https:// 这种低级错误。
func buildAccelIngress(host string, prefix string) accelIngressView {
	cleaned := strings.TrimSpace(host)
	if cleaned == "" {
		return accelIngressView{}
	}
	cleaned = strings.TrimSuffix(cleaned, "/")
	// 协议以管理端填的为准：本地/IP 调试通常是 http，强制 https 只会
	// 让用户拿到一个连不上的地址。没写协议时按 https（生产默认）。
	scheme := "https://"
	if strings.HasPrefix(cleaned, "http://") {
		scheme = "http://"
	}
	cleaned = strings.TrimPrefix(cleaned, "https://")
	cleaned = strings.TrimPrefix(cleaned, "http://")
	if cleaned == "" {
		return accelIngressView{}
	}
	baseURL := scheme + cleaned
	if prefix != "" {
		// hc52：路径水印前缀。SDK 会把它当 base_url 的路径部分拼进请求，
		// 服务端 rewrite 剥掉后走正常 relay 链路。
		baseURL += "/" + prefix
	}
	openaiBase := baseURL + "/v1"
	return accelIngressView{
		Host:          cleaned,
		Prefix:        prefix,
		BaseURL:       baseURL,
		OpenAIBaseURL: openaiBase,
		Example: "curl " + openaiBase + "/chat/completions \\\n" +
			"  -H \"Authorization: Bearer sk-xxx\" \\\n" +
			"  -H \"Content-Type: application/json\" \\\n" +
			"  -d '{\"model\":\"gpt-4o-mini\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}'",
	}
}

// accelPlanName 取套餐名用于订阅视图；套餐已删除时返回空串，
// 前端会退化成显示分组名。
func accelPlanName(planId int) string {
	if planId <= 0 {
		return ""
	}
	plan, err := model.GetAccelPlanById(planId)
	if err != nil || plan == nil {
		return ""
	}
	return plan.Name
}

// accelTodayStart 返回本地时区今日 0 点的时间戳。
func accelTodayStart() int64 {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
}

func buildAccelSubscriptionView(sub *model.UserAccelSub, planNames map[int]string) *accelSubscriptionView {
	if sub == nil {
		return nil
	}
	now := time.Now().Unix()
	daysLeft := 0
	if sub.ExpireAt > now {
		daysLeft = int((sub.ExpireAt - now + 86399) / 86400) // 向上取整，剩 1 小时也算 1 天
	}
	return &accelSubscriptionView{
		Id:              sub.Id,
		PlanId:          sub.PlanId,
		PlanName:        planNames[sub.PlanId],
		GroupName:       sub.GroupName,
		StartAt:         sub.StartAt,
		ExpireAt:        sub.ExpireAt,
		AutoRenew:       sub.AutoRenew,
		Status:          sub.Status,
		Active:          sub.Active(now),
		DaysLeft:        daysLeft,
		PricePerMonth:   sub.PricePerMonth,
		PriceUSD:        sub.PriceUSD,
		TotalCharged:    sub.TotalCharged,
		TotalChargedUSD: sub.TotalChargedUSD,
		LastChargeError: sub.LastChargeError,
		UserId:          sub.UserId,
	}
}

// GetAccelOverview 用户侧：可选套餐 + 我的订阅 + 付费余额 + 当前分组。
func GetAccelOverview(c *gin.Context) {
	userId := c.GetInt("id")
	plans, err := model.ListAccelPlans(true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 先让订阅走一次惰性续费，再看库：否则页面会显示「已到期」，
	// 而用户下一次请求其实会自动续期成功，状态自相矛盾。
	if _, err := service.GetActiveSubscription(userId); err != nil {
		common.ApiError(c, err)
		return
	}
	sub, err := model.GetUserAccelSub(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	planNames := make(map[int]string, len(plans))
	for _, plan := range plans {
		planNames[plan.Id] = plan.Name
	}
	// 订阅绑定的套餐可能已下架，补一次查询让历史订阅也能显示套餐名。
	if sub != nil {
		if _, ok := planNames[sub.PlanId]; !ok {
			if plan, planErr := model.GetAccelPlanById(sub.PlanId); planErr == nil {
				planNames[plan.Id] = plan.Name
			}
		}
	}
	paidBalance := int64(user.Quota - user.FreeQuota)
	if paidBalance < 0 {
		paidBalance = 0
	}
	// 接入地址只发给有生效订阅的用户（hc51）：域名本身是付费门槛，
	// 守卫会拦无订阅请求，但页面也不该把地址白送出去让人试。
	// hc52：地址带个人水印前缀，惰性生成（存量订阅首次查看时补写）。
	ingress := accelIngressView{}
	if sub != nil && sub.Active(time.Now().Unix()) {
		prefix, prefixErr := service.EnsureAccelAccessPrefix(userId)
		if prefixErr != nil {
			common.SysError(common.LogText("accel: ensure access prefix failed for user %d: %s", userId, prefixErr.Error()))
		}
		ingress = buildAccelIngress(operation_setting.GetAccelSetting().IngressHost, prefix)
	} else if operation_setting.GetAccelSetting().IngressHost != "" {
		ingress = accelIngressView{Locked: true}
	}
	view := accelUserView{
		Plans:          plans,
		Subscription:   buildAccelSubscriptionView(sub, planNames),
		UserGroup:      user.Group,
		PaidBalance:    paidBalance,
		PaidBalanceUSD: common.RoundUSD(common.QuotaToUSD(paidBalance)),
		Limits: accelLimitsView{
			MaxPriceUSD:    service.AccelMaxPriceUSD(),
			PriceStepUSD:   service.AccelPriceStepUSD,
			MaxConcurrency: service.AccelMaxConc,
			MaxExtendDays:  service.AccelMaxExtendDays,
		},
		Ingress:     ingress,
		SlotBackend: service.AccelSlotBackend(),
		ServerTime:  time.Now().Unix(),
	}
	if sub != nil {
		since := sub.StartAt
		if since <= 0 {
			since = sub.ExpireAt - model.AccelPeriodSeconds
		}
		if usage, usageErr := model.GetAccelUsage(userId, sub.GroupName, since); usageErr == nil {
			view.Usage = &usage
		}
		if today, todayErr := model.GetAccelUsage(userId, sub.GroupName, accelTodayStart()); todayErr == nil {
			view.TodayUsage = &today
		}
	}
	common.ApiSuccess(c, view)
}

// SubscribeAccel 开通 / 换套餐（扣付费余额）。
// body: {plan_id, switch}；switch=true 表示「生效中换套餐」，会作废剩余时长，
// 由前端二次确认后传。
func SubscribeAccel(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		PlanId int  `json:"plan_id"`
		Switch bool `json:"switch"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid request body"), gin.H{"code": "INVALID_REQUEST"})
		return
	}
	sub, err := service.SubscribeAccelPlan(userId, req.PlanId, req.Switch)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAccelPlanNotFound):
			common.ApiErrorStatus(c, http.StatusNotFound,
				common.NewMessage("acceleration plan not found"), gin.H{"code": "ACCEL_PLAN_NOT_FOUND"})
		case errors.Is(err, service.ErrAccelPlanDisabled):
			common.ApiErrorStatus(c, http.StatusForbidden,
				common.NewMessage("acceleration plan is disabled"), gin.H{"code": "ACCEL_PLAN_DISABLED"})
		case errors.Is(err, service.ErrAccelGroupMissing):
			common.ApiErrorStatus(c, http.StatusBadRequest,
				common.NewMessage("acceleration group has no ratio configured"),
				gin.H{"code": "ACCEL_GROUP_MISSING"})
		case errors.Is(err, service.ErrAccelAlreadyActive):
			common.ApiErrorStatus(c, http.StatusConflict,
				common.NewMessage("acceleration plan is still active"), gin.H{"code": "ACCEL_ALREADY_ACTIVE"})
		case errors.Is(err, service.ErrAccelInsufficientFund):
			common.ApiErrorStatus(c, http.StatusPaymentRequired,
				common.NewMessage("insufficient paid balance"), gin.H{"code": "ACCEL_INSUFFICIENT_QUOTA"})
		default:
			common.ApiError(c, err)
		}
		return
	}
	model.RecordLog(userId, model.LogTypeSystem,
		common.NewMessage("Subscribed acceleration group {{group}}", map[string]any{
			"group": sub.GroupName,
		}))
	common.ApiSuccess(c, buildAccelSubscriptionView(sub, map[int]string{sub.PlanId: accelPlanName(sub.PlanId)}))
}

// UpdateAccelAutoRenew 开关自动续费；关闭后到期自然失效，不做按天退款。
func UpdateAccelAutoRenew(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		AutoRenew *bool `json:"auto_renew"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.AutoRenew == nil {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid request body"), gin.H{"code": "INVALID_REQUEST"})
		return
	}
	if err := service.SetAccelAutoRenew(userId, *req.AutoRenew); err != nil {
		if errors.Is(err, service.ErrAccelNoSubscription) {
			common.ApiErrorStatus(c, http.StatusNotFound,
				common.NewMessage("acceleration subscription not found"), gin.H{"code": "ACCEL_NO_SUBSCRIPTION"})
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"auto_renew": *req.AutoRenew})
}

func AdminListAccelPlans(c *gin.Context) {
	plans, err := model.ListAccelPlans(false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, plans)
}

type accelPlanRequest struct {
	Name      string `json:"name"`
	GroupName string `json:"group_name"`
	// PriceUSD 是唯一对外的报价入口：管理端只填美元，额度由服务端换算。
	// PricePerMonth 保留兼容（旧前端/脚本仍可能直接传额度）。
	PriceUSD      *float64 `json:"price_usd"`
	PricePerMonth int64    `json:"price_per_month"`
	Concurrency   int      `json:"concurrency"`
	Description   string   `json:"description"`
	Enabled       *bool    `json:"enabled"`
	SortOrder     int      `json:"sort_order"`
}

func (req *accelPlanRequest) validate() error {
	req.Name = strings.TrimSpace(req.Name)
	req.GroupName = strings.TrimSpace(req.GroupName)
	if req.Name == "" || len(req.Name) > 64 {
		return errors.New("name is required and must be within 64 characters")
	}
	if req.PriceUSD != nil {
		usd := *req.PriceUSD
		maxUSD := service.AccelMaxPriceUSD()
		if math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0 || usd > maxUSD {
			return fmt.Errorf("price must be between 0 and %.2f USD", maxUSD)
		}
		req.PricePerMonth = common.USDToQuota(usd)
	}
	if req.PricePerMonth < 0 || req.PricePerMonth > service.AccelMaxPrice {
		return fmt.Errorf("price must be between 0 and %.2f USD", service.AccelMaxPriceUSD())
	}
	if req.Concurrency < 0 || req.Concurrency > service.AccelMaxConc {
		return errors.New("concurrency out of range")
	}
	return service.ValidateAccelPlanGroup(req.GroupName)
}

func accelPlanErrorStatus(err error) (int, string, string) {
	switch {
	case errors.Is(err, service.ErrAccelGroupMissing):
		return http.StatusBadRequest, "ACCEL_GROUP_MISSING",
			"group has no ratio configured in group ratios; add it there first"
	case errors.Is(err, service.ErrAccelGroupPublic):
		return http.StatusBadRequest, "ACCEL_GROUP_PUBLIC",
			"group is already available to all users; remove it from user usable groups first"
	case errors.Is(err, model.ErrAccelPlanNotFound):
		return http.StatusNotFound, "ACCEL_PLAN_NOT_FOUND", "acceleration plan not found"
	default:
		return http.StatusBadRequest, "INVALID_REQUEST", err.Error()
	}
}

func AdminCreateAccelPlan(c *gin.Context) {
	var req accelPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid request body"), gin.H{"code": "INVALID_REQUEST"})
		return
	}
	if err := req.validate(); err != nil {
		status, code, message := accelPlanErrorStatus(err)
		common.ApiErrorStatus(c, status, common.NewMessage(message), gin.H{"code": code})
		return
	}
	plan := &model.AccelPlan{
		Name:          req.Name,
		GroupName:     req.GroupName,
		PricePerMonth: req.PricePerMonth,
		Concurrency:   req.Concurrency,
		Description:   req.Description,
		Enabled:       req.Enabled == nil || *req.Enabled,
		SortOrder:     req.SortOrder,
	}
	if err := model.CreateAccelPlan(plan); err != nil {
		common.ApiError(c, err)
		return
	}
	service.InvalidateAccelPlanCache()
	common.ApiSuccess(c, plan)
}

func AdminUpdateAccelPlan(c *gin.Context) {
	id, err := parseAccelIdParam(c, "id")
	if err != nil {
		return
	}
	plan, err := model.GetAccelPlanById(id)
	if err != nil {
		status, code, message := accelPlanErrorStatus(err)
		common.ApiErrorStatus(c, status, common.NewMessage(message), gin.H{"code": code})
		return
	}
	var req accelPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid request body"), gin.H{"code": "INVALID_REQUEST"})
		return
	}
	if err := req.validate(); err != nil {
		status, code, message := accelPlanErrorStatus(err)
		common.ApiErrorStatus(c, status, common.NewMessage(message), gin.H{"code": code})
		return
	}
	plan.Name = req.Name
	plan.GroupName = req.GroupName
	plan.PricePerMonth = req.PricePerMonth
	plan.Concurrency = req.Concurrency
	plan.Description = req.Description
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	plan.SortOrder = req.SortOrder
	if err := model.UpdateAccelPlan(plan); err != nil {
		common.ApiError(c, err)
		return
	}
	service.InvalidateAccelPlanCache()
	common.ApiSuccess(c, plan)
}

func AdminDeleteAccelPlan(c *gin.Context) {
	id, err := parseAccelIdParam(c, "id")
	if err != nil {
		return
	}
	if err := model.DeleteAccelPlan(id); err != nil {
		status, code, message := accelPlanErrorStatus(err)
		common.ApiErrorStatus(c, status, common.NewMessage(message), gin.H{"code": code})
		return
	}
	// 订阅里存了分组与价格快照，删套餐不会让存量用户立刻失效，
	// 只是不能再买；续费仍按快照价走。
	service.InvalidateAccelPlanCache()
	common.ApiSuccess(c, gin.H{"deleted": id})
}

func AdminListAccelSubs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	rows, total, err := model.ListAccelSubs(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(rows)
	common.ApiSuccess(c, pageInfo)
}

// AdminExtendAccelSub 售后补时长：不扣费，直接把到期时间往后推。
// 用于「用户反馈线路故障，补 3 天」这类场景，避免绕到数据库里手改。
func AdminExtendAccelSub(c *gin.Context) {
	var req struct {
		UserId int `json:"user_id"`
		Days   int `json:"days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.UserId <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid request body"), gin.H{"code": "INVALID_REQUEST"})
		return
	}
	if req.Days == 0 {
		req.Days = 30
	}
	sub, err := service.ExtendAccelSubscription(req.UserId, req.Days)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAccelNoSubscription):
			common.ApiErrorStatus(c, http.StatusNotFound,
				common.NewMessage("acceleration subscription not found"),
				gin.H{"code": "ACCEL_NO_SUBSCRIPTION"})
		case errors.Is(err, service.ErrAccelInvalidDays):
			common.ApiErrorStatus(c, http.StatusBadRequest,
				common.NewMessage("extend days out of range"), gin.H{"code": "INVALID_REQUEST"})
		default:
			common.ApiError(c, err)
		}
		return
	}
	common.ApiSuccess(c, buildAccelSubscriptionView(sub, map[int]string{sub.PlanId: accelPlanName(sub.PlanId)}))
}

// AdminRevokeAccelSub 强制停用（违规/退款/风控）。只改状态，不自动退款。
func AdminRevokeAccelSub(c *gin.Context) {
	var req struct {
		UserId int    `json:"user_id"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.UserId <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid request body"), gin.H{"code": "INVALID_REQUEST"})
		return
	}
	if err := service.RevokeAccelSubscription(req.UserId, req.Reason); err != nil {
		if errors.Is(err, service.ErrAccelNoSubscription) {
			common.ApiErrorStatus(c, http.StatusNotFound,
				common.NewMessage("acceleration subscription not found"),
				gin.H{"code": "ACCEL_NO_SUBSCRIPTION"})
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"user_id": req.UserId, "status": model.AccelSubStatusExpired})
}

// accelLinesView 线路状态视图。Visible=false 时前端整块不渲染
// （进程刚重启、健康记忆冷启动，展示一片 0% 只会让用户以为线路坏了）。
type accelLinesView struct {
	Group       string                 `json:"group"`
	Lines       []model.AccelLine      `json:"lines"`
	Summary     model.AccelLineSummary `json:"summary"`
	Visible     bool                   `json:"visible"`
	SlotBackend string                 `json:"slot_backend"`
	DegradedPct int                    `json:"degraded_percent"`
}

// buildAccelLinesView 聚合线路并按运营配置决定是否对用户可见。
func buildAccelLinesView(group string, visible bool) accelLinesView {
	setting := operation_setting.GetAccelSetting()
	lines := model.CollectAccelLines(group, time.Now(), setting.LineDegradedRate, 5)
	view := accelLinesView{
		Group:       group,
		Lines:       lines,
		Summary:     model.SummarizeAccelLines(lines),
		Visible:     visible && setting.LineDisplayEnabled,
		SlotBackend: service.AccelSlotBackend(),
		DegradedPct: setting.LineDegradedRate,
	}
	if view.Lines == nil {
		view.Lines = []model.AccelLine{}
	}
	return view
}

// GetAccelLines 线路状态。
//
// 普通用户：只返回自己订阅分组下的线路（脱敏，无出口地址/密钥），
// 没有生效订阅时返回空列表 + visible=false，前端提示「订阅后可见」。
// 管理员：可带 ?group=xxx 查任意分组（含已禁用渠道），用于排查
// 「用户说加速很慢」到底是哪条出口在拖。
func GetAccelLines(c *gin.Context) {
	userId := c.GetInt("id")
	groupParam := strings.TrimSpace(c.Query("group"))
	if groupParam != "" {
		if c.GetInt("role") < common.RoleAdminUser {
			common.ApiErrorStatus(c, http.StatusForbidden,
				common.NewMessage("only admin can query other groups"), gin.H{"code": "FORBIDDEN"})
			return
		}
		common.ApiSuccess(c, buildAccelLinesView(groupParam, true))
		return
	}
	sub, err := service.GetActiveSubscription(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if sub == nil || !sub.Active(time.Now().Unix()) || sub.GroupName == "" {
		// 没订阅、或套餐不绑分组（hc54：门牌模型，调度与主站一致，无专线渠道可言）：
		// 线路卡片没有内容可展示，返回空列表让前端隐藏。
		common.ApiSuccess(c, accelLinesView{
			Lines:       []model.AccelLine{},
			SlotBackend: service.AccelSlotBackend(),
		})
		return
	}
	common.ApiSuccess(c, buildAccelLinesView(sub.GroupName, true))
}

func parseAccelIdParam(c *gin.Context, name string) (int, error) {
	id, err := strconv.Atoi(c.Param(name))
	if err != nil || id <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid id"), gin.H{"code": "INVALID_REQUEST"})
		return 0, errors.New("invalid id")
	}
	return id, nil
}

// GetAccelIngressStats 加速域名守卫计数（hc52 · 管理端）。
// 拦截数突增 ≈ 域名或前缀被传播出去被人试了，此时考虑轮换域名。
func GetAccelIngressStats(c *gin.Context) {
	allowed, denied := middleware.AccelIngressStats()
	common.ApiSuccess(c, gin.H{
		"allowed":      allowed,
		"denied":       denied,
		"ingress_host": operation_setting.GetAccelSetting().IngressHost,
		"global_limit": operation_setting.GetAccelSetting().GlobalConcurrency,
		"slot_backend": service.AccelSlotBackend(),
		"server_time":  time.Now().Unix(),
	})
}
