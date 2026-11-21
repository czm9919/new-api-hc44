package model

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// setupConsumptionRankDB 注入独立的内存库（logs + users），并返回当前时间
// 作为窗口锚点。测试结束后恢复全局 DB / LOG_DB。
func setupConsumptionRankDB(t *testing.T) int64 {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{},
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}, &User{}))
	previousDB, previousLogDB := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLogDB })
	return time.Now().Unix()
}

func insertConsumeLog(t *testing.T, userId int, username string, quota int, createdAt int64) {
	t.Helper()
	require.NoError(t, LOG_DB.Create(&Log{
		UserId: userId, Username: username, Quota: quota,
		Type: LogTypeConsume, CreatedAt: createdAt, ModelName: "gpt-test",
	}).Error)
}

func TestGetUserConsumptionRankOrdersByQuotaAndAggregatesPerUser(t *testing.T) {
	now := setupConsumptionRankDB(t)
	dayAgo := now - 86400

	// 用户 1：两笔日志、两个历史用户名（改名后按 user_id 聚合仍是一行）
	insertConsumeLog(t, 1, "alice_old", 1000, dayAgo)
	insertConsumeLog(t, 1, "alice_new", 500, dayAgo+3600)
	// 用户 2：单笔更高额度
	insertConsumeLog(t, 2, "bob", 3000, dayAgo)
	// 用户 3：只有错误日志，不计入消耗
	require.NoError(t, LOG_DB.Create(&Log{
		UserId: 3, Username: "carol", Quota: 9000,
		Type: LogTypeError, CreatedAt: dayAgo,
	}).Error)

	entries, err := GetUserConsumptionRank(now-2*86400, 0, 10)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	assert.Equal(t, 2, entries[0].UserId)
	assert.Equal(t, "bob", entries[0].Username)
	assert.EqualValues(t, 3000, entries[0].UsedQuota)
	assert.EqualValues(t, 1, entries[0].RequestCount)

	assert.Equal(t, 1, entries[1].UserId)
	// 同一 user_id 的多用户名聚合成一行；日志用户名是历史快照（MAX 为
	// 字典序兜底口径），当前用户名以 users 表为准，由 controller 覆盖。
	assert.NotEmpty(t, entries[1].Username)
	assert.Contains(t, []string{"alice_old", "alice_new"}, entries[1].Username)
	assert.EqualValues(t, 1500, entries[1].UsedQuota)
	assert.EqualValues(t, 2, entries[1].RequestCount)
	assert.Equal(t, dayAgo+3600, entries[1].LastActive)
}

func TestGetUserConsumptionRankFiltersByWindowAndDropsZeroQuota(t *testing.T) {
	now := setupConsumptionRankDB(t)
	oldTs := now - 10*86400

	insertConsumeLog(t, 1, "alice", 100, oldTs) // 窗口外
	insertConsumeLog(t, 2, "bob", 200, now-3600)
	insertConsumeLog(t, 3, "carol", 0, now-3600) // 0 消耗，不上榜

	start := now - 7*86400
	entries, err := GetUserConsumptionRank(start, 0, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 2, entries[0].UserId)
	assert.EqualValues(t, 200, entries[0].UsedQuota)

	// 全时段（start=0）下 carol 仍因 0 消耗被剔除
	allEntries, err := GetUserConsumptionRank(0, 0, 10)
	require.NoError(t, err)
	require.Len(t, allEntries, 2)
}

func TestGetUserConsumptionRankLimitClamp(t *testing.T) {
	setupConsumptionRankDB(t)
	now := time.Now().Unix()
	for i := 1; i <= 5; i++ {
		insertConsumeLog(t, i, "u", 100*i, now-3600)
	}
	// limit<=0 兜底 20；负 limit 不报错
	entries, err := GetUserConsumptionRank(now-2*86400, 0, -1)
	require.NoError(t, err)
	assert.Len(t, entries, 5)

	entries, err = GetUserConsumptionRank(now-2*86400, 0, 3)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
	assert.EqualValues(t, 500, entries[0].UsedQuota)
}

func TestGetUserConsumptionTotalsCountsDistinctUsers(t *testing.T) {
	setupConsumptionRankDB(t)
	now := time.Now().Unix()
	dayAgo := now - 86400
	insertConsumeLog(t, 1, "alice", 1000, dayAgo)
	insertConsumeLog(t, 1, "alice", 500, dayAgo+60)
	insertConsumeLog(t, 2, "bob", 3000, dayAgo)
	// 窗口外与错误日志不应计入
	insertConsumeLog(t, 2, "bob", 9999, now-30*86400)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId: 2, Username: "bob", Quota: 500,
		Type: LogTypeError, CreatedAt: dayAgo,
	}).Error)

	totals, err := GetUserConsumptionTotals(now-2*86400, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 4500, totals.UsedQuota)
	assert.EqualValues(t, 3, totals.RequestCount)
	assert.EqualValues(t, 2, totals.UserCount)
}

