package model

// ---------------------------------------------------------------------------
// B63 邀请排行榜
// ---------------------------------------------------------------------------

// AffLeaderboardMinCount 上榜门槛：邀请人数至少达到该值才出现在榜单里，
// 避免「0 人邀请」的用户挤满榜单、也保护低活跃用户的隐私。
const AffLeaderboardMinCount = 1

// AffLeaderboardEntry 榜单条目。Username 在 controller 层脱敏后输出。
type AffLeaderboardEntry struct {
	UserId   int    `json:"user_id"`
	Username string `json:"username"`
	AffCount int    `json:"aff_count"`
}

// GetAffLeaderboard 按邀请人数倒序取前 limit 名。
// limit 由调用方夹紧，这里再做一次兜底，防止全表扫描。
func GetAffLeaderboard(limit int) ([]AffLeaderboardEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	entries := make([]AffLeaderboardEntry, 0, limit)
	err := DB.Model(&User{}).
		Select("id AS user_id, username, aff_count").
		Where("aff_count >= ?", AffLeaderboardMinCount).
		Order("aff_count DESC, id ASC").
		Limit(limit).
		Scan(&entries).Error
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// GetUserAffRank 返回用户的名次（1 起）与上榜总人数。
// 未达门槛 / 未产生邀请时 rank 返回 0，表示不上榜。
func GetUserAffRank(userId int) (rank int, total int, affCount int, err error) {
	var user User
	if err = DB.Select("id, aff_count").Where("id = ?", userId).First(&user).Error; err != nil {
		return 0, 0, 0, err
	}
	affCount = user.AffCount
	if affCount < AffLeaderboardMinCount {
		// 未达门槛时 rank 返回 0，仍返回上榜总人数，方便前端展示差距。
		var totalCount int64
		DB.Model(&User{}).Where("aff_count >= ?", AffLeaderboardMinCount).Count(&totalCount)
		return 0, int(totalCount), affCount, nil
	}
	var ahead int64
	if err = DB.Model(&User{}).
		Where("aff_count > ?", affCount).
		Count(&ahead).Error; err != nil {
		return 0, 0, affCount, err
	}
	var totalCount int64
	if err = DB.Model(&User{}).
		Where("aff_count >= ?", AffLeaderboardMinCount).
		Count(&totalCount).Error; err != nil {
		return 0, 0, affCount, err
	}
	// 名次 = 邀请数严格大于我的人数 + 1（并列同名次）。
	rank = int(ahead) + 1
	total = int(totalCount)
	return rank, total, affCount, nil
}
