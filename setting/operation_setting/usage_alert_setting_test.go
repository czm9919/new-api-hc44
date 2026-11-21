package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateUsageAnomalyTriggers(t *testing.T) {
	// 近 7 天里有消耗的天：100, 100, 200 → 均值 133.3
	baseline := []int64{100, 100, 200, 0, 0, 0, 0}
	decision := EvaluateUsageAnomaly(600, baseline, 3, 0)
	require.True(t, decision.Triggered)
	assert.Equal(t, 3, decision.BaselineSamples)
	assert.InDelta(t, 400.0/3.0, decision.BaselineAverage, 1e-9)
	assert.InDelta(t, 4.5, decision.Ratio, 1e-9)
}

func TestEvaluateUsageAnomalyGates(t *testing.T) {
	baseline := []int64{100, 100, 200}

	// 未超倍率
	assert.False(t, EvaluateUsageAnomaly(300, baseline, 3, 0).Triggered)
	// 正好等于倍率 → 触发（>= 语义）
	assert.True(t, EvaluateUsageAnomaly(400, baseline, 3, 0).Triggered)
	// 当日消耗为 0
	assert.False(t, EvaluateUsageAnomaly(0, baseline, 3, 0).Triggered)
	// multiplier <= 1 直接不判
	assert.False(t, EvaluateUsageAnomaly(600, baseline, 1, 0).Triggered)
	// 基线只有 1 个有效样本 → 统计意义不足，不判
	assert.False(t, EvaluateUsageAnomaly(600, []int64{100, 0, 0}, 3, 0).Triggered)
	// 基线全零
	assert.False(t, EvaluateUsageAnomaly(600, []int64{0, 0, 0}, 3, 0).Triggered)
}

func TestEvaluateUsageAnomalyMinDailyQuotaGate(t *testing.T) {
	baseline := []int64{1, 1} // 均值 1，今日 10 → 倍率 10 倍
	// 不设门槛：会误报
	assert.True(t, EvaluateUsageAnomaly(10, baseline, 3, 0).Triggered)
	// 设门槛 1000：当日消耗远低于门槛，静默
	assert.False(t, EvaluateUsageAnomaly(10, baseline, 3, 1000).Triggered)
}

func TestValidateUsageAlertOption(t *testing.T) {
	require.NoError(t, ValidateUsageAlertOption("usage_alert_setting.multiplier", "3"))
	require.NoError(t, ValidateUsageAlertOption("usage_alert_setting.multiplier", "1.5"))
	require.Error(t, ValidateUsageAlertOption("usage_alert_setting.multiplier", "1"))
	require.Error(t, ValidateUsageAlertOption("usage_alert_setting.multiplier", "0"))
	require.Error(t, ValidateUsageAlertOption("usage_alert_setting.multiplier", "abc"))
	require.Error(t, ValidateUsageAlertOption("usage_alert_setting.multiplier", "NaN"))

	require.NoError(t, ValidateUsageAlertOption("usage_alert_setting.baseline_days", "1"))
	require.NoError(t, ValidateUsageAlertOption("usage_alert_setting.baseline_days", "90"))
	require.Error(t, ValidateUsageAlertOption("usage_alert_setting.baseline_days", "0"))
	require.Error(t, ValidateUsageAlertOption("usage_alert_setting.baseline_days", "91"))

	require.NoError(t, ValidateUsageAlertOption("usage_alert_setting.min_daily_quota", "0"))
	require.NoError(t, ValidateUsageAlertOption("usage_alert_setting.min_daily_quota", "1000"))
	require.Error(t, ValidateUsageAlertOption("usage_alert_setting.min_daily_quota", "-1"))

	// 非本设置键一律放行
	require.NoError(t, ValidateUsageAlertOption("other.key", "whatever"))
}