func TestGetUserConsumptionProfilesIncludesDeletedUsersGracefully(t *testing.T) {
	setupConsumptionRankDB(t)
	require.NoError(t, DB.Create(&User{Username: "alice", DisplayName: "Alice", Quota: 42, UsedQuota: 1500, Status: 1}).Error)

	profiles, err := GetUserConsumptionProfiles([]int{1, 99})
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, "alice", profiles[0].Username)
	assert.Equal(t, "Alice", profiles[0].DisplayName)
	assert.Equal(t, 42, profiles[0].Quota)
	assert.Equal(t, 1, profiles[0].Status)
}

func TestGetUserConsumptionRankNilLogDB(t *testing.T) {
	previous := LOG_DB
	LOG_DB = nil
	t.Cleanup(func() { LOG_DB = previous })
	entries, err := GetUserConsumptionRank(0, 0, 10)
	assert.NoError(t, err)
	assert.Empty(t, entries)
}

func TestGetUserConsumptionSelfRankAndGap(t *testing.T) {
	now := setupConsumptionRankDB(t)
	dayAgo := now - 86400

	// 用户 1：1500（两笔），用户 2：3000，用户 3：800
	insertConsumeLog(t, 1, "alice", 1000, dayAgo)
	insertConsumeLog(t, 1, "alice", 500, dayAgo+60)
	insertConsumeLog(t, 2, "bob", 3000, dayAgo)
	insertConsumeLog(t, 3, "carol", 800, dayAgo)

	start := now - 2*86400

	self, err := GetUserConsumptionSelf(1, start, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, self.UserId)
	assert.EqualValues(t, 1500, self.UsedQuota)
	assert.EqualValues(t, 2, self.RequestCount)
	// bob(3000) 在我上面 → 第 2 名，追赶目标是 3000
	assert.Equal(t, 2, self.Rank)
	assert.EqualValues(t, 3000, self.AheadQuota)
	assert.EqualValues(t, 3, self.TotalUsers)

	// 榜首没有追赶目标
	top, err := GetUserConsumptionSelf(2, start, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, top.Rank)
	assert.EqualValues(t, 0, top.AheadQuota)

	// 末位：两名在我上面
	last, err := GetUserConsumptionSelf(3, start, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, last.Rank)
	assert.EqualValues(t, 1500, last.AheadQuota)
}

func TestGetUserConsumptionSelfUnrankedWithoutConsumption(t *testing.T) {
	now := setupConsumptionRankDB(t)
	dayAgo := now - 86400
	insertConsumeLog(t, 2, "bob", 3000, dayAgo)

	// 用户 1 在窗口内没有任何消费日志
	self, err := GetUserConsumptionSelf(1, now-2*86400, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, self.Rank)
	assert.EqualValues(t, 0, self.UsedQuota)
	assert.EqualValues(t, 1, self.TotalUsers)

	// 窗口外有消费也不算：名次仍然是 0
	insertConsumeLog(t, 1, "alice", 9999, now-10*86400)
	self, err = GetUserConsumptionSelf(1, now-2*86400, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, self.Rank)
	assert.EqualValues(t, 0, self.UsedQuota)

	// 非法 userId 直接返回零值，不报错
	invalid, err := GetUserConsumptionSelf(0, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, invalid.Rank)
}

func TestGetUserConsumptionSelfSharesRankOnTie(t *testing.T) {
	now := setupConsumptionRankDB(t)
	dayAgo := now - 86400
	insertConsumeLog(t, 1, "alice", 500, dayAgo)
	insertConsumeLog(t, 2, "bob", 1000, dayAgo)
	insertConsumeLog(t, 3, "carol", 1000, dayAgo)

	start := now - 2*86400

	// 并列第一：没人严格高于我 → 名次 1，追赶目标 0
	bob, err := GetUserConsumptionSelf(2, start, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, bob.Rank)
	assert.EqualValues(t, 0, bob.AheadQuota)

	carol, err := GetUserConsumptionSelf(3, start, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, carol.Rank)

	// 第三名（两个并列第一压着我）：名次 3，追赶目标 1000
	alice, err := GetUserConsumptionSelf(1, start, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, alice.Rank)
	assert.EqualValues(t, 1000, alice.AheadQuota)
	assert.EqualValues(t, 3, alice.TotalUsers)
}

func TestGetUserConsumptionSelfIgnoresExcludedLogTypes(t *testing.T) {
	now := setupConsumptionRankDB(t)
	dayAgo := now - 86400
	insertConsumeLog(t, 2, "bob", 3000, dayAgo)
	// 用户 1 只有错误日志和 0 额度日志：按榜单口径不构成消耗
	require.NoError(t, LOG_DB.Create(&Log{
		UserId: 1, Username: "alice", Quota: 5000,
		Type: LogTypeError, CreatedAt: dayAgo,
	}).Error)
	insertConsumeLog(t, 1, "alice", 0, dayAgo)

	self, err := GetUserConsumptionSelf(1, now-2*86400, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, self.Rank)
	assert.EqualValues(t, 0, self.UsedQuota)
}
