package operation_setting

import (
	"fmt"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/setting/config"
)

// UsageAlertSetting 用量异常提醒配置（B105）。
// 语义：当某用户「当日消耗额度」超过其近 BaselineDays 天日均消耗（只统计有消耗的天）
// 的 Multiplier 倍，且当日消耗达到 MinDailyQuota 时，向用户发送提醒。
type UsageAlertSetting struct {
	Enabled       bool    `json:"enabled"`         // 总开关，默认关闭
	Multiplier    float64 `json:"multiplier"`      // 触发倍率，默认 3
	BaselineDays  int     `json:"baseline_days"`   // 基线天数（不含今天），默认 7
	MinDailyQuota int64   `json:"min_daily_quota"` // 触发所需的当日最低消耗（quota 单位），默认 0
	NotifyAdmins  bool    `json:"notify_admins"`   // 是否同时通知开启渠道通知的管理员
}

var usageAlertSetting = UsageAlertSetting{
	Enabled:       false,
	Multiplier:    3,
	BaselineDays:  7,
	MinDailyQuota: 0,
	NotifyAdmins:  false,
}

func init() {
	config.GlobalConfig.Register("usage_alert_setting", &usageAlertSetting)
}

func GetUsageAlertSetting() *UsageAlertSetting {
	return &usageAlertSetting
}

// ValidateUsageAlertOption 校验 usage_alert_setting.* 选项，挂到 option 校验链。
func ValidateUsageAlertOption(key, value string) error {
	switch key {
	case "usage_alert_setting.multiplier":
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number <= 1 {
			return fmt.Errorf("%s must be a finite number greater than 1", key)
		}
		return nil
	case "usage_alert_setting.baseline_days":
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil || number < 1 || number > 90 {
			return fmt.Errorf("%s must be an integer between 1 and 90", key)
		}
		return nil
	case "usage_alert_setting.min_daily_quota":
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil || number < 0 {
			return fmt.Errorf("%s must be a non-negative integer", key)
		}
		return nil
	}
	return nil
}

// UsageAlertDecision 异常判定结果，由纯函数 EvaluateUsageAnomaly 产出，便于单测。
type UsageAlertDecision struct {
	Triggered       bool    // 是否触发提醒
	Ratio           float64 // 当日消耗 / 基线日均消耗
	BaselineAverage float64 // 基线日均消耗（quota 单位，只统计有消耗的天）
	BaselineSamples int     // 参与均值的「有消耗天数」
}

// EvaluateUsageAnomaly 纯决策函数（B105）：
//   - baselineDaily 为近 N 天里「每天的总消耗」（0 表示当天无消耗，不参与均值）；
//   - todayQuota 为当日累计消耗；
//   - minDailyQuota 为触发门槛，避免小额消耗被噪声放大误报；
//   - 至少需要 2 个「有消耗」的基线天才参与判定（单天基线毫无统计意义）。
func EvaluateUsageAnomaly(todayQuota int64, baselineDaily []int64, multiplier float64, minDailyQuota int64) UsageAlertDecision {
	decision := UsageAlertDecision{}
	if todayQuota <= 0 || multiplier <= 1 {
		return decision
	}
	var sum int64
	samples := 0
	for _, day := range baselineDaily {
		if day > 0 {
			sum += day
			samples++
		}
	}
	if samples < 2 {
		return decision
	}
	average := float64(sum) / float64(samples)
	decision.BaselineAverage = average
	decision.BaselineSamples = samples
	decision.Ratio = float64(todayQuota) / average
	if todayQuota >= minDailyQuota && decision.Ratio >= multiplier {
		decision.Triggered = true
	}
	return decision
}
