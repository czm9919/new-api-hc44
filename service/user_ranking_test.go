package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// setupUserRankingDB 注入独立内存库（logs + users），并清掉榜单缓存。
func setupUserRankingDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{},
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.User{}))
	previousDB, previousLogDB := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = db, db
	resetUserRankingCache()
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		resetUserRankingCache()
	})
}

func insertUserRankingLog(t *testing.T, userId int, username string, quota int) {
	t.Helper()
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId: userId, Username: username, Quota: quota,
		Type: model.LogTypeConsume, CreatedAt: time.Now().Unix(), ModelName: "gpt-test",
	}).Error)
}

func TestUserRankingWindowResolvesLocalPeriods(t *testing.T) {
	loc := time.Local
	// 2026-03-05 是周四
	now := time.Date(2026, 3, 5, 14, 30, 0, 0, loc)

	period, start, end, err := userRankingWindow("today", now)
	require.NoError(t, err)
	assert.Equal(t, "today", period)
	assert.Equal(t, time.Date(2026, 3, 5, 0, 0, 0, 0, loc).Unix(), start)
	assert.Zero(t, end, "end 恒为 0，表示不设上限")

	period, start, _, err = userRankingWindow("week", now)
	require.NoError(t, err)
	assert.Equal(t, "week", period)
	assert.Equal(t, time.Date(2026, 3, 2, 0, 0, 0, 0, loc).Unix(), start, "一周从周一开始")

	period, start, _, err = userRankingWindow("month", now)
	require.NoError(t, err)
	assert.Equal(t, "month", period)
	assert.Equal(t, time.Date(2026, 3, 1, 0, 0, 0, 0, loc).Unix(), start)

	period, start, _, err = userRankingWindow("year", now)
	require.NoError(t, err)
	assert.Equal(t, "year", period)
	assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, loc).Unix(), start)
}

func TestUserRankingWindowTreatsSundayAsPreviousMonday(t *testing.T) {
	loc := time.Local
	// 2026-03-08 是周日，应归到 2026-03-02（周一）那一周
	sunday := time.Date(2026, 3, 8, 23, 59, 0, 0, loc)
	period, start, _, err := userRankingWindow("week", sunday)
	require.NoError(t, err)
	assert.Equal(t, "week", period)
	assert.Equal(t, time.Date(2026, 3, 2, 0, 0, 0, 0, loc).Unix(), start)
}

func TestUserRankingWindowDefaultsToWeekAndRejectsUnknown(t *testing.T) {
	now := time.Date(2026, 3, 5, 14, 30, 0, 0, time.Local)

	period, start, _, err := userRankingWindow("", now)
	require.NoError(t, err)
	assert.Equal(t, "week", period)
	assert.Equal(t, time.Date(2026, 3, 2, 0, 0, 0, 0, time.Local).Unix(), start)

	_, _, _, err = userRankingWindow("decade", now)
	assert.ErrorIs(t, err, ErrUserRankingBadPeriod)
}

func TestGetUserRankingMasksNamesAndFlagsSelf(t *testing.T) {
	setupUserRankingDB(t)
	insertUserRankingLog(t, 1, "alice", 1000)
	insertUserRankingLog(t, 2, "bob", 3000)

	resp, err := GetUserRanking("today", 10, 1)
	require.NoError(t, err)
	require.Len(t, resp.Leaderboard, 2)

	// 额度倒序：bob(3000) 第一，alice(1000) 第二
	assert.Equal(t, 2, resp.Leaderboard[0].UserId)
	assert.Equal(t, 1, resp.Leaderboard[1].UserId)
	// 用户名一律打码
	assert.Equal(t, model.MaskUsername("bob"), resp.Leaderboard[0].Username)
	assert.Equal(t, model.MaskUsername("alice"), resp.Leaderboard[1].Username)
	// 只有自己那一行带 is_self
	assert.False(t, resp.Leaderboard[0].IsSelf)
	assert.True(t, resp.Leaderboard[1].IsSelf)

	require.NotNil(t, resp.Self)
	assert.True(t, resp.Self.Ranked)
	assert.Equal(t, 2, resp.Self.Rank)
	assert.EqualValues(t, 1000, resp.Self.UsedQuota)
	assert.EqualValues(t, 3000, resp.Self.AheadQuota)
	assert.EqualValues(t, 2000, resp.Self.GapToAhead)
	assert.EqualValues(t, 2, resp.Self.TotalUsers)
	// 2 人里第 2 名 → 超过 0%
	assert.InDelta(t, 0, resp.Self.Percentile, 1e-9)

	assert.EqualValues(t, 4000, resp.Totals.UsedQuota)
	assert.EqualValues(t, 2, resp.Totals.UserCount)
}

func TestGetUserRankingAnonymousHasNoSelf(t *testing.T) {
	setupUserRankingDB(t)
	insertUserRankingLog(t, 1, "alice", 1000)

	resp, err := GetUserRanking("today", 10, 0)
	require.NoError(t, err)
	require.Len(t, resp.Leaderboard, 1)
	assert.Nil(t, resp.Self)
	assert.False(t, resp.Leaderboard[0].IsSelf)
}

// 缓存对象被并发请求共享：不同 viewer 复用时不能互相污染 is_self。
func TestGetUserRankingCacheDoesNotLeakSelfFlag(t *testing.T) {
	setupUserRankingDB(t)
	insertUserRankingLog(t, 1, "alice", 1000)
	insertUserRankingLog(t, 2, "bob", 3000)

	first, err := GetUserRanking("today", 10, 1)
	require.NoError(t, err)
	require.Len(t, first.Leaderboard, 2)
	assert.True(t, first.Leaderboard[1].IsSelf)

	second, err := GetUserRanking("today", 10, 2)
	require.NoError(t, err)
	require.Len(t, second.Leaderboard, 2)
	assert.True(t, second.Leaderboard[0].IsSelf)
	assert.False(t, second.Leaderboard[1].IsSelf)

	// 第一次返回的切片也不能被第二次调用改掉
	assert.False(t, first.Leaderboard[0].IsSelf)
	assert.True(t, first.Leaderboard[1].IsSelf)
}

func TestGetUserRankingSelfUnranked(t *testing.T) {
	setupUserRankingDB(t)
	insertUserRankingLog(t, 2, "bob", 3000)

	// 用户 1 今天没有任何消费
	resp, err := GetUserRanking("today", 10, 1)
	require.NoError(t, err)
	require.NotNil(t, resp.Self)
	assert.False(t, resp.Self.Ranked)
	assert.Equal(t, 0, resp.Self.Rank)
	assert.EqualValues(t, 0, resp.Self.UsedQuota)
	assert.EqualValues(t, 0, resp.Self.GapToAhead)
	assert.EqualValues(t, 1, resp.Self.TotalUsers)
}

func TestGetUserRankingLimitClampAndBadPeriod(t *testing.T) {
	setupUserRankingDB(t)
	insertUserRankingLog(t, 1, "alice", 1000)
	insertUserRankingLog(t, 2, "bob", 3000)

	resp, err := GetUserRanking("today", -5, 0)
	require.NoError(t, err)
	assert.Equal(t, "today", resp.Period)
	require.Len(t, resp.Leaderboard, 2)

	resp, err = GetUserRanking("today", 1, 0)
	require.NoError(t, err)
	assert.Len(t, resp.Leaderboard, 1)

	_, err = GetUserRanking("decade", 10, 0)
	assert.ErrorIs(t, err, ErrUserRankingBadPeriod)
}
