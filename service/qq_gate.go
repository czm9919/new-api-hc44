package service

import (
	"crypto/subtle"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/model"
)

// QQ 门禁（v1.1.0 M2）
//
// 两处共用同一个「是否已绑定」判定：
//   - 抽奖：service/lottery.go 里直接查 IsQQBound
//   - 免费模型：relay 入口按「本次请求会不会吃免费额度」决定是否强制绑定
//
// 为什么要门禁免费模型：免费额度是站点成本，最容易被打包/多号薅。绑定 QQ
// 后每个额度获取点都能追到具体的人，配合 AstrBot 还能做拉群/公告触达。

const (
	// QQAgentTokenOptionKey AstrBot 机器人调用绑定接口的服务令牌。
	// 未配置时 /api/qq/agent/* 全部拒绝（默认关闭，避免裸奔）。
	QQAgentTokenOptionKey = "qq_agent_token"
	// QQRequireForFreeOptionKey 使用免费模型（免费额度白名单内模型）是否要求绑定 QQ。
	QQRequireForFreeOptionKey = "qq_require_for_free"
)

// GetQQAgentToken 读取机器人服务令牌。
func GetQQAgentToken() string {
	v, err := model.GetOptionValue(QQAgentTokenOptionKey)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// QQAgentEnabled 机器人接入是否已配置（未配置=关闭）。
func QQAgentEnabled() bool {
	return GetQQAgentToken() != ""
}

// CheckQQAgentToken 校验机器人服务令牌（常数时间比较，避免时序侧信道）。
func CheckQQAgentToken(provided string) bool {
	expected := GetQQAgentToken()
	if expected == "" {
		return false
	}
	got := strings.TrimSpace(provided)
	if len(got) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

// QQRequireForFreeEnabled 免费模型是否要求绑定 QQ（默认开启）。
func QQRequireForFreeEnabled() bool {
	v, err := model.GetOptionValue(QQRequireForFreeOptionKey)
	if err != nil || strings.TrimSpace(v) == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

// ErrQQBindRequiredForFree 免费模型使用前必须绑定 QQ。
var ErrQQBindRequiredForFree = errors.New("bind a QQ account before using free models")

// RequiresQQForFreeUsage 本次请求是否属于「免费模型消费」，需要 QQ 门禁。
//
// 判定口径（任一成立即算）：
//   - token 是号池发放的免费级 key（IsFreeTier）
//   - 模型显式出现在免费模型白名单（FreeQuotaAllowedModels）里
//
// 这里刻意不使用 model.FreeQuotaUsable：白名单为空时它返回 true（表示
// 免费额度对所有模型可用），若照它判定会把付费模型一起拦下来。
// 「免费模型」在本项目里的稳定含义是运营显式配置的白名单。
func RequiresQQForFreeUsage(token *model.Token, group, modelName string) bool {
	if token != nil && token.IsFreeTier() {
		return true
	}
	if modelName == "" {
		return false
	}
	if strings.TrimSpace(model.GetFreeQuotaAllowedModels()) == "" {
		return false
	}
	return model.IsFreeQuotaAllowedModel(modelName)
}

// EnsureQQBoundForFreeUsage 门禁判定；未开启或已绑定返回 nil。
func EnsureQQBoundForFreeUsage(userId int, token *model.Token, group, modelName string) error {
	if !QQRequireForFreeEnabled() {
		return nil
	}
	if !RequiresQQForFreeUsage(token, group, modelName) {
		return nil
	}
	if userId <= 0 {
		return ErrQQBindRequiredForFree
	}
	bound, err := model.IsQQBound(userId)
	if err != nil {
		return err
	}
	if !bound {
		return ErrQQBindRequiredForFree
	}
	return nil
}
