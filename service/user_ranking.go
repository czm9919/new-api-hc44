package service

// ---------------------------------------------------------------------------
// hc47 用户消耗排行榜（公开榜 + 我的排名）
// ---------------------------------------------------------------------------
//
// 与 hc34 的 /api/user/consumption_rank 的区别：
//   - hc34 是管理员视图，能看到余额、累计消耗、已删除用户，用来做运营决策；
//   - 这里是**给用户自己看的**：用户名一律打码，只暴露「额度 / 请求数 / 名次」，
//     并额外告诉当前登录用户「你第几名、离上一名还差多少」。
//
// 设计取舍：
//   - 打码但不隐藏：榜单要能激起好胜心，全遮掉就没意义了，所以保留首尾字符，
//     同时给自己那一行加 is_self 标记（前端据此高亮 + 显示「我」）。
//   - 榜单聚合结果缓存 60 秒：logs 聚合是重查询，而「今日榜」又要求够新，
//     一分钟是查询压力和新鲜度的折中；「我的排名」不进缓存，永远实时。

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/model"
)

const (
	// userRankingCacheTTL 榜单（不含「我的排名」）的进程内缓存时间。
	userRankingCacheTTL = 60 * time.Second
	// UserRankingDefaultLimit 单次返回条数缺省值与上限。
	UserRankingDefaultLimit = 20
	UserRankingMaxLimit     = 100
)

// ErrUserRankingBadPeriod 周期参数不认识（today/week/month/year 之外的取值）。
var ErrUserRankingBadPeriod = errors.New("unsupported ranking period")

// UserRankingEntry 榜单里的一行。Username 已打码。
type UserRankingEntry struct {
	Rank         int     `json:"rank"`
	UserId       int     `json:"user_id"`
	Username     string  `json:"username"`
	UsedQuota    int64   `json:"used_quota"`
	RequestCount int64   `json:"request_count"`
	QuotaShare   float64 `json:"quota_share"`
	// IsSelf 由调用方的登录态决定，同一个缓存对象对每个人的取值都不同。
	IsSelf bool `json:"is_self"`
}

// UserRankingTotals 窗口内的全站汇总，用于占比与「共有多少人参与」。
type UserRankingTotals struct {
	UsedQuota    int64 `json:"used_quota"`
	RequestCount int64 `json:"request_count"`
	UserCount    int64 `json:"user_count"`
}

// UserRankingSelf 当前登录用户的排名信息。未登录时整个字段不下发。
type UserRankingSelf struct {
	// Ranked 为 false 表示窗口内没有消耗（或消耗为 0），此时 Rank = 0。
	Ranked       bool    `json:"ranked"`
	Rank         int     `json:"rank"`
	UsedQuota    int64   `json:"used_quota"`
	RequestCount int64   `json:"request_count"`
	AheadQuota   int64   `json:"ahead_quota"`
	GapToAhead   int64   `json:"gap_to_ahead"`
	TotalUsers   int64   `json:"total_users"`
	Percentile   float64 `json:"percentile"`
}

// UserRankingResponse GET /api/rankings/users 的响应体。
type UserRankingResponse struct {
	Period      string             `json:"period"`
	StartTs     int64              `json:"start_ts"`
	EndTs       int64              `json:"end_ts"`
	Leaderboard []UserRankingEntry `json:"leaderboard"`
	Totals      UserRankingTotals  `json:"totals"`
	Self        *UserRankingSelf   `json:"self,omitempty"`
	GeneratedAt int64              `json:"generated_at"`
}

// userRankingBoard 是缓存下来的榜单主体（不含 IsSelf / Self）。
type userRankingBoard struct {
	period      string
	startTs     int64
	endTs       int64
	totals      UserRankingTotals
	entries     []UserRankingEntry
	generatedAt int64
}

type userRankingCacheItem struct {
	expiresAt time.Time
	board     *userRankingBoard
}

var (
	userRankingCacheMu sync.RWMutex
	userRankingCache   = make(map[string]userRankingCacheItem)
)

// userRankingWindow 把周期换算成 [start, end) 的 Unix 秒窗口。
//
// 全部按服务器本地时区切（容器里 TZ=Asia/Shanghai），和运营看到的「今天」
// 「本周」一致。end 恒为 0，表示该侧不设上限（含刚刚产生的消耗）。
func userRankingWindow(period string, now time.Time) (string, int64, int64, error) {
	loc := now.Location()
	startOfDay := func(t time.Time) time.Time {
		year, month, day := t.Date()
		return time.Date(year, month, day, 0, 0, 0, 0, loc)
	}

	switch period {
	case "today":
		return "today", startOfDay(now).Unix(), 0, nil
	case "", "week":
		// 以周一为一周起点：Go 的 Weekday 里周日是 0，先折算成 0=周一。
		offset := (int(now.Weekday()) + 6) % 7
		return "week", startOfDay(now.AddDate(0, 0, -offset)).Unix(), 0, nil
	case "month":
		year, month, _ := now.Date()
		return "month", time.Date(year, month, 1, 0, 0, 0, 0, loc).Unix(), 0, nil
	case "year":
		return "year", time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, loc).Unix(), 0, nil
	default:
		return "", 0, 0, ErrUserRankingBadPeriod
	}
}

