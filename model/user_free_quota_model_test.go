package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// 免费额度可用模型（FreeQuotaAllowedModels，hc31 · B89）
// ---------------------------------------------------------------------------

func withFreeQuotaModels(t *testing.T, raw string) {
	t.Helper()
	SetFreeQuotaAllowedModels(raw)
	t.Cleanup(func() { SetFreeQuotaAllowedModels("") })
}

// 白名单为空 = 不限制，任何模型都可用。
func TestIsFreeQuotaAllowedModelUnrestricted(t *testing.T) {
	withFreeQuotaModels(t, "")
	assert.True(t, IsFreeQuotaAllowedModel("gpt-4o"))
	assert.True(t, IsFreeQuotaAllowedModel("deepseek-chat"))
	assert.True(t, IsFreeQuotaAllowedModel(""))
}

// 精确匹配：白名单内的模型可用，其余不可用。
func TestIsFreeQuotaAllowedModelExact(t *testing.T) {
	withFreeQuotaModels(t, " gpt-4o-mini ,, gemini-2.0-flash , ")
	assert.True(t, IsFreeQuotaAllowedModel("gpt-4o-mini"))
	assert.True(t, IsFreeQuotaAllowedModel("gemini-2.0-flash"))
	assert.False(t, IsFreeQuotaAllowedModel("gpt-4o"))
	assert.False(t, IsFreeQuotaAllowedModel("GPT-4O-MINI")) // 大小写敏感，与分组白名单一致
	assert.False(t, IsFreeQuotaAllowedModel(""))
}

// prefix* 通配："gpt-3*" 命中所有 gpt-3 前缀模型，"*" 单独一项匹配全部。
func TestIsFreeQuotaAllowedModelWildcard(t *testing.T) {
	withFreeQuotaModels(t, "gpt-3*,gemini-*")
	assert.True(t, IsFreeQuotaAllowedModel("gpt-3.5-turbo"))
	assert.True(t, IsFreeQuotaAllowedModel("gpt-3"))
	assert.True(t, IsFreeQuotaAllowedModel("gemini-2.0-flash"))
	assert.False(t, IsFreeQuotaAllowedModel("gpt-4o"))
	assert.False(t, IsFreeQuotaAllowedModel("deepseek-chat"))

	withFreeQuotaModels(t, "*")
	assert.True(t, IsFreeQuotaAllowedModel("anything"))
}

// ParseFreeQuotaAllowedModels 去空白、丢空段、保留通配符。
func TestParseFreeQuotaAllowedModels(t *testing.T) {
	assert.Equal(t, []string{"gpt-3*", "gemini-2.0-flash"}, ParseFreeQuotaAllowedModels(" gpt-3* ,, gemini-2.0-flash ,"))
	assert.Empty(t, ParseFreeQuotaAllowedModels(" , , "))
	assert.Empty(t, ParseFreeQuotaAllowedModels(""))
}

// FreeQuotaUsable 分组与模型白名单必须同时放行；任一为空表示该维度不限制。
func TestFreeQuotaUsableCombinesBothWhitelists(t *testing.T) {
	withFreeQuotaGroups(t, "免费分组")
	withFreeQuotaModels(t, "gpt-3*")

	// 分组 + 模型都命中
	assert.True(t, FreeQuotaUsable("免费分组", "gpt-3.5-turbo"))
	// 模型未命中 → 不允许
	assert.False(t, FreeQuotaUsable("免费分组", "gpt-4o"))
	// 分组未命中 → 不允许（即使模型命中）
	assert.False(t, FreeQuotaUsable("default", "gpt-3.5-turbo"))

	// 模型白名单为空：只受分组约束
	withFreeQuotaModels(t, "")
	assert.True(t, FreeQuotaUsable("免费分组", "deepseek-chat"))

	// 分组白名单为空：恢复模型白名单后只受模型约束
	withFreeQuotaGroups(t, "")
	withFreeQuotaModels(t, "gpt-3*")
	assert.False(t, FreeQuotaUsable("default", "gpt-4o"))
	assert.True(t, FreeQuotaUsable("default", "gpt-3.5-turbo"))

	// 两个白名单都清空：全部允许
	withFreeQuotaGroups(t, "")
	withFreeQuotaModels(t, "")
	assert.True(t, FreeQuotaUsable("default", "gpt-4o"))
}
