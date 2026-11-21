package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 免费额度可用分组（FreeQuotaAllowedGroups）
// ---------------------------------------------------------------------------

func withFreeQuotaGroups(t *testing.T, raw string) {
	t.Helper()
	SetFreeQuotaAllowedGroups(raw)
	t.Cleanup(func() { SetFreeQuotaAllowedGroups("") })
}

// 白名单为空 = 不限制，任何分组都可用。
func TestIsFreeQuotaAllowedGroupUnrestricted(t *testing.T) {
	withFreeQuotaGroups(t, "")
	assert.True(t, IsFreeQuotaAllowedGroup("default"))
	assert.True(t, IsFreeQuotaAllowedGroup("gy"))
}

// 白名单命中（含空白修剪）才可用，未列出的分组不可用。
func TestIsFreeQuotaAllowedGroupWhitelist(t *testing.T) {
	withFreeQuotaGroups(t, " 号池 , 公益模型组 , gy ")
	assert.True(t, IsFreeQuotaAllowedGroup("号池"))
	assert.True(t, IsFreeQuotaAllowedGroup("公益模型组"))
	assert.True(t, IsFreeQuotaAllowedGroup("gy"))
	assert.False(t, IsFreeQuotaAllowedGroup("default"))
	assert.False(t, IsFreeQuotaAllowedGroup(""))
}

// ParseFreeQuotaAllowedGroups 去空白、丢空段。
func TestParseFreeQuotaAllowedGroups(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, ParseFreeQuotaAllowedGroups(" a, ,b ,"))
	assert.Empty(t, ParseFreeQuotaAllowedGroups(" , , "))
	assert.Empty(t, ParseFreeQuotaAllowedGroups(""))
}

// 付费-only 扣减：quota 下降，free_quota 原封不动。
func TestDecreaseUserQuotaPaidOnlyKeepsFree(t *testing.T) {
	setupFreeQuotaTestDB(t)
	u := createReserveTestUser(t, 500)
	setFree(t, u.Id, 200) // 200 免费 + 300 付费
	require.NoError(t, decreaseUserQuota(u.Id, 250, false))
	got := getRow(t, u.Id)
	assert.Equal(t, 250, got.Quota)
	assert.Equal(t, 200, got.FreeQuota)
}

// 付费-only 扣减不能吃掉免费部分：扣减额超过付费余额时返回失败且无副作用。
func TestReserveUserQuotaPaidOnlyInsufficient(t *testing.T) {
	setupFreeQuotaTestDB(t)
	u := createReserveTestUser(t, 500)
	setFree(t, u.Id, 450) // 付费余额只有 50
	ok, err := reserveUserQuotaPaidOnlyDB(u.Id, 100)
	require.NoError(t, err)
	assert.False(t, ok)
	got := getRow(t, u.Id)
	assert.Equal(t, 500, got.Quota)
	assert.Equal(t, 450, got.FreeQuota)
}

// 付费-only 预扣成功路径：只动 quota。
func TestTryReserveUserQuotaPaidOnly(t *testing.T) {
	setupFreeQuotaTestDB(t)
	u := createReserveTestUser(t, 500)
	setFree(t, u.Id, 200)

	ok, err := TryReserveUserQuotaUseFree(u.Id, 150, false)
	require.NoError(t, err)
	assert.True(t, ok)
	got := getRow(t, u.Id)
	assert.Equal(t, 350, got.Quota)
	assert.Equal(t, 200, got.FreeQuota)

	// 超出付费余额的部分不能预扣
	ok, err = TryReserveUserQuotaUseFree(u.Id, 200, false)
	require.NoError(t, err)
	assert.False(t, ok)
	got = getRow(t, u.Id)
	assert.Equal(t, 350, got.Quota)
	assert.Equal(t, 200, got.FreeQuota)
}

// useFree=true 的包装行为与旧接口一致（先吃免费）。
func TestTryReserveUserQuotaUseFreeTrue(t *testing.T) {
	setupFreeQuotaTestDB(t)
	u := createReserveTestUser(t, 500)
	setFree(t, u.Id, 500)
	ok, err := TryReserveUserQuotaUseFree(u.Id, 200, true)
	require.NoError(t, err)
	assert.True(t, ok)
	got := getRow(t, u.Id)
	assert.Equal(t, 300, got.Quota)
	assert.Equal(t, 300, got.FreeQuota)
}
