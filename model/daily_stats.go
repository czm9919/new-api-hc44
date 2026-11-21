package model

import "math"

// 运营日报的聚合查询
//
// 与 usage_stats.go 一样，统计口径统一落在 logs 表 type=LogTypeConsume 的行上：
// 那是真正扣过费的请求，避免把「失败/未计费」的调用算进消耗。
// 时间边界一律用 Unix 秒的半开区间 [start, end)，跨 SQLite / MySQL / PostgreSQL
// 都能直接走 created_at 索引。

// DailySummary 某一天（或任意区间）的运营汇总。
type DailySummary struct {
	StartUnix   int64            `json:"start_unix"`
	EndUnix     int64            `json:"end_unix"`
	Requests    int64            `json:"requests"`
	Quota       int64            `json:"quota"`
	ActiveUsers int64            `json:"active_users"`
	NewUsers    int64            `json:"new_users"`
	NewBinds    int64            `json:"new_binds"`
	TopUsers    []ConsumptionRow `json:"top_users"`
}

// GetDailySummary 汇总 [start, end) 区间内的请求量、消耗与新增。
// topN <= 0 时不查消耗榜（省一次分组查询）。
func GetDailySummary(start, end int64, topN int) (DailySummary, error) {
	summary := DailySummary{StartUnix: start, EndUnix: end}

	var agg struct {
		Requests int64
		Quota    float64
		Users    int64
	}
	err := DB.Model(&Log{}).
		Select("COUNT(*) AS requests, COALESCE(SUM(quota), 0) AS quota, COUNT(DISTINCT user_id) AS users").
		Where("type = ? AND created_at >= ? AND created_at < ?", LogTypeConsume, start, end).
		Scan(&agg).Error
	if err != nil {
		return summary, err
	}
	summary.Requests = agg.Requests
	summary.Quota = int64(math.Round(agg.Quota))
	summary.ActiveUsers = agg.Users

	if err := DB.Model(&User{}).
		Where("created_at >= ? AND created_at < ?", start, end).
		Count(&summary.NewUsers).Error; err != nil {
		return summary, err
	}

	if err := DB.Model(&User{}).
		Where("qq <> '' AND qq_bound_at >= ? AND qq_bound_at < ?", start, end).
		Count(&summary.NewBinds).Error; err != nil {
		return summary, err
	}

	if topN > 0 {
		top, err := GetTopConsumersBetween(start, end, topN)
		if err != nil {
			return summary, err
		}
		summary.TopUsers = top
	}
	return summary, nil
}

// GetTopConsumersBetween 返回 [start, end) 内消耗最高的前 limit 个用户。
// 与 GetTopConsumersSince 的差别只在右边界：日报要看「昨天一整天」，
// 没有上界会把今天上午的量也算进去。
func GetTopConsumersBetween(start, end int64, limit int) ([]ConsumptionRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows := make([]ConsumptionRow, 0, limit)
	err := DB.Model(&Log{}).
		Select("user_id, username, COALESCE(SUM(quota), 0) AS quota, COUNT(*) AS requests").
		Where("type = ? AND created_at >= ? AND created_at < ?", LogTypeConsume, start, end).
		Group("user_id, username").
		Order("quota DESC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// ChannelStatusCount 渠道按状态的数量分布。
type ChannelStatusCount struct {
	Enabled int64
	Auto    int64 // 被自动禁用
	Manual  int64 // 被人手动禁用
	Total   int64
}

// CountChannelsByStatus 统计渠道状态。日报要知道「昨天有没有渠道被自动关掉」。
func CountChannelsByStatus() (ChannelStatusCount, error) {
	rows := make([]struct {
		Status int
		Count  int64
	}, 0, 4)
	err := DB.Model(&Channel{}).
		Select("status, COUNT(*) AS count").
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return ChannelStatusCount{}, err
	}
	var out ChannelStatusCount
	for _, row := range rows {
		out.Total += row.Count
		switch row.Status {
		case 1:
			out.Enabled = row.Count
		case 2:
			out.Manual = row.Count
		case 3:
			out.Auto = row.Count
		}
	}
	return out, nil
}
