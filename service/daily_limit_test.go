package service

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupDailyLimitTestDB 起一个只含 logs 的内存 SQLite。
// 连接数必须压到 1，否则 gorm 从池里取到新连接会看到另一个空库。
func setupDailyLimitTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Log{}))

	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous })
	return db
}

func enableDailyLimitForTest(t *testing.T, userQuota int, groupQuota map[string]int) {
	t.Helper()
	setting := operation_setting.GetDailyLimitSetting()
	previous := *setting
	previousGroups := setting.GroupQuota
	setting.Enabled = true
	setting.UserQuota = userQuota
	setting.GroupQuota = groupQuota
	t.Cleanup(func() {
		*setting = previous
		setting.GroupQuota = previousGroups
		dailyUsageCache.Range(func(key, _ any) bool {
			dailyUsageCache.Delete(key)
			return true
		})
	})
}

func TestDailyQuotaForGroupFallsBack(t *testing.T) {
	setting := &operation_setting.DailyLimitSetting{
		Enabled:    true,
		UserQuota:  1000,
		GroupQuota: map[string]int{"vip": 5000},
	}
	assert.Equal(t, 5000, setting.DailyQuotaForGroup("vip"), "分组配置应覆盖默认值")
	assert.Equal(t, 1000, setting.DailyQuotaForGroup("default"), "未配置的分组回退到默认值")
	assert.Equal(t, 1000, setting.DailyQuotaForGroup(""))

	setting.Enabled = false
	assert.Equal(t, 0, setting.DailyQuotaForGroup("vip"), "关闭时返回 0 表示不限")
}

func TestCheckDailyQuotaRejectsOverLimit(t *testing.T) {
	db := setupDailyLimitTestDB(t)
	enableDailyLimitForTest(t, 1000, nil)
	userId := 42

	since := model.StartOfTodayUnix()
	require.NoError(t, db.Create(&model.Log{
		UserId:    userId,
		Type:      model.LogTypeConsume,
		Quota:     600,
		CreatedAt: since + 10,
	}).Error)

	// 未达上限：放行
	require.NoError(t, CheckDailyQuota(userId, "default"))

	// 再记一笔到 1000，正好等于上限即拒绝
	require.NoError(t, db.Create(&model.Log{
		UserId:    userId,
		Type:      model.LogTypeConsume,
		Quota:     400,
		CreatedAt: since + 20,
	}).Error)
	InvalidateDailyQuotaCache(userId)

	err := CheckDailyQuota(userId, "default")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrDailyQuotaExceeded))

	// 别人不受影响
	require.NoError(t, CheckDailyQuota(43, "default"))
}

func TestDailyQuotaIgnoresNonConsumeLogs(t *testing.T) {
	db := setupDailyLimitTestDB(t)
	enableDailyLimitForTest(t, 1000, nil)
	userId := 7

	// 充值/管理/系统日志都不该计入消耗
	for _, logType := range []int{model.LogTypeTopup, model.LogTypeManage, model.LogTypeSystem} {
		require.NoError(t, db.Create(&model.Log{
			UserId:    userId,
			Type:      logType,
			Quota:     5000,
			CreatedAt: model.StartOfTodayUnix() + 5,
		}).Error)
	}
	require.NoError(t, CheckDailyQuota(userId, "default"))
}

func TestTodayConsumedQuotaOnlyCountsToday(t *testing.T) {
	db := setupDailyLimitTestDB(t)
	enableDailyLimitForTest(t, 1000, nil)
	userId := 9

	since := model.StartOfTodayUnix()
	require.NoError(t, db.Create(&model.Log{
		UserId: userId, Type: model.LogTypeConsume, Quota: 300, CreatedAt: since - 3600,
	}).Error) // 昨天的
	require.NoError(t, db.Create(&model.Log{
		UserId: userId, Type: model.LogTypeConsume, Quota: 200, CreatedAt: since + 60,
	}).Error) // 今天的

	InvalidateDailyQuotaCache(userId)
	used, err := TodayConsumedQuota(userId)
	require.NoError(t, err)
	assert.Equal(t, int64(200), used, "只统计当天")
}

func TestDailyUsageCacheHitsUntilInvalidated(t *testing.T) {
	db := setupDailyLimitTestDB(t)
	enableDailyLimitForTest(t, 1000, nil)
	userId := 11

	require.NoError(t, db.Create(&model.Log{
		UserId: userId, Type: model.LogTypeConsume, Quota: 100, CreatedAt: model.StartOfTodayUnix() + 1,
	}).Error)
	InvalidateDailyQuotaCache(userId)

	used, err := TodayConsumedQuota(userId)
	require.NoError(t, err)
	assert.Equal(t, int64(100), used)

	// 新增消费后，60 秒内仍读到缓存值
	require.NoError(t, db.Create(&model.Log{
		UserId: userId, Type: model.LogTypeConsume, Quota: 50, CreatedAt: model.StartOfTodayUnix() + 2,
	}).Error)
	cached, err := TodayConsumedQuota(userId)
	require.NoError(t, err)
	assert.Equal(t, int64(100), cached, "缓存窗口内应返回旧值")

	// 手动失效后立刻拿到新值
	InvalidateDailyQuotaCache(userId)
	fresh, err := TodayConsumedQuota(userId)
	require.NoError(t, err)
	assert.Equal(t, int64(150), fresh)
}

func TestDailyLimitDisabledAlwaysPasses(t *testing.T) {
	setupDailyLimitTestDB(t)
	setting := operation_setting.GetDailyLimitSetting()
	previous := *setting
	setting.Enabled = false
	t.Cleanup(func() { *setting = previous })

	require.NoError(t, CheckDailyQuota(1, "default"))

	// 即便开了但上限为 0，也不限制
	setting.Enabled = true
	setting.UserQuota = 0
	require.NoError(t, CheckDailyQuota(1, "default"))
}

func TestStartOfTodayUnixIsMidnight(t *testing.T) {
	start := time.Unix(model.StartOfTodayUnix(), 0)
	assert.Equal(t, 0, start.Hour())
	assert.Equal(t, 0, start.Minute())
	assert.Equal(t, 0, start.Second())
	assert.False(t, start.After(time.Now()), "不该是未来时间")
}
