package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v1.1.0 M2 · B93 密钥泄露风险检测。
func seedTokenRiskFixture(t *testing.T, username string) (*model.User, *model.Token) {
	t.Helper()
	// 直接 DB.Create 会跳过 Insert 钩子里的随机邀请码，这里显式给唯一值。
	user := &model.User{Username: username, Password: "123456", Role: common.RoleCommonUser, Group: "default", Quota: 1000000, AffCode: "aff" + username}
	require.NoError(t, model.DB.Create(user).Error)
	t.Cleanup(func() { model.DB.Unscoped().Delete(user) })
	token := &model.Token{
		UserId:      user.Id,
		Key:         "risk-" + username,
		Name:        username + "-token",
		CreatedTime: time.Now().Unix(),
		ExpiredTime: -1,
	}
	require.NoError(t, model.DB.Create(token).Error)
	return user, token
}

func seedTokenRiskLog(t *testing.T, userId, tokenId int, ip string) {
	t.Helper()
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId:    userId,
		Type:      model.LogTypeConsume,
		TokenId:   tokenId,
		ModelName: "risk-model",
		Ip:        ip,
		CreatedAt: time.Now().Unix() - 3600,
	}).Error)
}

func cleanupTokenRisk(t *testing.T, token *model.Token) {
	t.Helper()
	t.Cleanup(func() {
		model.DB.Unscoped().Delete(token)
		model.LOG_DB.Where("token_id = ?", token.Id).Delete(&model.Log{})
	})
}

// 同一密钥出现 ≥3 个不同 IP 触发 multi_ip；≥5 个直接判 high。
func TestTokenRiskMultiIP(t *testing.T) {
	user, token := seedTokenRiskFixture(t, "risk_multi_ip")
	cleanupTokenRisk(t, token)

	seedTokenRiskLog(t, user.Id, token.Id, "1.1.1.1")
	seedTokenRiskLog(t, user.Id, token.Id, "2.2.2.2")
	seedTokenRiskLog(t, user.Id, token.Id, "3.3.3.3")

	report, err := BuildTokenRiskReport(user.Id, 7)
	require.NoError(t, err)
	require.Len(t, report.Items, 1)
	item := report.Items[0]
	assert.Equal(t, token.Id, item.TokenId)
	assert.Equal(t, token.Name, item.TokenName)
	assert.Equal(t, TokenRiskLevelMedium, item.RiskLevel)
	assert.Equal(t, []string{TokenRiskReasonMultiIP}, item.Reasons)
	assert.Equal(t, 3, item.DistinctIPs)
	assert.Equal(t, int64(3), item.RequestCount)
	assert.NotEmpty(t, item.Recommendation)
	assert.Equal(t, 1, report.TotalTokens)
	assert.Equal(t, 1, report.RiskyTokens)

	// 追加到 5 个 IP → 升级为 high。
	seedTokenRiskLog(t, user.Id, token.Id, "4.4.4.4")
	seedTokenRiskLog(t, user.Id, token.Id, "5.5.5.5")
	report, err = BuildTokenRiskReport(user.Id, 7)
	require.NoError(t, err)
	require.Len(t, report.Items, 1)
	assert.Equal(t, TokenRiskLevelHigh, report.Items[0].RiskLevel)
	assert.Equal(t, 5, report.Items[0].DistinctIPs)

	// 单 IP 使用不触发风险。
	other, otherToken := seedTokenRiskFixture(t, "risk_single_ip")
	cleanupTokenRisk(t, otherToken)
	seedTokenRiskLog(t, other.Id, otherToken.Id, "9.9.9.9")
	report, err = BuildTokenRiskReport(other.Id, 7)
	require.NoError(t, err)
	assert.Empty(t, report.Items)
	assert.Equal(t, 0, report.RiskyTokens)
	assert.Equal(t, 1, report.TotalTokens)
}

