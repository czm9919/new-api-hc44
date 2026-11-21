package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// hc31 · B89：免费额度按模型限定——relayFreeQuotaUsable 是预扣、结算、
// 资金流水标注三处共用的唯一判定入口，必须同时满足分组与模型白名单。
func TestRelayFreeQuotaUsableModelWhitelist(t *testing.T) {
	origGroups := model.GetFreeQuotaAllowedGroups()
	origModels := model.GetFreeQuotaAllowedModels()
	defer func() {
		model.SetFreeQuotaAllowedGroups(origGroups)
		model.SetFreeQuotaAllowedModels(origModels)
	}()

	model.SetFreeQuotaAllowedGroups("免费分组")
	model.SetFreeQuotaAllowedModels("gpt-3*,gemini-*")

	info := &relaycommon.RelayInfo{UsingGroup: "免费分组", OriginModelName: "gpt-3.5-turbo"}
	assert.True(t, relayFreeQuotaUsable(info), "分组+模型都在白名单内应允许免费额度")

	info.OriginModelName = "gpt-4o"
	assert.False(t, relayFreeQuotaUsable(info), "模型不在白名单内应只扣付费余额")

	// 分组不在白名单：模型命中也不允许
	info.UsingGroup = "default"
	info.OriginModelName = "gpt-3.5-turbo"
	assert.False(t, relayFreeQuotaUsable(info), "分组不在白名单内应只扣付费余额")

	// 计费身份（BillingModelName）优先于 OriginModelName，虚拟定价别名不被误判
	info.UsingGroup = "免费分组"
	info.OriginModelName = "gpt-4o"
	info.BillingModelName = "gemini-2.0-flash"
	assert.True(t, relayFreeQuotaUsable(info), "应以 BillingModelName 作为判定依据")

	// 模型白名单清空后只受分组约束
	model.SetFreeQuotaAllowedModels("")
	info.OriginModelName = "deepseek-chat"
	assert.True(t, relayFreeQuotaUsable(info))

	// nil relayInfo 不 panic（沿用既有兜底语义）
	assert.NotPanics(t, func() { _ = relayFreeQuotaUsable(nil) })
}
