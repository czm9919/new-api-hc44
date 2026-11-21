package controller

import (
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ConsumptionRankMaxDays 回看窗口上限（天）。排行榜是运营视图，超过一年的
// 窗口与"全部"没有实际差别，还会拖慢日志库聚合，这里夹紧。
const ConsumptionRankMaxDays = 365

// ConsumptionRankMaxLimit 单次最多返回条数，与 model 层的兜底保持一致。
const ConsumptionRankMaxLimit = 100

// GetUserConsumptionRank 用户消耗排行榜（hc34）：
// 管理员专属，按窗口内消费额度倒序返回前 N 名。
//
// 查询参数：
//   - days：回看天数，0 表示全部时段；缺省 30，负数/超上限按上限或缺省处理
//   - limit：返回条数，缺省 20，上限 100
//
// 响应：
//   - leaderboard：榜单条目（rank 从 1 起，quota_share 是占全站窗口消耗的比例）
//   - totals：全站窗口汇总（额度/请求数/参与用户数）
//   - window：实际生效的查询窗口（start_ts/end_ts，0 表示该侧不设限）
func GetUserConsumptionRank(c *gin.Context) {
	days, _ := strconv.Atoi(c.Query("days"))
	if days < 0 {
		days = 0
	}
	if days > ConsumptionRankMaxDays {
		days = ConsumptionRankMaxDays
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 {
		limit = 20
	}
	if limit > ConsumptionRankMaxLimit {
		limit = ConsumptionRankMaxLimit
	}

	now := time.Now().Unix()
	var start int64
	if days > 0 {
		start = now - int64(days)*86400
	}

	entries, err := model.GetUserConsumptionRank(start, 0, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	totals, err := model.GetUserConsumptionTotals(start, 0)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 拉用户侧展示信息：展示名、当前余额、累计消耗、状态。
	userIds := make([]int, 0, len(entries))
	for _, entry := range entries {
		userIds = append(userIds, entry.UserId)
	}
	profiles, err := model.GetUserConsumptionProfiles(userIds)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	profileById := make(map[int]model.UserConsumptionProfile, len(profiles))
	for _, profile := range profiles {
		profileById[profile.Id] = profile
	}

	type leaderboardEntry struct {
		Rank         int     `json:"rank"`
		UserId       int     `json:"user_id"`
		Username     string  `json:"username"`
		DisplayName  string  `json:"display_name"`
		UsedQuota    int64   `json:"used_quota"`
		QuotaShare   float64 `json:"quota_share"`
		RequestCount int64   `json:"request_count"`
		LastActive   int64   `json:"last_active"`
		// RemainingQuota 是用户当前余额（快照），Deleted 为 true 表示用户
		// 已从 users 表删除（或日志库里只剩历史记录）。
		RemainingQuota int  `json:"remaining_quota"`
		LifetimeQuota  int  `json:"lifetime_quota"`
		Status         int  `json:"status"`
		Deleted        bool `json:"deleted"`
	}
	list := make([]leaderboardEntry, 0, len(entries))
	for i, entry := range entries {
		item := leaderboardEntry{
			Rank:         i + 1,
			UserId:       entry.UserId,
			Username:     entry.Username,
			UsedQuota:    entry.UsedQuota,
			RequestCount: entry.RequestCount,
			LastActive:   entry.LastActive,
			Status:       0,
			Deleted:      true,
		}
		if profile, ok := profileById[entry.UserId]; ok {
			// 日志里的 username 是写入当时的快照，改过名的用户会留下旧值；
			// 榜单展示以 users 表的当前用户名为准。
			if profile.Username != "" {
				item.Username = profile.Username
			}
			item.DisplayName = profile.DisplayName
			item.RemainingQuota = profile.Quota
			item.LifetimeQuota = profile.UsedQuota
			item.Status = profile.Status
			item.Deleted = false
		}
		if totals.UsedQuota > 0 {
			item.QuotaShare = float64(entry.UsedQuota) / float64(totals.UsedQuota)
		}
		list = append(list, item)
	}

	common.ApiSuccess(c, gin.H{
		"leaderboard": list,
		"totals": gin.H{
			"used_quota":    totals.UsedQuota,
			"request_count": totals.RequestCount,
			"user_count":    totals.UserCount,
		},
		"window": gin.H{
			"days":     days,
			"start_ts": start,
			"end_ts":   int64(0),
		},
		"generated_at": now,
	})
}
