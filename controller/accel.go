package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// 加速专线（hc48）
//
// 用户侧只做三件事：看套餐、开通、开关自动续费。
// 管理端负责套餐 CRUD 与订阅查看，护栏都在 service.ValidateAccelPlanGroup。

type accelSubscriptionView struct {
	Id              int    `json:"id"`
	PlanId          int    `json:"plan_id"`
	PlanName        string `json:"plan_name"`
	GroupName       string `json:"group_name"`
	StartAt         int64  `json:"start_at"`
	ExpireAt        int64  `json:"expire_at"`
	AutoRenew       bool   `json:"auto_renew"`
	Status          string `json:"status"`
	Active          bool   `json:"active"`
	DaysLeft        int    `json:"days_left"`
	PricePerMonth   int64  `json:"price_per_month"`
	TotalCharged    int64  `json:"total_charged"`
	LastChargeError string `json:"last_charge_error"`
}

type accelUserView struct {
	Plans        []model.AccelPlan      `json:"plans"`
	Subscription *accelSubscriptionView `json:"subscription"`
	UserGroup    string                 `json:"user_group"`
	PaidBalance  int64                  `json:"paid_balance"`
	ServerTime   int64                  `json:"server_time"`
}

func buildAccelSubscriptionView(sub *model.UserAccelSub, planNames map[int]string) *accelSubscriptionView {
	if sub == nil {
		return nil
	}
	now := time.Now().Unix()
	daysLeft := 0
	if sub.ExpireAt > now {
		daysLeft = int((sub.ExpireAt-now+86399)/86400) // 向上取整，剩 1 小时也算 1 天
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
		TotalCharged:    sub.TotalCharged,
		LastChargeError: sub.LastChargeError,
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
	common.ApiSuccess(c, accelUserView{
		Plans:        plans,
		Subscription: buildAccelSubscriptionView(sub, planNames),
		UserGroup:    user.Group,
		PaidBalance:  paidBalance,
		ServerTime:   time.Now().Unix(),
	})
}

// SubscribeAccel 开通或重新开通加速套餐（扣付费余额）。
func SubscribeAccel(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		PlanId int `json:"plan_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid request body"), gin.H{"code": "INVALID_REQUEST"})
		return
	}
	sub, err := service.SubscribeAccelPlan(userId, req.PlanId)
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
	common.ApiSuccess(c, buildAccelSubscriptionView(sub, map[int]string{sub.PlanId: ""}))
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
	Name          string `json:"name"`
	GroupName     string `json:"group_name"`
	PricePerMonth int64  `json:"price_per_month"`
	Concurrency   int    `json:"concurrency"`
	Description   string `json:"description"`
	Enabled       *bool  `json:"enabled"`
	SortOrder     int    `json:"sort_order"`
}

func (req *accelPlanRequest) validate() error {
	req.Name = strings.TrimSpace(req.Name)
	req.GroupName = strings.TrimSpace(req.GroupName)
	if req.Name == "" || len(req.Name) > 64 {
		return errors.New("name is required and must be within 64 characters")
	}
	if req.PricePerMonth < 0 || req.PricePerMonth > service.AccelMaxPrice {
		return errors.New("price_per_month out of range")
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

func parseAccelIdParam(c *gin.Context, name string) (int, error) {
	id, err := strconv.Atoi(c.Param(name))
	if err != nil || id <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid id"), gin.H{"code": "INVALID_REQUEST"})
		return 0, errors.New("invalid id")
	}
	return id, nil
}
