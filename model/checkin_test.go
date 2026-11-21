package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupCheckinTestDB 起一个只含 users / checkins 的内存 SQLite。
// 内存库必须把连接数压到 1，否则 gorm 每次从池里取新连接都会拿到另一个空库，
// 事务里写完外面读不到。
func setupCheckinTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&User{}, &Checkin{}))

	previous := DB
	DB = db
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { DB = previous })
}

// enableCheckinForTest 打开签到并固定奖励区间，结束后还原全局配置。
func enableCheckinForTest(t *testing.T, minQuota, maxQuota int) {
	t.Helper()
	setting := operation_setting.GetCheckinSetting()
	previous := *setting
	setting.Enabled = true
	setting.MinQuota = minQuota
	setting.MaxQuota = maxQuota
	t.Cleanup(func() { *setting = previous })
}

// 签到只送免费额度：quota 与 free_quota 同步 +奖励，
// 付费余额（quota - free_quota）在签到前后必须一模一样。
// 两条入库路径（SQLite 直写 / MySQL·PostgreSQL 事务）都要满足。
func TestUserCheckinCreditsFreeQuotaOnly(t *testing.T) {
	for _, dbType := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL} {
		t.Run(string(dbType), func(t *testing.T) {
			setupCheckinTestDB(t)
			enableCheckinForTest(t, 500, 500)
			previousType := common.MainDatabaseType()
			common.SetMainDatabaseType(dbType)
			t.Cleanup(func() { common.SetMainDatabaseType(previousType) })

			user := createReserveTestUser(t, 2000)

			record, err := UserCheckin(user.Id)
			require.NoError(t, err)
			assert.Equal(t, 500, record.QuotaAwarded)

			got := getRow(t, user.Id)
			assert.Equal(t, 2500, got.Quota, "总额度应 +奖励")
			assert.Equal(t, 500, got.FreeQuota, "免费额度应 +奖励")
			assert.Equal(t, 2000, got.Quota-got.FreeQuota, "付费余额不应因签到变化")

			var stored Checkin
			require.NoError(t, DB.Where("user_id = ?", user.Id).First(&stored).Error)
			assert.Equal(t, 500, stored.QuotaAwarded)
		})
	}
}

// 同一天第二次签到必须被拒，且不能再发一次奖励。
func TestUserCheckinRejectsDuplicateDay(t *testing.T) {
	setupCheckinTestDB(t)
	enableCheckinForTest(t, 300, 300)

	user := createReserveTestUser(t, 0)
	_, err := UserCheckin(user.Id)
	require.NoError(t, err)

	_, err = UserCheckin(user.Id)
	require.Error(t, err)

	got := getRow(t, user.Id)
	assert.Equal(t, 300, got.Quota, "重复签到不能二次入账")
	assert.Equal(t, 300, got.FreeQuota)
}

// 奖励区间为闭区间，且无奖励区间（min == max）时不随机。
func TestUserCheckinAwardStaysWithinRange(t *testing.T) {
	setupCheckinTestDB(t)
	enableCheckinForTest(t, 100, 150)

	for i := 0; i < 20; i++ {
		user := createReserveTestUser(t, 0)
		record, err := UserCheckin(user.Id)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, record.QuotaAwarded, 100)
		assert.LessOrEqual(t, record.QuotaAwarded, 150)
	}
}

// 会突破钱包上限时整笔签到必须失败：记录不能留下，额度也不能只加一半。
func TestUserCheckinRespectsWalletCeiling(t *testing.T) {
	setupCheckinTestDB(t)
	enableCheckinForTest(t, 1000, 1000)

	user := createReserveTestUser(t, common.MaxWalletQuota-100)
	_, err := UserCheckin(user.Id)
	require.Error(t, err)

	got := getRow(t, user.Id)
	assert.Equal(t, common.MaxWalletQuota-100, got.Quota, "失败时总额度不能被改动")
	assert.Equal(t, 0, got.FreeQuota, "失败时免费额度不能被改动")

	var count int64
	require.NoError(t, DB.Model(&Checkin{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count, "失败时签到记录必须回滚/删除")
}
