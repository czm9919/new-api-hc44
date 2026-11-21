package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// 大转盘抽奖（v1.1.0 M2）

// GetLottery 用户侧：开关、奖池、今日剩余次数、QQ 绑定状态。
func GetLottery(c *gin.Context) {
	userId := c.GetInt("id")
	status, err := service.GetLotteryStatus(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, status)
}

// SpinLottery 抽一次奖。body 里 paid=true 表示付费抽奖（扣付费余额）。
func SpinLottery(c *gin.Context) {
	userId := c.GetInt("id")
	// 请求体是可选的：老客户端不带 body 时按免费抽奖处理，保持兼容。
	var req struct {
		Paid bool `json:"paid"`
	}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			common.ApiErrorStatus(c, http.StatusBadRequest,
				common.NewMessage("invalid request body"), gin.H{"code": "INVALID_REQUEST"})
			return
		}
	}
	result, err := service.SpinLottery(userId, req.Paid)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrLotteryDisabled):
			common.ApiErrorStatus(c, http.StatusForbidden,
				common.NewMessage("lottery is not enabled"), gin.H{"code": "LOTTERY_DISABLED"})
		case errors.Is(err, service.ErrLotteryPaidDisabled):
			common.ApiErrorStatus(c, http.StatusForbidden,
				common.NewMessage("paid lottery is not enabled"), gin.H{"code": "LOTTERY_PAID_DISABLED"})
		case errors.Is(err, service.ErrLotteryQQRequired):
			common.ApiErrorStatus(c, http.StatusForbidden,
				common.NewMessage("bind a QQ account before spinning"),
				gin.H{"code": "QQ_BINDING_REQUIRED"})
		case errors.Is(err, service.ErrLotteryNoSpinsLeft):
			common.ApiErrorStatus(c, http.StatusTooManyRequests,
				common.NewMessage("no spins left today"), gin.H{"code": "LOTTERY_NO_SPINS"})
		case errors.Is(err, service.ErrLotteryPaidNoSpinsLeft):
			common.ApiErrorStatus(c, http.StatusTooManyRequests,
				common.NewMessage("no paid spins left today"), gin.H{"code": "LOTTERY_PAID_NO_SPINS"})
		case errors.Is(err, service.ErrLotteryInsufficientQuota):
			common.ApiErrorStatus(c, http.StatusPaymentRequired,
				common.NewMessage("insufficient paid quota"),
				gin.H{"code": "LOTTERY_INSUFFICIENT_QUOTA"})
		default:
			common.ApiError(c, err)
		}
		return
	}

	if result.QuotaAwarded > 0 {
		model.RecordLog(userId, model.LogTypeSystem,
			common.NewMessage("Lottery prize: {{prize}}", map[string]any{"prize": result.Prize.Name}))
	}
	common.ApiSuccess(c, result)
}

// GetLotteryRecords 用户最近的抽奖记录。
func GetLotteryRecords(c *gin.Context) {
	userId := c.GetInt("id")
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	records, err := model.ListUserLotterySpins(userId, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": records})
}

// AdminGetLotterySettings 管理端读取抽奖配置。
func AdminGetLotterySettings(c *gin.Context) {
	common.ApiSuccess(c, service.GetLotterySettings())
}

// AdminUpdateLotterySettings 管理端保存抽奖配置。
//
// 校验失败返回 400 + LOTTERY_SETTINGS_INVALID + fields[]，前端按 field 定位到
// 具体行；成功时返回**回读后的**配置，前端不用自己猜规范化结果。
func AdminUpdateLotterySettings(c *gin.Context) {
	var settings service.LotterySettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid request body"), gin.H{"code": "INVALID_REQUEST"})
		return
	}
	saved, err := service.SaveLotterySettings(settings)
	if err != nil {
		var invalid *service.LotterySettingsError
		if errors.As(err, &invalid) {
			common.ApiErrorStatus(c, http.StatusBadRequest,
				common.NewMessage("lottery settings are invalid"),
				gin.H{"code": "LOTTERY_SETTINGS_INVALID", "fields": invalid.Fields})
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, err,
			gin.H{"code": "LOTTERY_SETTINGS_SAVE_FAILED"})
		return
	}
	common.ApiSuccess(c, saved)
}

// AdminResetLotterySettings 恢复默认奖池（管理端「恢复默认」按钮）。
func AdminResetLotterySettings(c *gin.Context) {
	saved, err := service.ResetLotterySettings()
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, err,
			gin.H{"code": "LOTTERY_SETTINGS_SAVE_FAILED"})
		return
	}
	common.ApiSuccess(c, saved)
}

// QQAgentSettingsRequest 机器人接入配置的写入体。
type QQAgentSettingsRequest struct {
	// AgentToken 新令牌；留空表示不修改。
	AgentToken string `json:"agent_token"`
	// ClearToken 显式撤销令牌（与 AgentToken 互斥）。
	ClearToken bool `json:"clear_token"`
	// RequireForFree 免费模型是否要求绑定 QQ。
	RequireForFree bool `json:"require_for_free"`
}

// AdminGetQQAgentSettings 管理端读取机器人接入配置 + 运营概览。
func AdminGetQQAgentSettings(c *gin.Context) {
	common.ApiSuccess(c, service.GetQQAgentConfig())
}

// AdminUpdateQQAgentSettings 管理端保存机器人接入配置（令牌只写不读）。
func AdminUpdateQQAgentSettings(c *gin.Context) {
	var req QQAgentSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid request body"), gin.H{"code": "INVALID_REQUEST"})
		return
	}
	config, err := service.SaveQQAgentSettings(req.AgentToken, req.ClearToken, req.RequireForFree)
	if err != nil {
		var invalid *service.QQAgentTokenError
		if errors.As(err, &invalid) {
			common.ApiErrorStatus(c, http.StatusBadRequest,
				common.NewMessage(invalid.Reason),
				gin.H{"code": "QQ_AGENT_TOKEN_INVALID",
					"fields": []gin.H{{"field": "agent_token", "message": invalid.Reason}}})
			return
		}
		common.ApiErrorStatus(c, http.StatusInternalServerError, err,
			gin.H{"code": "QQ_AGENT_SETTINGS_SAVE_FAILED"})
		return
	}
	common.ApiSuccess(c, config)
}

// AdminRotateQQAgentToken 一键生成并启用新令牌，明文只在这次响应里出现。
func AdminRotateQQAgentToken(c *gin.Context) {
	token, config, err := service.RotateQQAgentToken()
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, err,
			gin.H{"code": "QQ_AGENT_TOKEN_ROTATE_FAILED"})
		return
	}
	common.ApiSuccess(c, gin.H{
		"agent_token": token,
		"config":      config,
	})
}

// AdminClearQQAgentToken 撤销令牌（绑定码接口立刻回到 503）。
func AdminClearQQAgentToken(c *gin.Context) {
	config, err := service.ClearQQAgentToken()
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, err,
			gin.H{"code": "QQ_AGENT_SETTINGS_SAVE_FAILED"})
		return
	}
	common.ApiSuccess(c, config)
}
