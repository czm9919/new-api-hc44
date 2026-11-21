package billing_setting

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

// TaxSetting 控制价目/账单的含税与不含税展示（B100）。
// 只影响展示口径与账单标注，不参与实际扣费：扣费仍按倍率/按次价格原样计算。
type TaxSetting struct {
	Enabled     bool    `json:"enabled"`      // 是否启用税率展示
	RatePercent float64 `json:"rate_percent"` // 税率百分比，例如 6 表示 6%
	DisplayMode string  `json:"display_mode"` // include=价目已含税 / exclude=价目未含税（需另加）
	Label       string  `json:"label"`        // 税种名称，例如「增值税」「VAT」
	InvoiceNote string  `json:"invoice_note"` // 发票/收据备注
}

// 默认配置：不启用，展示口径与升级前完全一致。
var taxSetting = TaxSetting{
	Enabled:     false,
	RatePercent: 0,
	DisplayMode: "exclude",
	Label:       "VAT",
}

func init() {
	config.GlobalConfig.Register("tax_setting", &taxSetting)
}

func GetTaxSetting() *TaxSetting {
	return &taxSetting
}

const (
	TaxDisplayModeInclude = "include"
	TaxDisplayModeExclude = "exclude"
)

// Validate 归一化并校验运行时配置；保存前也调用同一份逻辑。
func (s *TaxSetting) Validate() error {
	if s.RatePercent < 0 || math.IsNaN(s.RatePercent) || math.IsInf(s.RatePercent, 0) || s.RatePercent > 100 {
		return fmt.Errorf("tax rate must be a percentage between 0 and 100")
	}
	mode := strings.ToLower(strings.TrimSpace(s.DisplayMode))
	if mode == "" {
		mode = TaxDisplayModeExclude
	}
	if mode != TaxDisplayModeInclude && mode != TaxDisplayModeExclude {
		return fmt.Errorf("tax display mode must be \"include\" or \"exclude\"")
	}
	s.DisplayMode = mode
	if s.Enabled && s.RatePercent <= 0 {
		return fmt.Errorf("tax rate must be greater than zero when tax display is enabled")
	}
	return nil
}

// Rate 返回税率的小数形式（6% → 0.06）。未启用时恒为 0。
func (s *TaxSetting) Rate() float64 {
	if !s.Enabled {
		return 0
	}
	if s.RatePercent < 0 || math.IsNaN(s.RatePercent) || math.IsInf(s.RatePercent, 0) || s.RatePercent > 100 {
		return 0
	}
	return s.RatePercent / 100
}

// GrossFromNet 不含税价 → 含税价。未启用时原样返回。
func (s *TaxSetting) GrossFromNet(net float64) float64 {
	rate := s.Rate()
	if rate <= 0 {
		return net
	}
	return net * (1 + rate)
}

// NetFromGross 含税价 → 不含税价。未启用时原样返回。
func (s *TaxSetting) NetFromGross(gross float64) float64 {
	rate := s.Rate()
	if rate <= 0 {
		return gross
	}
	return gross / (1 + rate)
}

// TaxPublicInfo 暴露给公开价目接口的只读税信息，不给运营侧内部字段。
type TaxPublicInfo struct {
	Enabled     bool    `json:"enabled"`
	RatePercent float64 `json:"rate_percent"`
	DisplayMode string  `json:"display_mode"`
	Label       string  `json:"label"`
	InvoiceNote string  `json:"invoice_note"`
}

func (s *TaxSetting) PublicInfo() TaxPublicInfo {
	return TaxPublicInfo{
		Enabled:     s.Enabled,
		RatePercent: s.RatePercent,
		DisplayMode: s.DisplayMode,
		Label:       s.Label,
		InvoiceNote: s.InvoiceNote,
	}
}

// ValidateTaxOption 在 option 保存入口校验 tax_setting.* 三个数值/枚举键。
func ValidateTaxOption(key, value string) error {
	switch key {
	case "tax_setting.rate_percent":
		// 直接解析：config.UpdateConfigFromMap 对无法解析的浮点值会静默跳过，
		// 若借它做校验，"abc" 这类脏值会一路存进数据库却看起来「校验通过」。
		rate, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 100 {
			return fmt.Errorf("tax_setting.rate_percent must be a percentage between 0 and 100")
		}
		return nil
	case "tax_setting.display_mode":
		mode := strings.ToLower(strings.TrimSpace(value))
		if mode != TaxDisplayModeInclude && mode != TaxDisplayModeExclude {
			return fmt.Errorf("tax_setting.display_mode must be \"include\" or \"exclude\"")
		}
		return nil
	}
	return nil
}
