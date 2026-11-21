package model

// ---------------------------------------------------------------------------
// B105 用量异常提醒的数据聚合
// ---------------------------------------------------------------------------

// SumQuotaByUserInWindow 返回 [start, end) 时间窗内每个用户的计费消耗总额（quota 单位）。
// 只统计 LogTypeConsume（真实扣费），按 user_id 聚合，方言安全（无日期函数）。
// 返回 map[userId]quota；查询失败时返回错误。
func SumQuotaByUserInWindow(start int64, end int64) (map[int]int64, error) {
	type row struct {
		UserId int   `gorm:"column:user_id"`
		Quota  int64 `gorm:"column:quota"`
	}
	rows := make([]row, 0, 64)
	err := LOG_DB.Model(&Log{}).
		Select("user_id, SUM(quota) AS quota").
		Where("type = ? AND created_at >= ? AND created_at < ?", LogTypeConsume, start, end).
		Group("user_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[int]int64, len(rows))
	for _, r := range rows {
		result[r.UserId] = r.Quota
	}
	return result, nil
}
