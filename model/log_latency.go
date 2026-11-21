package model

// GetUserLatencySamples 返回用户在时间窗内的请求耗时样本（秒，沿用日志 use_time 单位）。
// 只取成功计费的请求（LogTypeConsume），并按时间倒序限制样本量，
// 避免为了算分位数把整张大表拉进内存。
func GetUserLatencySamples(userId int, startTimestamp int64, endTimestamp int64, limit int) ([]int, error) {
	if limit <= 0 || limit > 20000 {
		limit = 10000
	}
	query := LOG_DB.Model(&Log{}).
		Where("user_id = ? AND type = ?", userId, LogTypeConsume)
	if startTimestamp > 0 {
		query = query.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp > 0 {
		query = query.Where("created_at <= ?", endTimestamp)
	}
	var samples []int
	err := query.Order("created_at desc").Limit(limit).Pluck("use_time", &samples).Error
	if err != nil {
		return nil, err
	}
	// use_time 为 0 表示上游没回耗时（流式或异常），样本里剔除，否则会把 p50 拉到 0。
	filtered := make([]int, 0, len(samples))
	for _, v := range samples {
		if v > 0 {
			filtered = append(filtered, v)
		}
	}
	return filtered, nil
}

// CountUserConsumeLogs 返回用户在时间窗内的计费请求条数（含 use_time=0 的），
// 用于前端提示「样本覆盖了多少请求」。
func CountUserConsumeLogs(userId int, startTimestamp int64, endTimestamp int64) (int64, error) {
	query := LOG_DB.Model(&Log{}).
		Where("user_id = ? AND type = ?", userId, LogTypeConsume)
	if startTimestamp > 0 {
		query = query.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp > 0 {
		query = query.Where("created_at <= ?", endTimestamp)
	}
	var total int64
	err := query.Count(&total).Error
	return total, err
}
