package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/model"
)

// setupPoolKeyRecordDB 在号池测试库基础上补上贡献台账表。
func setupPoolKeyRecordDB(t *testing.T) {
	t.Helper()
	setupKeyPoolTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.PoolKeyRecord{}))
}

// 账号标识归一化：大小写、首尾空白、内部空白都不应造成「同一账号被当成两个」。
func TestNormalizePoolAccountKey(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"  ":                   "",
		"Zhang@Gmail.com":      "zhang@gmail.com",
		" zhang@gmail.com ":    "zhang@gmail.com",
		"zhang@ gmail.com":     "zhang@gmail.com",
		"ZHANG@GMAIL.COM   ":   "zhang@gmail.com",
		"Acme Team / zhangsan": "acmeteam/zhangsan",
	}
	for input, want := range cases {
		assert.Equal(t, want, model.NormalizePoolAccountKey(input), "input=%q", input)
	}
}

// 同一把密钥重复贡献：必须判为 duplicate_key（含贡献者信息，便于提示）。
func TestCheckPoolKeyConflictsDuplicateKey(t *testing.T) {
	setupPoolKeyRecordDB(t)

	const key = "nvapi-aaaa1111"
	require.NoError(t, model.UpsertPoolKeyRecord(&model.PoolKeyRecord{
		Provider:      string(ProviderNVIDIA),
		KeyHash:       model.PoolKeyHash(key),
		KeyMask:       maskKey(key),
		AccountKey:    "a@example.com",
		AccountLabel:  "a@example.com",
		ContributorId: 7,
		ChannelId:     3,
		Status:        1,
	}))

	conflicts, err := CheckPoolKeyConflicts(ProviderNVIDIA, []string{key}, "")
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	assert.Equal(t, PoolKeyConflictDuplicateKey, conflicts[0].Reason)
	assert.Equal(t, 7, conflicts[0].ContributorId)
	assert.Contains(t, FormatPoolKeyConflicts(conflicts).Error(), "已在号池中")
}

// 同一上游账号下的第二把密钥：必须判为 duplicate_account。
func TestCheckPoolKeyConflictsDuplicateAccount(t *testing.T) {
	setupPoolKeyRecordDB(t)

	const first = "nvapi-first0001"
	require.NoError(t, model.UpsertPoolKeyRecord(&model.PoolKeyRecord{
		Provider:      string(ProviderNVIDIA),
		KeyHash:       model.PoolKeyHash(first),
		KeyMask:       maskKey(first),
		AccountKey:    model.NormalizePoolAccountKey("Zhang@Gmail.com"),
		AccountLabel:  "Zhang@Gmail.com",
		ContributorId: 9,
		ChannelId:     4,
		Status:        1,
	}))

	// 同账号、不同密钥（大小写/空白写法不同也要命中）
	second := "nvapi-second002"
	conflicts, err := CheckPoolKeyConflicts(
		ProviderNVIDIA,
		[]string{second},
		model.NormalizePoolAccountKey(" zhang@gmail.com "),
	)
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	assert.Equal(t, PoolKeyConflictDuplicateAccount, conflicts[0].Reason)
	assert.Equal(t, 9, conflicts[0].ContributorId)
	assert.Contains(t, FormatPoolKeyConflicts(conflicts).Error(), "同一账号只能贡献一次")

	// 另一个账号、另一把密钥：放行
	conflicts, err = CheckPoolKeyConflicts(
		ProviderNVIDIA,
		[]string{"nvapi-third0003"},
		model.NormalizePoolAccountKey("other@gmail.com"),
	)
	require.NoError(t, err)
	assert.Empty(t, conflicts)
}

// 台账是后加的，历史贡献只有渠道里的 key：仍然要能拦住同一把 key。
func TestCheckPoolKeyConflictsFallsBackToChannelKeys(t *testing.T) {
	setupPoolKeyRecordDB(t)

	const legacyKey = "nvapi-legacy009"
	newPoolChannel(t, "英伟达号池", legacyKey, "m-a")

	conflicts, err := CheckPoolKeyConflicts(ProviderNVIDIA, []string{legacyKey}, "")
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	assert.Equal(t, PoolKeyConflictDuplicateKey, conflicts[0].Reason)
}

// 同一批提交里多把 key 属于同一账号：账号冲突只报一次，不刷屏。
func TestCheckPoolKeyConflictsReportsAccountOnce(t *testing.T) {
	setupPoolKeyRecordDB(t)

	require.NoError(t, model.UpsertPoolKeyRecord(&model.PoolKeyRecord{
		Provider:      string(ProviderNVIDIA),
		KeyHash:       model.PoolKeyHash("nvapi-used01"),
		KeyMask:       maskKey("nvapi-used01"),
		AccountKey:    "a@example.com",
		AccountLabel:  "a@example.com",
		ContributorId: 2,
		Status:        1,
	}))

	conflicts, err := CheckPoolKeyConflicts(
		ProviderNVIDIA,
		[]string{"nvapi-new01", "nvapi-new02", "nvapi-new03"},
		"a@example.com",
	)
	require.NoError(t, err)
	assert.Len(t, conflicts, 1, "同账号只需报一次")
	assert.Equal(t, PoolKeyConflictDuplicateAccount, conflicts[0].Reason)
}

// 台账写入：同一把 key 重复记录只保留一行，并刷新账号/渠道信息。
func TestRecordPoolContributionsUpserts(t *testing.T) {
	setupPoolKeyRecordDB(t)

	const key = "nvapi-upsert01"
	RecordPoolContributions(ProviderNVIDIA, 5, 11, "first@example.com", []string{key})
	RecordPoolContributions(ProviderNVIDIA, 6, 12, "second@example.com", []string{key})

	rec, err := model.GetPoolKeyRecordByHash(string(ProviderNVIDIA), model.PoolKeyHash(key))
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, 12, rec.ContributorId)
	assert.Equal(t, 6, rec.ChannelId)
	assert.Equal(t, "second@example.com", rec.AccountLabel)
	assert.Equal(t, 1, rec.Status)

	var count int64
	require.NoError(t, model.DB.Model(&model.PoolKeyRecord{}).
		Where("provider = ?", string(ProviderNVIDIA)).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
