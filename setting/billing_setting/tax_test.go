package billing_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaxSettingValidate(t *testing.T) {
	s := &TaxSetting{Enabled: true, RatePercent: 6, DisplayMode: "EXCLUDE  "}
	require.NoError(t, s.Validate())
	assert.Equal(t, TaxDisplayModeExclude, s.DisplayMode, "display_mode 归一化为小写去空白")

	s = &TaxSetting{Enabled: true, RatePercent: 0}
	require.Error(t, s.Validate(), "启用但税率为 0 必须拒绝")

	s = &TaxSetting{Enabled: false, RatePercent: 101}
	require.Error(t, s.Validate(), "税率超过 100 拒绝")

	s = &TaxSetting{Enabled: false, RatePercent: -1}
	require.Error(t, s.Validate(), "负税率拒绝")

	s = &TaxSetting{Enabled: false, RatePercent: 0, DisplayMode: ""}
	require.NoError(t, s.Validate())
	assert.Equal(t, TaxDisplayModeExclude, s.DisplayMode, "空 display_mode 默认 exclude")

	s = &TaxSetting{Enabled: false, RatePercent: 0, DisplayMode: "both"}
	require.Error(t, s.Validate())
}

func TestTaxRateAndConversion(t *testing.T) {
	// 未启用：恒等
	s := &TaxSetting{Enabled: false, RatePercent: 6}
	assert.Equal(t, 0.0, s.Rate())
	assert.Equal(t, 100.0, s.GrossFromNet(100.0))
	assert.Equal(t, 100.0, s.NetFromGross(100.0))

	// 启用 6%：net 100 → gross 106；gross 106 → net 100
	s = &TaxSetting{Enabled: true, RatePercent: 6}
	assert.InDelta(t, 0.06, s.Rate(), 1e-12)
	assert.InDelta(t, 106.0, s.GrossFromNet(100.0), 1e-9)
	assert.InDelta(t, 100.0, s.NetFromGross(106.0), 1e-9)
	// 往返一致
	assert.InDelta(t, 100.0, s.NetFromGross(s.GrossFromNet(100.0)), 1e-9)

	// 脏数据：NaN / Inf / 超界 → Rate 恒 0
	bad := &TaxSetting{Enabled: true, RatePercent: 999}
	assert.Equal(t, 0.0, bad.Rate())
}

func TestTaxPublicInfoOmitsNothingSensitive(t *testing.T) {
	s := &TaxSetting{Enabled: true, RatePercent: 6, DisplayMode: "include", Label: "增值税", InvoiceNote: "可开专票"}
	info := s.PublicInfo()
	assert.True(t, info.Enabled)
	assert.Equal(t, 6.0, info.RatePercent)
	assert.Equal(t, "include", info.DisplayMode)
	assert.Equal(t, "增值税", info.Label)
	assert.Equal(t, "可开专票", info.InvoiceNote)
}

func TestValidateTaxOption(t *testing.T) {
	require.NoError(t, ValidateTaxOption("tax_setting.rate_percent", "6"))
	require.NoError(t, ValidateTaxOption("tax_setting.rate_percent", "0"))
	require.Error(t, ValidateTaxOption("tax_setting.rate_percent", "101"))
	require.Error(t, ValidateTaxOption("tax_setting.rate_percent", "abc"))

	require.NoError(t, ValidateTaxOption("tax_setting.display_mode", "include"))
	require.NoError(t, ValidateTaxOption("tax_setting.display_mode", "EXCLUDE"))
	require.Error(t, ValidateTaxOption("tax_setting.display_mode", "both"))

	// 非本设置键放行
	require.NoError(t, ValidateTaxOption("other.key", "whatever"))
}
