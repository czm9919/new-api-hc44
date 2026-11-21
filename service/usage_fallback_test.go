package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withUsageFallback(t *testing.T, enabled bool) {
	t.Helper()
	old := common.UsageFallbackEnabled
	common.UsageFallbackEnabled = enabled
	t.Cleanup(func() { common.UsageFallbackEnabled = old })
}

func TestApplyUsageFallbackSkipsCasesItCannotImprove(t *testing.T) {
	withUsageFallback(t, true)

	t.Run("nil usage keeps the dedicated settlement path", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		info.SetEstimatePromptTokens(120)
		got, applied := applyUsageFallback(info, nil)
		assert.Nil(t, got)
		assert.False(t, applied)
	})

	t.Run("no estimate means nothing to fall back to", func(t *testing.T) {
		got, applied := applyUsageFallback(&relaycommon.RelayInfo{}, &dto.Usage{})
		assert.False(t, applied)
		require.NotNil(t, got)
		assert.Zero(t, got.PromptTokens)
	})

	t.Run("billable usage is never rewritten", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		info.SetEstimatePromptTokens(999)
		usage := &dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
		got, applied := applyUsageFallback(info, usage)
		assert.False(t, applied)
		assert.Equal(t, usage, got)
	})

	t.Run("cache only usage counts as billable", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		info.SetEstimatePromptTokens(999)
		usage := &dto.Usage{PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 8}}
		_, applied := applyUsageFallback(info, usage)
		assert.False(t, applied)
	})

	t.Run("disabled switch keeps the old behaviour", func(t *testing.T) {
		withUsageFallback(t, false)
		info := &relaycommon.RelayInfo{}
		info.SetEstimatePromptTokens(120)
		_, applied := applyUsageFallback(info, &dto.Usage{})
		assert.False(t, applied)
	})
}

func TestApplyUsageFallbackUsesPromptEstimateOnly(t *testing.T) {
	withUsageFallback(t, true)
	info := &relaycommon.RelayInfo{}
	info.SetEstimatePromptTokens(240)
	usage := &dto.Usage{BillingUsage: &dto.BillingUsage{Source: "oai_chat"}}

	got, applied := applyUsageFallback(info, usage)
	require.True(t, applied)
	require.NotNil(t, got)
	assert.Equal(t, 240, got.PromptTokens)
	assert.Equal(t, 0, got.CompletionTokens, "completion is never fabricated")
	assert.Equal(t, 240, got.TotalTokens)
	assert.Nil(t, got.BillingUsage, "the zero-valued snapshot must not survive the rewrite")
	// 原对象不被就地修改，调用方仍可用它做 usage_billing_path 判断。
	assert.Zero(t, usage.PromptTokens)
	assert.NotNil(t, usage.BillingUsage)
}

// 全零 usage 的端到端行为：按估算的 prompt tokens 计费，并在消费日志里
// 留下 admin 字段，事后可区分"上游没给计数"和"真实用量为 0"。
func TestPostTextConsumeQuotaBillsEstimatedPromptTokensOnZeroUsage(t *testing.T) {
	withUsageFallback(t, true)
	oldAlert := common.BillingAlertEnabled
	common.BillingAlertEnabled = false
	t.Cleanup(func() { common.BillingAlertEnabled = oldAlert })

	truncate(t)
	const userID, channelID = 64, 64
	seedUser(t, userID, 10_000)
	seedChannel(t, channelID)
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		OriginModelName: "zero-usage-model",
		UsingGroup:      "default",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
		PriceData: hosttypes.PriceData{
			ModelRatio: 1, CompletionRatio: 1,
			GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
	}
	info.SetEstimatePromptTokens(240)

	PostTextConsumeQuota(ctx, info, &dto.Usage{}, nil)

	log := getLastLog(t)
	require.NotNil(t, log)
	// prompt 240 tokens × 模型倍率 1 × 分组倍率 1。
	assert.Equal(t, 240, log.Quota)
	assert.Equal(t, 240, log.PromptTokens)
	assert.Equal(t, 0, log.CompletionTokens)
	assert.Contains(t, log.Content, "billed an estimated prompt token count")

	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok, "admin_info must be an object: %v", other)
	assert.Equal(t, true, adminInfo["usage_fallback"])
	assert.Equal(t, float64(240), adminInfo["usage_fallback_prompt_tokens"])
}
