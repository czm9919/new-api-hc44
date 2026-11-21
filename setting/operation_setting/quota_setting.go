package operation_setting

import (
	"fmt"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type QuotaSetting struct {
	EnableFreeModelPreConsume bool    `json:"enable_free_model_pre_consume"` // 是否对免费模型启用预消耗
	TrustQuotaUSD             float64 `json:"trust_quota_usd"`               // 钱包免预扣门槛，0 表示禁用
	PreConsumeMultiplier      float64 `json:"pre_consume_multiplier"`        // 预计输入费用的预扣倍率，仅影响预留
	PerCallQuotaCap           int64   `json:"per_call_quota_cap"`            // 按次计费单次扣费上限（quota 单位），0 表示不限制（B95）
	MinWalletQuotaUSD         float64 `json:"min_wallet_quota_usd"`          // 最低余额门槛（USD），钱包可用额度低于该值拒绝新请求，默认 0（B96）
}

// 默认配置
var quotaSetting = QuotaSetting{
	EnableFreeModelPreConsume: true,
	TrustQuotaUSD:             10,
	PreConsumeMultiplier:      1,
	PerCallQuotaCap:           0,
	MinWalletQuotaUSD:         0,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("quota_setting", &quotaSetting)
}

func GetQuotaSetting() *QuotaSetting {
	return &quotaSetting
}

// ValidateQuotaOption validates reservation settings before they are persisted.
func ValidateQuotaOption(key, value string) error {
	switch key {
	case "quota_setting.trust_quota_usd", "quota_setting.pre_consume_multiplier", "quota_setting.min_wallet_quota_usd":
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("%s must be a finite number", key)
		}
		if key != "quota_setting.min_wallet_quota_usd" && number < 0 {
			return fmt.Errorf("%s must be a non-negative number", key)
		}
		if key == "quota_setting.pre_consume_multiplier" && number == 0 {
			return fmt.Errorf("%s must be greater than zero", key)
		}
		return nil
	case "quota_setting.per_call_quota_cap":
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil || number < 0 {
			return fmt.Errorf("%s must be a non-negative integer", key)
		}
		return nil
	}
	return nil
}

// InputPreConsumeMultiplier rejects invalid runtime settings as well as invalid saves.
func InputPreConsumeMultiplier() (float64, error) {
	multiplier := quotaSetting.PreConsumeMultiplier
	if multiplier <= 0 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		return 0, fmt.Errorf("pre-consume multiplier must be a finite number greater than zero")
	}
	return multiplier, nil
}

// PerCallQuotaCap 返回按次计费单次扣费上限（quota 单位）。0 表示不限制。
// 读取时对脏数据（负数）归零，避免把「0 上限」误判成「全部免费」。
func PerCallQuotaCap() int64 {
	if quotaSetting.PerCallQuotaCap < 0 {
		return 0
	}
	return quotaSetting.PerCallQuotaCap
}

// ClampPerCallQuota 对按次计费的扣费额度做上限夹紧。
// cap 为 0（未配置）或 quota 未超限时原样返回，保证默认行为与升级前一致。
func ClampPerCallQuota(quota int) int {
	cap := PerCallQuotaCap()
	if cap <= 0 || int64(quota) <= cap {
		return quota
	}
	return int(cap)
}

// MinWalletQuota 返回钱包最低余额门槛（quota 单位）。
// 配置项以 USD 表达（运维更直观），这里换算成内部 quota 单位；
// 允许负值，用于「欠费宽限」：门槛为 -2 表示余额跌到 -2 USD 才拒绝新请求。
func MinWalletQuota() float64 {
	usd := quotaSetting.MinWalletQuotaUSD
	if math.IsNaN(usd) || math.IsInf(usd, 0) {
		return 0
	}
	return usd * common.QuotaPerUnit
}
