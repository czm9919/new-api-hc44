package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hc35 · B13：健康总览聚合的排序与字段回填。
//
// 排序约定：有样本在前按成功率升序（坏渠道置顶），同率按样本数降序；
// 无样本渠道垫底按 id 升序。

func TestCollectChannelHealthOverview(t *testing.T) {
	withLoadBalanceSettings(t, func(setting *operation_setting.LoadBalanceSetting) {
		setting.SmartFailCooldownSeconds = 45
		setting.SmartLatencyHalfLifeMinutes = 10
	})

	previousIDM := channelsIDM
	tag := "tier-a"
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Name: "good", Type: 1, Status: common.ChannelStatusEnabled, Group: "default"},
		2: {Id: 2, Name: "bad", Type: 1, Status: common.ChannelStatusEnabled, Group: "default"},
		3: {Id: 3, Name: "cold", Type: 1, Status: common.ChannelStatusAutoDisabled, Group: "vip", Tag: &tag},
	}
	t.Cleanup(func() { channelsIDM = previousIDM })

	base := time.Unix(1800000000, 0)
	t.Cleanup(func() {
		channelHealthStore.Delete(1)
		channelHealthStore.Delete(2)
		channelHealthStore.Delete(3)
	})

	// 渠道 1：全部成功；渠道 2：两次失败（触发冷却）；渠道 3：无样本。
	RecordChannelAttemptHealth(1, true, 500, 0, base)
	RecordChannelAttemptHealth(2, false, 0, 0, base)
	RecordChannelAttemptHealth(2, false, 0, 0, base)

	rows := CollectChannelHealthOverview(base)
	require.Len(t, rows, 3)

	// 坏渠道置顶：成功率 0，冷却中
	bad := rows[0]
	assert.Equal(t, 2, bad.ChannelId)
	assert.Equal(t, "bad", bad.Name)
	assert.True(t, bad.HasSamples)
	assert.InDelta(t, 0.0, bad.SuccessRate, 1e-9)
	assert.InDelta(t, 2.0, bad.Samples, 1e-9)
	assert.True(t, bad.CoolingDown)

	// 健康渠道第二：成功率 1，TTFT 回填
	good := rows[1]
	assert.Equal(t, 1, good.ChannelId)
	assert.True(t, good.HasSamples)
	assert.InDelta(t, 1.0, good.SuccessRate, 1e-9)
	assert.InDelta(t, 500.0, good.TtftMs, 1e-9)
	assert.False(t, good.CoolingDown)

	// 无样本渠道垫底
	cold := rows[2]
	assert.Equal(t, 3, cold.ChannelId)
	assert.False(t, cold.HasSamples)
	assert.Equal(t, "vip", cold.Group)
	assert.Equal(t, "tier-a", cold.Tag)
	assert.Equal(t, int64(0), cold.ActiveCount)
}

func TestCollectModelHealthOverview(t *testing.T) {
	base := time.Unix(1800000000, 0)
	t.Cleanup(func() {
		modelHealthStore.Delete("g1|mA")
		modelHealthStore.Delete("g1|mB")
	})

	// mA：3 成功 + TTFT；mB：1 失败（问题模型应靠前按样本排序）
	RecordModelAttemptHealth("g1", "mA", true, 800, base)
	RecordModelAttemptHealth("g1", "mA", true, 900, base.Add(time.Second))
	RecordModelAttemptHealth("g1", "mA", true, 0, base.Add(2*time.Second))
	RecordModelAttemptHealth("g1", "mB", false, 0, base)

	rows := CollectModelHealthOverview(base)
	require.Len(t, rows, 2)

	// 样本多者在前（1s / 2s 间隔在半衰期 10min 下各有 0.999 倍衰减，故取 0.01 容差）
	ma := rows[0]
	assert.Equal(t, "g1", ma.Group)
	assert.Equal(t, "mA", ma.Model)
	assert.InDelta(t, 3.0, ma.Samples, 0.01)
	assert.InDelta(t, 1.0, ma.SuccessRate, 1e-9)
	// EWMA：0.7*800 + 0.3*900 = 830
	assert.InDelta(t, 830.0, ma.TtftMs, 1e-9)

	mb := rows[1]
	assert.Equal(t, "mB", mb.Model)
	assert.InDelta(t, 1.0, mb.Samples, 1e-9)
	assert.InDelta(t, 0.0, mb.SuccessRate, 1e-9)
}
