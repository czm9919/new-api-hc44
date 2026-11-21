package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// QQ 机器人接入配置（v1.1.0 M2 管理端）
//
// 与 service/qq_gate.go 的分工：
//   - qq_gate.go 负责「运行时判定」（令牌比对、免费模型门禁）
//   - 本文件负责「配置读写」（展示用视图、令牌轮换/清除、输入校验）
//
// 令牌只写不读：任何接口都不会回显明文，管理端只看到尾号提示。

const (
	// QQAgentTokenMinLength 令牌最短长度：太短的服务令牌可以被撞库。
	QQAgentTokenMinLength = 16
	// QQAgentTokenMaxLength 令牌最长长度：兼容运营商从别处粘贴的长密钥。
	QQAgentTokenMaxLength = 128
	// QQAgentTokenHintRunes 回显给管理端的尾号长度。
	QQAgentTokenHintRunes = 4
	// QQAgentTokenRandomBytes 轮换时生成的随机字节数（hex 后 64 字符）。
	QQAgentTokenRandomBytes = 32
)

// QQAgentConfig 管理端看到的机器人接入视图（不含明文令牌）。
type QQAgentConfig struct {
	// AgentConfigured 是否已配置令牌（未配置时绑定码接口一律 503）。
	AgentConfigured bool `json:"agent_configured"`
	// AgentTokenHint 令牌尾号提示，形如 "••••f0c1"；未配置为空。
	AgentTokenHint string `json:"agent_token_hint"`
	// RequireForFree 免费模型是否要求绑定 QQ。
	RequireForFree bool `json:"require_for_free"`
	// BoundUsers 已绑定 QQ 的用户数。
	BoundUsers int64 `json:"bound_users"`
	// ActiveBindCodes 未使用且未过期的绑定码数量。
	ActiveBindCodes int64 `json:"active_bind_codes"`
}

// ErrQQAgentTokenInvalid 令牌格式不合法。
var ErrQQAgentTokenInvalid = errors.New("invalid agent token")

// QQAgentTokenError 携带给用户看的原因。
type QQAgentTokenError struct {
	Reason string
}

func (e *QQAgentTokenError) Error() string { return e.Reason }

// Is 让 errors.Is(err, ErrQQAgentTokenInvalid) 成立。
func (e *QQAgentTokenError) Is(target error) bool { return target == ErrQQAgentTokenInvalid }

// MaskQQAgentToken 只暴露尾号：管理端用来核对「是不是同一枚令牌」。
func MaskQQAgentToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	runes := []rune(token)
	if len(runes) <= QQAgentTokenHintRunes {
		// 过短令牌连尾号都不给：固定四个点，避免泄露长度。
		return strings.Repeat("•", QQAgentTokenHintRunes)
	}
	return strings.Repeat("•", QQAgentTokenHintRunes) + string(runes[len(runes)-QQAgentTokenHintRunes:])
}

// ValidateQQAgentToken 校验管理员手填的令牌。
//
// 规则刻意保守：只允许可见 ASCII（不可含空格/换行），长度 16-128。
// 因为令牌既要放进 HTTP 头，又要能粘到 AstrBot 的配置文件里。
func ValidateQQAgentToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return &QQAgentTokenError{Reason: "令牌不能为空"}
	}
	if token != strings.TrimSpace(token) || strings.ContainsAny(token, " \t\r\n") {
		return &QQAgentTokenError{Reason: "令牌不能包含空格或换行"}
	}
	if len(token) < QQAgentTokenMinLength {
		return &QQAgentTokenError{
			Reason: fmt.Sprintf("令牌至少 %d 个字符（可用「生成随机令牌」一键生成）", QQAgentTokenMinLength),
		}
	}
	if len(token) > QQAgentTokenMaxLength {
		return &QQAgentTokenError{
			Reason: fmt.Sprintf("令牌不能超过 %d 个字符", QQAgentTokenMaxLength),
		}
	}
	for _, r := range token {
		if r < 0x21 || r > 0x7e {
			return &QQAgentTokenError{Reason: "令牌只能使用可见 ASCII 字符（字母、数字、符号）"}
		}
	}
	return nil
}

// GetQQAgentConfig 汇总机器人接入配置 + 运营概览指标。
func GetQQAgentConfig() QQAgentConfig {
	token := GetQQAgentToken()
	config := QQAgentConfig{
		AgentConfigured: token != "",
		AgentTokenHint:  MaskQQAgentToken(token),
		RequireForFree:  QQRequireForFreeEnabled(),
	}
	if bound, err := model.CountQQBoundUsers(); err == nil {
		config.BoundUsers = bound
	}
	if codes, err := model.CountActiveQQBindCodes(time.Now()); err == nil {
		config.ActiveBindCodes = codes
	}
	return config
}

func persistQQAgentOptions(values map[string]string) error {
	return model.UpdateOptionsBulk(values)
}

// SaveQQAgentSettings 保存机器人接入配置。
//
// 语义（与旧版「空串=保持原值」相比更明确，前端也按这套调用）：
//   - clearToken=true：删除令牌（绑定码接口立刻回到 503）
//   - token 非空：写入新令牌（覆盖旧的）
//   - token 为空且 clearToken=false：只改 require_for_free，不动令牌
func SaveQQAgentSettings(token string, clearToken bool, requireForFree bool) (QQAgentConfig, error) {
	token = strings.TrimSpace(token)
	if clearToken && token != "" {
		return QQAgentConfig{}, &QQAgentTokenError{
			Reason: "不能同时「填入新令牌」和「清除令牌」，请二选一",
		}
	}
	if token != "" {
		if err := ValidateQQAgentToken(token); err != nil {
			return QQAgentConfig{}, err
		}
	}
	values := map[string]string{
		QQRequireForFreeOptionKey: fmt.Sprintf("%t", requireForFree),
	}
	if clearToken {
		values[QQAgentTokenOptionKey] = ""
	} else if token != "" {
		values[QQAgentTokenOptionKey] = token
	}
	if err := persistQQAgentOptions(values); err != nil {
		return QQAgentConfig{}, err
	}
	return GetQQAgentConfig(), nil
}

// GenerateRandomQQAgentToken 生成一枚 64 位十六进制令牌。
func GenerateRandomQQAgentToken() (string, error) {
	buf := make([]byte, QQAgentTokenRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate agent token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// RotateQQAgentToken 生成并立即启用新令牌，返回明文（只在这一次响应里出现）。
//
// 为什么不复用 SaveQQAgentSettings：轮换的明文必须回到前端让管理员复制，
// 而普通保存路径永远不回显令牌，两条路径的返回体故意不一样。
func RotateQQAgentToken() (string, QQAgentConfig, error) {
	token, err := GenerateRandomQQAgentToken()
	if err != nil {
		return "", QQAgentConfig{}, err
	}
	if err := persistQQAgentOptions(map[string]string{QQAgentTokenOptionKey: token}); err != nil {
		return "", QQAgentConfig{}, err
	}
	return token, GetQQAgentConfig(), nil
}

// ClearQQAgentToken 撤销令牌（机器人接入立即关闭）。
func ClearQQAgentToken() (QQAgentConfig, error) {
	if err := persistQQAgentOptions(map[string]string{QQAgentTokenOptionKey: ""}); err != nil {
		return QQAgentConfig{}, err
	}
	return GetQQAgentConfig(), nil
}
