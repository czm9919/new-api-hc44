package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/glebarez/sqlite"
)

// setupFreeQuotaTestDB 用内存 SQLite 起一个干净的 users 表，关闭批量更新走直写路径。
func setupFreeQuotaTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	common.BatchUpdateEnabled = false
}

// setFree 直接给测试用户设定 free_quota（覆盖 createReserveTestUser 的默认值 0）。
func setFree(t *testing.T, id int, free int) {
	t.Helper()
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).
		Update("free_quota", free).Error)
}

func getRow(t *testing.T, id int) User {
	t.Helper()
	var u User
	require.NoError(t, DB.First(&u, id).Error)
	return u
}

// 签到入账：quota 与 free_quota 同时抬升，维持 free <= quota。
func TestIncreaseUserFreeQuota(t *testing.T) {
	setupFreeQuotaTestDB(t)
	u := createReserveTestUser(t, 0)
	require.NoError(t, IncreaseUserFreeQuota(u.Id, 1000))
	got := getRow(t, u.Id)
	assert.Equal(t, 1000, got.Quota)
	assert.Equal(t, 1000, got.FreeQuota)
}

// 免费额度足够时，扣减全部来自 free_quota。
func TestDecreaseUserQuotaFreeFirst(t *testing.T) {
	setupFreeQuotaTestDB(t)
	u := createReserveTestUser(t, 500)
	setFree(t, u.Id, 500) // 全部是免费额度
	require.NoError(t, decreaseUserQuota(u.Id, 300, true))
	got := getRow(t, u.Id)
	assert.Equal(t, 200, got.Quota)
	assert.Equal(t, 200, got.FreeQuota)
}

// 免费额度不足时，先吃免费、再吃付费，且不变量 free <= quota 保持。
func TestDecreaseUserQuotaDrainsFreeThenPaid(t *testing.T) {
	setupFreeQuotaTestDB(t)
	u := createReserveTestUser(t, 500)
	setFree(t, u.Id, 200) // 200 免费 + 300 付费
	require.NoError(t, decreaseUserQuota(u.Id, 350, true))
	got := getRow(t, u.Id)
	assert.Equal(t, 150, got.Quota)
	assert.Equal(t, 0, got.FreeQuota)
}

// 没有任何免费额度时，扣减全走付费余额。
func TestDecreaseUserQuotaNoFree(t *testing.T) {
	setupFreeQuotaTestDB(t)
	u := createReserveTestUser(t, 500)
	require.NoError(t, decreaseUserQuota(u.Id, 100, true))
	got := getRow(t, u.Id)
	assert.Equal(t, 400, got.Quota)
	assert.Equal(t, 0, got.FreeQuota)
}

// reserveUserQuotaDB（非 Redis 降级路径）也应优先扣免费。
func TestReserveUserQuotaDBFreeFirst(t *testing.T) {
	setupFreeQuotaTestDB(t)
	u := createReserveTestUser(t, 500)
	setFree(t, u.Id, 500)
	ok, err := reserveUserQuotaDB(u.Id, 200)
	require.NoError(t, err)
	assert.True(t, ok)
	got := getRow(t, u.Id)
	assert.Equal(t, 300, got.Quota)
	assert.Equal(t, 300, got.FreeQuota)
}

// persistUserQuotaDelta：消费(delta<0)优先扣免费，回补(delta>=0)记回免费。
func TestPersistUserQuotaDeltaFreeFirst(t *testing.T) {
	setupFreeQuotaTestDB(t)
	u := createReserveTestUser(t, 500)
	setFree(t, u.Id, 500)
	require.NoError(t, persistUserQuotaDelta(u.Id, -200))
	got := getRow(t, u.Id)
	assert.Equal(t, 300, got.Quota)
	assert.Equal(t, 300, got.FreeQuota)

	require.NoError(t, persistUserQuotaDelta(u.Id, 50)) // 退款记回免费
	got = getRow(t, u.Id)
	assert.Equal(t, 350, got.Quota)
	assert.Equal(t, 350, got.FreeQuota)
}
