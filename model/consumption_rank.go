package model

// ---------------------------------------------------------------------------
// hc34 用户消耗排行榜
// ---------------------------------------------------------------------------

// UserConsumptionRankEntry 是单个用户在窗口内的消耗汇总。
// 窗口由调用方的 [start, end) 控制，start/end 为 0 表示该侧不设限
// （两个都为 0 即全时段）。
type UserConsumptionRankEntry struct {
	UserId       int    `json:"user_id" gorm:"column:user_id"`
	Username     string `json:"username" gorm:"column:username"`
	UsedQuota    int64  `json:"used_quota" gorm:"column:used_quota"`
	RequestCount int64  `json:"request_count" gorm:"column:request_count"`
	// LastActive 是窗口内最后一次消费的时间戳（秒）。
	LastActive int64 `json:"last_active" gorm:"column:last_active"`
}

// ConsumptionRankTotals 是窗口内的全站汇总，用于算占比与「榜外还有多少」。
type ConsumptionRankTotals struct {
	UsedQuota    int64
	RequestCount int64
	UserCount    int64
}

// GetUserConsumptionRank 按窗口内消费额度倒序取前 limit 名。
//
// 口径说明：
//   - 只统计 type = LogTypeConsume 的日志（充值/退款/错误不计入消耗）；
//   - 按 user_id 聚合而不是按 username：用户名可以改，日志里会同时留下新旧
//     用户名，按 user_id 聚合才不会把同一个人拆成两行；用户名取窗口内
//     最大值（MAX）作为展示口径；
//   - 消费额度 <= 0 的用户不上榜（免费额度或纯错误请求不构成"消耗"）。
func GetUserConsumptionRank(start, end int64, limit int) ([]UserConsumptionRankEntry, error) {
	if LOG_DB == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	tx := LOG_DB.Table("logs").
		Select("user_id, COALESCE(MAX(username), '') AS username, "+
			"COALESCE(SUM(quota), 0) AS used_quota, COUNT(*) AS request_count, "+
			"COALESCE(MAX(created_at), 0) AS last_active").
		Where("type = ? AND user_id > ?", LogTypeConsume, 0)
	if start > 0 {
		tx = tx.Where("created_at >= ?", start)
	}
	if end > 0 {
		tx = tx.Where("created_at < ?", end)
	}
	entries := make([]UserConsumptionRankEntry, 0, limit)
	err := tx.Group("user_id").
		Having("COALESCE(SUM(quota), 0) > 0").
		Order("used_quota DESC, user_id ASC").
		Limit(limit).
		Scan(&entries).Error
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// GetUserConsumptionTotals 汇总窗口内的全站消耗：额度、请求数、参与用户数。
// 用于排行榜的占比分母与「全站还有多少人没进前 N」。
func GetUserConsumptionTotals(start, end int64) (ConsumptionRankTotals, error) {
	var totals ConsumptionRankTotals
	if LOG_DB == nil {
		return totals, nil
	}
	tx := LOG_DB.Table("logs").
		Select("COALESCE(SUM(quota), 0) AS used_quota, COUNT(*) AS request_count, COUNT(DISTINCT user_id) AS user_count").
		Where("type = ? AND user_id > ?", LogTypeConsume, 0)
	if start > 0 {
		tx = tx.Where("created_at >= ?", start)
	}
	if end > 0 {
		tx = tx.Where("created_at < ?", end)
	}
	if err := tx.Scan(&totals).Error; err != nil {
		return totals, err
	}
	return totals, nil
}

// UserConsumptionProfile 榜单条目的用户侧补充信息。
// 日志库可能是独立的 ClickHouse，用户表在主库，因此这里单独取一次，
// 取不到（用户已删除）时前端只展示日志里的用户名。
type UserConsumptionProfile struct {
	Id          int    `json:"id" gorm:"column:id"`
	Username    string `json:"username" gorm:"column:username"`
	DisplayName string `json:"display_name" gorm:"column:display_name"`
	Quota       int    `json:"quota" gorm:"column:quota"`
	UsedQuota   int    `json:"used_quota" gorm:"column:used_quota"`
	Status      int    `json:"status" gorm:"column:status"`
}

// UserConsumptionSelf 是「我的排名」（hc47）：单个用户在窗口内的消耗、
// 名次、以及和上一名的差距。Rank 为 0 表示窗口内没有消耗（不上榜）。
type UserConsumptionSelf struct {
	UserId       int
	UsedQuota    int64
	RequestCount int64
	Rank         int
	// AheadQuota 是紧邻我上面那一名的窗口消耗；Rank<=1 时为 0。
	AheadQuota int64
	// TotalUsers 是窗口内有消耗的人数（含未进前 N 的人），用于「超过 x% 用户」。
	TotalUsers int64
}

// GetUserConsumptionSelf 计算某用户在窗口内的消耗、名次与追赶目标。
//
// 名次口径与 GetUserConsumptionRank 完全一致（同窗口、同 type 过滤、同
// user_id 聚合、同「消耗 > 0」门槛）：名次 = 消耗严格大于我的人数 + 1，
// 因此并列同额度的人共享名次，不会出现「同分不同名」的迷惑结果。
func GetUserConsumptionSelf(userId int, start, end int64) (UserConsumptionSelf, error) {
	var self UserConsumptionSelf
	if LOG_DB == nil || userId <= 0 {
		return self, nil
	}
	self.UserId = userId

	// 1) 我在窗口内的消耗与请求数。
	mine := LOG_DB.Table("logs").
		Select("COALESCE(SUM(quota), 0) AS used_quota, COUNT(*) AS request_count").
		Where("type = ? AND user_id = ?", LogTypeConsume, userId)
	if start > 0 {
		mine = mine.Where("created_at >= ?", start)
	}
	if end > 0 {
		mine = mine.Where("created_at < ?", end)
	}
	var mineRow struct {
		UsedQuota    int64 `gorm:"column:used_quota"`
		RequestCount int64 `gorm:"column:request_count"`
	}
	if err := mine.Scan(&mineRow).Error; err != nil {
		return self, err
	}
	self.UsedQuota = mineRow.UsedQuota
	self.RequestCount = mineRow.RequestCount

	// 2) 窗口内有消耗的总人数：没有这个数就没法说「你超过了 87% 的人」。
	totals, err := GetUserConsumptionTotals(start, end)
	if err != nil {
		return self, err
	}
	self.TotalUsers = totals.UserCount

	// 消耗 <= 0 与榜单口径一致，一律不上榜（名次 0）。
	if self.UsedQuota <= 0 {
		return self, nil
	}

	// 3) 比我高的人数 + 紧邻我上一名的消耗。
	//
	// 用「按 user_id 聚合后的派生表」再统计，才能按"人均消耗"排名；
	// 直接在 logs 上比单条 quota 会把大客户的零散小额请求算漏。
	agg := LOG_DB.Table("logs").
		Select("SUM(quota) AS used_quota").
		Where("type = ? AND user_id > ?", LogTypeConsume, 0)
	if start > 0 {
		agg = agg.Where("created_at >= ?", start)
	}
	if end > 0 {
		agg = agg.Where("created_at < ?", end)
	}
	agg = agg.Group("user_id").Having("COALESCE(SUM(quota), 0) > 0")

	var ahead struct {
		AheadCount int64 `gorm:"column:ahead_count"`
		NextQuota  int64 `gorm:"column:next_quota"`
	}
	err = LOG_DB.Table("(?) as ranked", agg).
		Select("COUNT(*) AS ahead_count, COALESCE(MIN(used_quota), 0) AS next_quota").
		Where("used_quota > ?", self.UsedQuota).
		Scan(&ahead).Error
	if err != nil {
		return self, err
	}
	self.Rank = int(ahead.AheadCount) + 1
	self.AheadQuota = ahead.NextQuota
	return self, nil
}

// GetUserConsumptionProfiles 批量取榜单用户的展示名、余额与累计消耗。
func GetUserConsumptionProfiles(userIds []int) ([]UserConsumptionProfile, error) {
	profiles := make([]UserConsumptionProfile, 0, len(userIds))
	if len(userIds) == 0 {
		return profiles, nil
	}
	// 夹紧一次查询的 id 数量，避免超长 IN 列表（limit 上限 100，正常不会触发）。
	if len(userIds) > 200 {
		userIds = userIds[:200]
	}
	err := DB.Model(&User{}).
		Select("id, username, display_name, quota, used_quota, status").
		Where("id IN ?", userIds).
		Scan(&profiles).Error
	if err != nil {
		return nil, err
	}
	return profiles, nil
}