// buildUserRankingBoard 真正打日志库的路径：聚合 → 补用户名 → 打码。
func buildUserRankingBoard(period string, limit int, now time.Time) (*userRankingBoard, error) {
	normalized, start, end, err := userRankingWindow(period, now)
	if err != nil {
		return nil, err
	}

	entries, err := model.GetUserConsumptionRank(start, end, limit)
	if err != nil {
		return nil, err
	}
	totals, err := model.GetUserConsumptionTotals(start, end)
	if err != nil {
		return nil, err
	}

	// 日志里的 username 是写入当时的快照，改过名的会留下旧值；榜单展示以
	// users 表的当前用户名为准，取不到（用户已删）就退回日志里的值。
	userIds := make([]int, 0, len(entries))
	for _, entry := range entries {
		userIds = append(userIds, entry.UserId)
	}
	profiles, err := model.GetUserConsumptionProfiles(userIds)
	if err != nil {
		return nil, err
	}
	profileById := make(map[int]model.UserConsumptionProfile, len(profiles))
	for _, profile := range profiles {
		profileById[profile.Id] = profile
	}

	list := make([]UserRankingEntry, 0, len(entries))
	for i, entry := range entries {
		name := entry.Username
		if profile, ok := profileById[entry.UserId]; ok && profile.Username != "" {
			name = profile.Username
		}
		item := UserRankingEntry{
			Rank:         i + 1,
			UserId:       entry.UserId,
			Username:     model.MaskUsername(name),
			UsedQuota:    entry.UsedQuota,
			RequestCount: entry.RequestCount,
		}
		if totals.UsedQuota > 0 {
			item.QuotaShare = float64(entry.UsedQuota) / float64(totals.UsedQuota)
		}
		list = append(list, item)
	}

	return &userRankingBoard{
		period:  normalized,
		startTs: start,
		endTs:   end,
		totals: UserRankingTotals{
			UsedQuota:    totals.UsedQuota,
			RequestCount: totals.RequestCount,
			UserCount:    totals.UserCount,
		},
		entries:     list,
		generatedAt: now.Unix(),
	}, nil
}

// loadUserRankingBoard 带缓存地取榜单主体。
func loadUserRankingBoard(period string, limit int, now time.Time) (*userRankingBoard, error) {
	key := fmt.Sprintf("%s|%d", period, limit)

	userRankingCacheMu.RLock()
	item, ok := userRankingCache[key]
	userRankingCacheMu.RUnlock()
	if ok && now.Before(item.expiresAt) {
		return item.board, nil
	}

	board, err := buildUserRankingBoard(period, limit, now)
	if err != nil {
		return nil, err
	}

	userRankingCacheMu.Lock()
	userRankingCache[key] = userRankingCacheItem{
		expiresAt: now.Add(userRankingCacheTTL),
		board:     board,
	}
	userRankingCacheMu.Unlock()

	return board, nil
}

// GetUserRanking 组装公开榜单 + 「我的排名」。
//
// viewerId <= 0 表示匿名访问：只返回榜单，Self 字段不下发。
func GetUserRanking(period string, limit int, viewerId int) (*UserRankingResponse, error) {
	if limit <= 0 {
		limit = UserRankingDefaultLimit
	}
	if limit > UserRankingMaxLimit {
		limit = UserRankingMaxLimit
	}

	now := time.Now()
	normalized, _, _, err := userRankingWindow(period, now)
	if err != nil {
		return nil, err
	}

	board, err := loadUserRankingBoard(normalized, limit, now)
	if err != nil {
		return nil, err
	}

	// 复制一份再填 IsSelf：缓存对象会被并发请求共享，就地改会串数据。
	entries := make([]UserRankingEntry, len(board.entries))
	copy(entries, board.entries)

	resp := &UserRankingResponse{
		Period:      board.period,
		StartTs:     board.startTs,
		EndTs:       board.endTs,
		Leaderboard: entries,
		Totals:      board.totals,
		GeneratedAt: board.generatedAt,
	}

	if viewerId <= 0 {
		return resp, nil
	}

	self, err := model.GetUserConsumptionSelf(viewerId, board.startTs, board.endTs)
	if err != nil {
		return nil, err
	}

	for i := range entries {
		if entries[i].UserId == viewerId {
			entries[i].IsSelf = true
			break
		}
	}

	info := &UserRankingSelf{
		UsedQuota:    self.UsedQuota,
		RequestCount: self.RequestCount,
		TotalUsers:   self.TotalUsers,
	}
	if self.Rank > 0 {
		info.Ranked = true
		info.Rank = self.Rank
		info.AheadQuota = self.AheadQuota
		if self.AheadQuota > self.UsedQuota {
			info.GapToAhead = self.AheadQuota - self.UsedQuota
		}
		if self.TotalUsers > 0 {
			ahead := self.TotalUsers - int64(self.Rank)
			if ahead < 0 {
				ahead = 0
			}
			info.Percentile = float64(ahead) / float64(self.TotalUsers)
		}
	}
	resp.Self = info
	return resp, nil
}

// resetUserRankingCache 仅供测试清理缓存用。
func resetUserRankingCache() {
	userRankingCacheMu.Lock()
	userRankingCache = make(map[string]userRankingCacheItem)
	userRankingCacheMu.Unlock()
}