// AllowIps 白名单与实际调用 IP 冲突 → high + ip_outside_allowlist。
func TestTokenRiskAllowlistConflict(t *testing.T) {
	user, token := seedTokenRiskFixture(t, "risk_allowlist")
	cleanupTokenRisk(t, token)
	allow := "1.1.1.1\n 2.2.2.2, "
	token.AllowIps = &allow
	require.NoError(t, model.DB.Save(token).Error)

	seedTokenRiskLog(t, user.Id, token.Id, "1.1.1.1") // 白名单内
	seedTokenRiskLog(t, user.Id, token.Id, "9.9.9.9") // 白名单外

	report, err := BuildTokenRiskReport(user.Id, 7)
	require.NoError(t, err)
	require.Len(t, report.Items, 1)
	item := report.Items[0]
	assert.Equal(t, TokenRiskLevelHigh, item.RiskLevel)
	assert.Equal(t, []string{TokenRiskReasonAllowIP}, item.Reasons)
	// 仅 2 个 IP，不触发 multi_ip。
	assert.Equal(t, 2, item.DistinctIPs)
	assert.Equal(t, []string{"1.1.1.1", "2.2.2.2"}, item.AllowIps)
	require.Len(t, item.IPs, 2)
	for _, ip := range item.IPs {
		switch ip.Ip {
		case "1.1.1.1":
			assert.True(t, ip.Allowed)
		case "9.9.9.9":
			assert.False(t, ip.Allowed)
		default:
			t.Fatalf("unexpected ip %s", ip.Ip)
		}
	}

	// 全部 IP 都在白名单内 → 无风险（换一把干净的 token，避免复用上面已越界的用量）。
	_, cleanToken := seedTokenRiskFixture(t, "risk_allowlist_clean")
	cleanupTokenRisk(t, cleanToken)
	cleanAllow := "1.1.1.1"
	cleanToken.AllowIps = &cleanAllow
	require.NoError(t, model.DB.Save(cleanToken).Error)
	seedTokenRiskLog(t, user.Id, cleanToken.Id, "1.1.1.1")
	seedTokenRiskLog(t, user.Id, cleanToken.Id, "1.1.1.1")

	report, err = BuildTokenRiskReport(cleanToken.UserId, 7)
	require.NoError(t, err)
	assert.Empty(t, report.Items)
}

// 用户侧报告只统计本人 token 的用量，不把其他用户日志算进窗口。
func TestTokenRiskFiltersOtherUsersUsage(t *testing.T) {
	_, otherToken := seedTokenRiskFixture(t, "risk_other_owner")
	cleanupTokenRisk(t, otherToken)
	user, token := seedTokenRiskFixture(t, "risk_me")
	cleanupTokenRisk(t, token)
	seedTokenRiskLog(t, user.Id, token.Id, "1.1.1.1")

	// 其他用户的 token 在窗口内有 3 个 IP 的消耗（会被他人误判为 multi_ip）。
	for _, ip := range []string{"2.2.2.2", "3.3.3.3", "4.4.4.4"} {
		seedTokenRiskLog(t, 999999, otherToken.Id, ip)
	}

	report, err := BuildTokenRiskReport(user.Id, 7)
	require.NoError(t, err)
	assert.Equal(t, 1, report.TotalTokens)
	assert.Empty(t, report.Items)
	for _, item := range report.Items {
		assert.NotEqual(t, otherToken.Id, item.TokenId)
	}
}

// 管理端概览：先按全量日志识别风险，再补齐 token 元数据与用户名。
func TestTokenRiskOverviewMetadata(t *testing.T) {
	user, token := seedTokenRiskFixture(t, "risk_overview_user")
	cleanupTokenRisk(t, token)
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4", "5.5.5.5"} {
		seedTokenRiskLog(t, user.Id, token.Id, ip)
	}

	report, err := BuildTokenRiskOverview(7, 100)
	require.NoError(t, err)
	require.NotEmpty(t, report.Items)
	var found *TokenRiskItem
	for i := range report.Items {
		if report.Items[i].TokenId == token.Id {
			found = &report.Items[i]
		}
	}
	require.NotNil(t, found, "risk token should appear in overview")
	assert.Equal(t, token.Name, found.TokenName)
	assert.Equal(t, user.Id, found.UserId)
	assert.Equal(t, user.Username, found.Username)
	assert.Equal(t, TokenRiskLevelHigh, found.RiskLevel)

	// 无风险的密钥不出现在概览里。
	for _, item := range report.Items {
		assert.NotEmpty(t, item.TokenName)
		assert.NotEmpty(t, item.Username)
	}
}

// NormalizeTokenRiskDays 校正窗口。
func TestNormalizeTokenRiskDays(t *testing.T) {
	assert.Equal(t, 30, NormalizeTokenRiskDays(0))
	assert.Equal(t, 30, NormalizeTokenRiskDays(-5))
	assert.Equal(t, 15, NormalizeTokenRiskDays(15))
	assert.Equal(t, 90, NormalizeTokenRiskDays(365))
}
