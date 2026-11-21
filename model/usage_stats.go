package model

import (
	"math"
	"strings"
	"time"
)

// 用量统计（每日额度护栏 / QQ 自助查询共用）
//
// 数据源统一走 logs 表：type=LogTypeConsume 的行才是真正扣过费的请求，
// created_at 是 Unix 秒，跨 SQLite / MySQL / PostgreSQL 都能直接用
// `created_at >= ?` 走索引，不需要各库写一套日期函数。

// StartOfTodayUnix 返回服务器本地时区今天 00:00:00 的 Unix 秒。
// 容器里 TZ=Asia/Shanghai，所以「今天」跟运营看到的一致。
func StartOfTodayUnix() int64 {
	now := time.Now()
	year, month, day := now.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, now.Location()).Unix()
}

// GetUserConsumedQuotaSince 统计某用户从 since 起累计消耗的额度。
// 只统计消费日志；查询失败返回错误，由调用方决定是放行还是拒绝。
func GetUserConsumedQuotaSince(userId int, since int64) (int64, error) {
	if userId <= 0 {
		return 0, nil
	}
	var total float64
	err := DB.Model(&Log{}).
		Select("COALESCE(SUM(quota), 0)").
		Where("user_id = ? AND type = ? AND created_at >= ?", userId, LogTypeConsume, since).
		Scan(&total).Error
	if err != nil {
		return 0, err
	}
	return int64(math.Round(total)), nil
}

// ConsumptionRow 一条「某人今天用了多少」的聚合行。
type ConsumptionRow struct {
	UserId   int     `json:"user_id"`
	Username string  `json:"username"`
	Quota    float64 `json:"quota"`
	Requests int64   `json:"requests"`
}

// GetTopConsumersSince 返回 since 之后消耗额度最高的前 limit 个用户。
// 用于排行榜：QQ 群里做「今日用量榜」，比纯文字统计有攀比感。
func GetTopConsumersSince(since int64, limit int) ([]ConsumptionRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows := make([]ConsumptionRow, 0, limit)
	err := DB.Model(&Log{}).
		Select("user_id, username, COALESCE(SUM(quota), 0) AS quota, COUNT(*) AS requests").
		Where("type = ? AND created_at >= ?", LogTypeConsume, since).
		Group("user_id, username").
		Order("quota DESC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// MaskUsername 群聊里展示用户名时打码，只留首尾各 1~2 个字符。
// 排行榜要让人看得出「是我」，但不能把完整用户名广播到几百人的群里。
func MaskUsername(name string) string {
	name = strings.TrimSpace(name)
	runes := []rune(name)
	switch {
	case len(runes) == 0:
		return "匿名"
	case len(runes) == 1:
		return string(runes)
	case len(runes) == 2:
		return string(runes[0]) + "*"
	case len(runes) <= 4:
		return string(runes[0]) + "**" + string(runes[len(runes)-1])
	default:
		return string(runes[:2]) + "***" + string(runes[len(runes)-2:])
	}
}
