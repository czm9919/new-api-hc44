package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withQuotaSetting(t *testing.T, mutate func(*QuotaSetting)) {
	t.Helper()
	saved := quotaSetting
	t.Cleanup(func() { quotaSetting = saved })
	mutate(&quotaSetting)
}

func TestClampPerCallQuota(t *testing.T) {
	withQuotaSetting(t, func(s *QuotaSetting) { s.PerCallQuotaCap = 0 })
	assert.Equal(t, 12345, ClampPerCallQuota(12345), "cap=0 表示不限制")

	withQuotaSetting(t, func(s *QuotaSetting) { s.PerCallQuotaCap = 1000 })
	assert.Equal(t, 500, ClampPerCallQuota(500), "未超限原样返回")
	assert.Equal(t, 1000, ClampPerCallQuota(5000), "超限夹紧")
	assert.Equal(t, 1000, ClampPerCallQuota(1000), "等于上限原样返回")

	// 脏数据负值按 0 处理（不限制），不能误判成「全部免费」
	withQuotaSetting(t, func(s *QuotaSetting) { s.PerCallQuotaCap = -5 })
	assert.Equal(t, 12345, ClampPerCallQuota(12345))
}

func TestMinWalletQuota(t *testing.T) {
	withQuotaSetting(t, func(s *QuotaSetting) { s.MinWalletQuotaUSD = 0 })
	assert.Equal(t, 0.0, MinWalletQuota())

	// 2 USD → 2 × QuotaPerUnit
	withQuotaSetting(t, func(s *QuotaSetting) { s.MinWalletQuotaUSD = 2 })
	assert.Equal(t, 2*common.QuotaPerUnit, MinWalletQuota())

	// 负值表达欠费宽限
	withQuotaSetting(t, func(s *QuotaSetting) { s.MinWalletQuotaUSD = -1 })
	assert.Equal(t, -common.QuotaPerUnit, MinWalletQuota())
}

func TestValidateQuotaOption(t *testing.T) {
	require.NoError(t, ValidateQuotaOption("quota_setting.per_call_quota_cap", "0"))
	require.NoError(t, ValidateQuotaOption("quota_setting.per_call_quota_cap", "100000"))
	require.Error(t, ValidateQuotaOption("quota_setting.per_call_quota_cap", "-1"))
	require.Error(t, ValidateQuotaOption("quota_setting.per_call_quota_cap", "abc"))

	require.NoError(t, ValidateQuotaOption("quota_setting.min_wallet_quota_usd", "0"))
	require.NoError(t, ValidateQuotaOption("quota_setting.min_wallet_quota_usd", "-1.5"))
	require.Error(t, ValidateQuotaOption("quota_setting.min_wallet_quota_usd", "abc"))
	require.Error(t, ValidateQuotaOption("quota_setting.min_wallet_quota_usd", "Inf"))

	require.NoError(t, ValidateQuotaOption("quota_setting.trust_quota_usd", "10"))
	require.Error(t, ValidateQuotaOption("quota_setting.trust_quota_usd", "-1"))
	require.NoError(t, ValidateQuotaOption("quota_setting.pre_consume_multiplier", "1.5"))
	require.Error(t, ValidateQuotaOption("quota_setting.pre_consume_multiplier", "0"))
}
