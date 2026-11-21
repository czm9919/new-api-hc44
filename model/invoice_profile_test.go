package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newInvoiceProfile(userId int, name string, isDefault bool) *InvoiceProfile {
	return &InvoiceProfile{
		UserId:    userId,
		Name:      name,
		Type:      "company",
		TaxId:     "91310000MA1K3XYZ8N",
		IsDefault: isDefault,
	}
}

func TestInsertInvoiceProfileValidates(t *testing.T) {
	truncateTables(t)

	noName := newInvoiceProfile(1, "  ", false)
	require.Error(t, InsertInvoiceProfile(noName))

	badType := newInvoiceProfile(1, "某某科技有限公司", false)
	badType.Type = "personal_corp"
	require.Error(t, InsertInvoiceProfile(badType))

	// 企业抬头缺税号必须拒绝
	noTaxId := newInvoiceProfile(1, "某某科技有限公司", false)
	noTaxId.TaxId = ""
	require.Error(t, InsertInvoiceProfile(noTaxId))

	// 个人抬头不要求税号
	personal := newInvoiceProfile(1, "张三", false)
	personal.Type = "personal"
	personal.TaxId = ""
	require.NoError(t, InsertInvoiceProfile(personal))
	assert.Positive(t, personal.Id)
	assert.Positive(t, personal.CreateTime)
}

func TestInsertInvoiceProfileDefaultIsExclusive(t *testing.T) {
	truncateTables(t)

	first := newInvoiceProfile(7, "第一家公司", true)
	require.NoError(t, InsertInvoiceProfile(first))
	second := newInvoiceProfile(7, "第二家公司", true)
	require.NoError(t, InsertInvoiceProfile(second))

	profiles, err := GetUserInvoiceProfiles(7)
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	// 默认抬头排最前，且只有一份默认
	assert.Equal(t, second.Id, profiles[0].Id)
	assert.True(t, profiles[0].IsDefault)
	assert.False(t, profiles[1].IsDefault)
}

func TestGetUserInvoiceProfilesIsScopedByUser(t *testing.T) {
	truncateTables(t)

	require.NoError(t, InsertInvoiceProfile(newInvoiceProfile(1, "用户一公司", false)))
	require.NoError(t, InsertInvoiceProfile(newInvoiceProfile(2, "用户二公司", false)))

	profiles, err := GetUserInvoiceProfiles(1)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, "用户一公司", profiles[0].Name)
}

func TestUpdateInvoiceProfileRejectsForeignKeyOwner(t *testing.T) {
	truncateTables(t)

	profile := newInvoiceProfile(3, "原名称", false)
	require.NoError(t, InsertInvoiceProfile(profile))

	// 用别的 userId 更新必须失败（所有权校验）
	stolen := *profile
	stolen.Name = "被改名"
	require.Error(t, UpdateInvoiceProfile(4, &stolen))

	// 本人更新成功，Id 不变
	own := *profile
	own.Name = "新名称"
	own.TaxId = "91310000MA1K3ABC1X"
	require.NoError(t, UpdateInvoiceProfile(3, &own))
	profiles, err := GetUserInvoiceProfiles(3)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, "新名称", profiles[0].Name)
	assert.Equal(t, "91310000MA1K3ABC1X", profiles[0].TaxId)
}

func TestUpdateInvoiceProfileMovesDefault(t *testing.T) {
	truncateTables(t)

	a := newInvoiceProfile(9, "A 公司", true)
	require.NoError(t, InsertInvoiceProfile(a))
	b := newInvoiceProfile(9, "B 公司", false)
	require.NoError(t, InsertInvoiceProfile(b))

	b.IsDefault = true
	require.NoError(t, UpdateInvoiceProfile(9, b))

	profiles, err := GetUserInvoiceProfiles(9)
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	assert.Equal(t, b.Id, profiles[0].Id)
	assert.False(t, profiles[1].IsDefault)
}

func TestDeleteInvoiceProfilePromotesOnlyWhenDefaultRemoved(t *testing.T) {
	truncateTables(t)

	a := newInvoiceProfile(11, "A 公司", true)
	require.NoError(t, InsertInvoiceProfile(a))
	b := newInvoiceProfile(11, "B 公司", false)
	require.NoError(t, InsertInvoiceProfile(b))

	// 删非默认抬头：不应动默认归属
	require.NoError(t, DeleteInvoiceProfile(11, b.Id))
	profiles, err := GetUserInvoiceProfiles(11)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, a.Id, profiles[0].Id)
	assert.True(t, profiles[0].IsDefault)

	// 删默认抬头后无剩余，不应报错
	require.NoError(t, DeleteInvoiceProfile(11, a.Id))
	profiles, err = GetUserInvoiceProfiles(11)
	require.NoError(t, err)
	assert.Empty(t, profiles)
}

func TestDeleteInvoiceProfilePromotesRemainingWhenDefaultGone(t *testing.T) {
	truncateTables(t)

	a := newInvoiceProfile(13, "A 公司", true)
	require.NoError(t, InsertInvoiceProfile(a))
	b := newInvoiceProfile(13, "B 公司", false)
	require.NoError(t, InsertInvoiceProfile(b))

	require.NoError(t, DeleteInvoiceProfile(13, a.Id))
	profiles, err := GetUserInvoiceProfiles(13)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, b.Id, profiles[0].Id)
	assert.True(t, profiles[0].IsDefault)
}

func TestDeleteInvoiceProfileRejectsForeignOwner(t *testing.T) {
	truncateTables(t)

	profile := newInvoiceProfile(15, "本人公司", true)
	require.NoError(t, InsertInvoiceProfile(profile))

	require.Error(t, DeleteInvoiceProfile(16, profile.Id))
	profiles, err := GetUserInvoiceProfiles(15)
	require.NoError(t, err)
	assert.Len(t, profiles, 1)
}
