package service

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 机器人接入配置（管理端）单测：令牌校验、只写不读、轮换、撤销、概览指标。

func TestMaskQQAgentToken(t *testing.T) {
	assert.Equal(t, "", MaskQQAgentToken("   "))
	// 短令牌全遮，不泄露长度以外的东西
	assert.Equal(t, "••••", MaskQQAgentToken("abc"))
	// 长令牌只留尾号
	assert.Equal(t, "••••c1d2", MaskQQAgentToken("2262009323c1d2"))
}

func TestValidateQQAgentToken(t *testing.T) {
	long := strings.Repeat("a", QQAgentTokenMaxLength+1)
	cases := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"空", "", true},
		{"太短", "0123456789abcde", true},
		{"刚好 16", "0123456789abcdef", false},
		{"带空格", "0123456789abc def", true},
		{"带换行", "0123456789abc\ndef", true},
		{"中文", "这是一个很长的令牌abcdefghij", true},
		{"正好上限", strings.Repeat("b", QQAgentTokenMaxLength), false},
		{"超上限", long, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateQQAgentToken(tc.token)
			if tc.wantErr {
				assert.ErrorIs(t, err, ErrQQAgentTokenInvalid)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestSaveQQAgentSettingsKeepsTokenWhenBlank(t *testing.T) {
	setupLotteryTestDB(t)
	token := strings.Repeat("k", 32)
	_, err := SaveQQAgentSettings(token, false, true)
	require.NoError(t, err)
	require.True(t, QQAgentEnabled())

	// 只改 require_for_free：令牌不能被清掉
	config, err := SaveQQAgentSettings("", false, false)
	require.NoError(t, err)
	assert.True(t, config.AgentConfigured)
	assert.False(t, config.RequireForFree)
	assert.True(t, QQAgentEnabled(), "空令牌表示不改，不是清空")
}

func TestSaveQQAgentSettingsRejectsTokenWithSpaces(t *testing.T) {
	setupLotteryTestDB(t)
	_, err := SaveQQAgentSettings("0123456789abc def", false, true)
	require.ErrorIs(t, err, ErrQQAgentTokenInvalid)
	assert.False(t, QQAgentEnabled())
}

func TestSaveQQAgentSettingsClearIsExplicit(t *testing.T) {
	setupLotteryTestDB(t)
	_, err := SaveQQAgentSettings(strings.Repeat("x", 24), false, true)
	require.NoError(t, err)

	config, err := SaveQQAgentSettings("", true, true)
	require.NoError(t, err)
	assert.False(t, config.AgentConfigured)
	assert.False(t, QQAgentEnabled())

	// 同时给新令牌和清除标记 → 拒绝，避免「以为改了其实删了」
	_, err = SaveQQAgentSettings(strings.Repeat("y", 24), true, true)
	require.ErrorIs(t, err, ErrQQAgentTokenInvalid)
}

func TestRotateQQAgentToken(t *testing.T) {
	setupLotteryTestDB(t)
	old := strings.Repeat("o", 24)
	_, err := SaveQQAgentSettings(old, false, true)
	require.NoError(t, err)

	token, config, err := RotateQQAgentToken()
	require.NoError(t, err)
	assert.Len(t, token, QQAgentTokenRandomBytes*2)
	assert.NotEqual(t, old, token, "轮换必须换掉旧令牌")
	assert.True(t, config.AgentConfigured)
	assert.Equal(t, MaskQQAgentToken(token), config.AgentTokenHint)
	assert.Equal(t, token, GetQQAgentToken(), "新令牌立即生效")
	assert.True(t, CheckQQAgentToken(token))
	assert.False(t, CheckQQAgentToken(old), "旧令牌立即失效")
}

func TestClearQQAgentToken(t *testing.T) {
	setupLotteryTestDB(t)
	_, err := SaveQQAgentSettings(strings.Repeat("z", 24), false, true)
	require.NoError(t, err)

	config, err := ClearQQAgentToken()
	require.NoError(t, err)
	assert.False(t, config.AgentConfigured)
	assert.Empty(t, config.AgentTokenHint)
	assert.False(t, QQAgentEnabled())
}

func TestGetQQAgentConfigReportsStats(t *testing.T) {
	setupLotteryTestDB(t)
	bound := createLotteryTestUser(t, "bound-user")
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", bound.Id).
		Updates(map[string]any{"qq": "100012345", "qq_bound_at": time.Now().Unix()}).Error)
	createLotteryTestUser(t, "unbound-user")

	code, err := model.CreateQQBindCode(bound.Id)
	require.NoError(t, err)
	require.NotEmpty(t, code.Code)

	config := GetQQAgentConfig()
	assert.Equal(t, int64(1), config.BoundUsers, "只统计已绑定的用户")
	assert.Equal(t, int64(1), config.ActiveBindCodes)
	assert.False(t, config.AgentConfigured)
	assert.True(t, config.RequireForFree, "默认开启免费模型门禁")

	// 绑定码用掉后不再计入
	_, err = model.BindQQByCode(code.Code, "100012345")
	require.NoError(t, err)
	config = GetQQAgentConfig()
	assert.Equal(t, int64(0), config.ActiveBindCodes)
	assert.Equal(t, int64(1), config.BoundUsers)

	_ = common.OptionMap
}
