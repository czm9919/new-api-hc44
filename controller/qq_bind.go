package controller

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// QQ 绑定（v1.1.0 M2）
//
// 面板侧只做三件事：生成一次性码、查状态、解绑。真正的「谁在发消息」
// 由配套 AstrBot 机器人确认后调 /api/qq/agent/verify 落库。

// GetQQBindStatus 查询当前账号的 QQ 绑定状态。
func GetQQBindStatus(c *gin.Context) {
	userId := c.GetInt("id")
	qq, err := model.GetUserQQ(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	boundAt := int64(0)
	if strings.TrimSpace(qq) != "" {
		var user model.User
		if err := model.DB.Select("qq_bound_at").Where("id = ?", userId).Take(&user).Error; err == nil {
			boundAt = user.QQBoundAt
		}
	}
	common.ApiSuccess(c, gin.H{
		"bound":         strings.TrimSpace(qq) != "",
		"qq":            service.MaskQQ(strings.TrimSpace(qq)),
		"bound_at":      boundAt,
		"agent_enabled": service.QQAgentEnabled(),
	})
}

// CreateQQBindCode 生成一次性绑定码（发给 AstrBot 机器人完成绑定）。
func CreateQQBindCode(c *gin.Context) {
	if !service.QQAgentEnabled() {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable,
			common.NewMessage("QQ bot is not configured yet, please contact the administrator"),
			gin.H{"code": "QQ_AGENT_UNAVAILABLE"})
		return
	}
	userId := c.GetInt("id")
	record, err := model.CreateQQBindCode(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"code":       record.Code,
		"expires_at": record.ExpiresAt,
		// TTL 同时给秒数，前端不必自己算时区。
		"ttl_seconds": int64(time.Until(time.Unix(record.ExpiresAt, 0)).Seconds()),
	})
}

// UnbindQQAccount 解除当前账号的 QQ 绑定。
func UnbindQQAccount(c *gin.Context) {
	userId := c.GetInt("id")
	if err := model.UnbindQQ(userId); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"bound": false})
}

// QQAgentVerifyRequest 机器人提交的绑定请求。
type QQAgentVerifyRequest struct {
	Code  string `json:"code"`
	QQ    string `json:"qq"`
	Token string `json:"token"`
}

// QQAgentVerify 由配套 AstrBot 机器人调用：QQ 号 + 一次性码 → 完成绑定。
//
// 鉴权走服务令牌（option qq_agent_token），与用户会话完全分离；令牌未配置
// 时整个接口关闭。支持 POST(JSON) 与 GET(查询串) 两种形式，方便不同插件写法。
func QQAgentVerify(c *gin.Context) {
	token := strings.TrimSpace(c.GetHeader("X-QQ-Agent-Token"))
	if token == "" {
		auth := strings.TrimSpace(c.GetHeader("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			token = strings.TrimSpace(auth[7:])
		}
	}

	var req QQAgentVerifyRequest
	if c.Request.Method == http.MethodGet {
		req.Code = c.Query("code")
		req.QQ = c.Query("qq")
		req.Token = c.Query("token")
	} else if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest,
			common.NewMessage("invalid request body"), gin.H{"code": "INVALID_REQUEST"})
		return
	}
	if token == "" {
		token = req.Token
	}

	if !service.QQAgentEnabled() {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable,
			common.NewMessage("QQ agent is not configured"), gin.H{"code": "QQ_AGENT_UNAVAILABLE"})
		return
	}
	if !service.CheckQQAgentToken(token) {
		common.ApiErrorStatus(c, http.StatusUnauthorized,
			common.NewMessage("invalid agent token"), gin.H{"code": "QQ_AGENT_UNAUTHORIZED"})
		return
	}

	result, err := model.BindQQByCode(req.Code, req.QQ)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrQQBindCodeInvalid):
			common.ApiErrorStatus(c, http.StatusBadRequest,
				common.NewMessage("bind code is invalid or expired"), gin.H{"code": "QQ_BIND_CODE_INVALID"})
		case errors.Is(err, model.ErrQQAlreadyBound):
			common.ApiErrorStatus(c, http.StatusConflict,
				common.NewMessage("this QQ is already bound to another account"), gin.H{"code": "QQ_ALREADY_BOUND"})
		case errors.Is(err, model.ErrQQBindAccountAlreadyBound):
			common.ApiErrorStatus(c, http.StatusConflict,
				common.NewMessage("this account is already bound to another QQ"), gin.H{"code": "QQ_ACCOUNT_ALREADY_BOUND"})
		default:
			common.ApiErrorStatus(c, http.StatusBadRequest,
				common.NewMessage(err.Error()), gin.H{"code": "QQ_BIND_FAILED"})
		}
		return
	}
	common.ApiSuccess(c, gin.H{
		"user_id":  result.UserId,
		"username": result.Username,
		"qq":       service.MaskQQ(result.QQ),
		"bound_at": result.BoundAt,
	})
}

// QQAgentPing 给机器人做连通性自检（不泄露任何账号信息）。
func QQAgentPing(c *gin.Context) {
	token := strings.TrimSpace(c.GetHeader("X-QQ-Agent-Token"))
	if token == "" {
		token = c.Query("token")
	}
	if !service.QQAgentEnabled() {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable,
			common.NewMessage("QQ agent is not configured"), gin.H{"code": "QQ_AGENT_UNAVAILABLE"})
		return
	}
	if !service.CheckQQAgentToken(token) {
		common.ApiErrorStatus(c, http.StatusUnauthorized,
			common.NewMessage("invalid agent token"), gin.H{"code": "QQ_AGENT_UNAUTHORIZED"})
		return
	}
	common.ApiSuccess(c, gin.H{
		"ok":              true,
		"require_qq_free": service.QQRequireForFreeEnabled(),
	})
}
