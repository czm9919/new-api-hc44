package service

import (
	"sort"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// =============================================================================
// v1.1.0 M2 · B93 密钥泄露风险检测
//
// 基于日志按 token + IP 的聚合（model.GetTokenIPUsageSince）识别两类异常：
//   1. multi_ip             —— 同一把 key 在窗口内被多个不同 IP 使用；
//   2. ip_outside_allowlist —— 使用了 AllowIps 白名单之外的 IP（配置与实际冲突）。
// 输出按风险等级排序的明细与轮换建议，供用户侧自查与管理端巡检。
// =============================================================================

const (
	// TokenRiskMaxDays 分析窗口上限（天），防止全表扫描。
	TokenRiskMaxDays = 90
	// TokenRiskDefaultDays 缺省回看天数。
	TokenRiskDefaultDays = 30
	// TokenRiskMultiIPThreshold 触发 multi_ip 风险的最少不同 IP 数。
	TokenRiskMultiIPThreshold = 3
	// TokenRiskHighIPThreshold 不同 IP 数达到该值直接判高风险。
	TokenRiskHighIPThreshold = 5
	// TokenRiskOverviewLimit 管理端概览默认最多返回的风险条目数。
	TokenRiskOverviewLimit = 100
	// TokenRiskMaxTokensPerUser 用户侧单次最多分析的 token 数。
	TokenRiskMaxTokensPerUser = 1000
)

const (
	TokenRiskLevelHigh   = "high"
	TokenRiskLevelMedium = "medium"
)

const (
	TokenRiskReasonMultiIP = "multi_ip"
	TokenRiskReasonAllowIP = "ip_outside_allowlist"
)

// TokenRiskIP 单个 IP 的使用证据。
type TokenRiskIP struct {
	Ip           string `json:"ip"`
	RequestCount int64  `json:"request_count"`
	LastSeenAt   int64  `json:"last_seen_at"`
	// Allowed 该 IP 是否在 AllowIps 白名单内（未设置白名单时恒为 true）。
	Allowed bool `json:"allowed"`
}

// TokenRiskItem 单把密钥的风险明细。
type TokenRiskItem struct {
	TokenId      int           `json:"token_id"`
	TokenName    string        `json:"token_name"`
	UserId       int           `json:"user_id,omitempty"`
	Username     string        `json:"username,omitempty"`
	Status       int           `json:"status,omitempty"`
	RiskLevel    string        `json:"risk_level"`
	Reasons      []string      `json:"reasons"`
	DistinctIPs  int           `json:"distinct_ips"`
	RequestCount int64         `json:"request_count"`
	AllowIps     []string      `json:"allow_ips"`
	IPs          []TokenRiskIP `json:"ips"`
	// LastUsedAt 窗口内最后一次使用时间。
	LastUsedAt     int64  `json:"last_used_at"`
	Recommendation string `json:"recommendation"`
}

// TokenRiskReport 密钥泄露风险报告。
type TokenRiskReport struct {
	WindowDays  int             `json:"window_days"`
	GeneratedAt int64           `json:"generated_at"`
	TotalTokens int             `json:"total_tokens"` // 窗口内有使用记录的 token 数
	RiskyTokens int             `json:"risky_tokens"` // 命中风险规则的 token 数
	Items       []TokenRiskItem `json:"items"`
}

// NormalizeTokenRiskDays 校正回看天数：非法/缺省回 30 天，上限 90 天。
func NormalizeTokenRiskDays(days int) int {
	if days <= 0 {
		return TokenRiskDefaultDays
	}
	if days > TokenRiskMaxDays {
		return TokenRiskMaxDays
	}
	return days
}

// BuildTokenRiskReport 用户侧自查：分析自己名下密钥的泄露风险。
func BuildTokenRiskReport(userId int, days int) (*TokenRiskReport, error) {
	days = NormalizeTokenRiskDays(days)
	tokens, err := model.GetAllUserTokens(userId, 0, TokenRiskMaxTokensPerUser)
	if err != nil {
		return nil, err
	}
	tokenMap := make(map[int]*model.Token, len(tokens))
	for _, t := range tokens {
		tokenMap[t.Id] = t
	}
	usage, err := model.GetTokenIPUsageSince(time.Now().Unix() - int64(days)*86400)
	if err != nil {
		return nil, err
	}
	// 只保留自己名下 token 的聚合行，避免把其他用户的用量算进窗口统计。
	own := make([]model.TokenIPUsage, 0, len(usage))
	for _, row := range usage {
		if _, ok := tokenMap[row.TokenId]; ok {
			own = append(own, row)
		}
	}
	return buildTokenRiskReport(own, tokenMap, days), nil
}

// BuildTokenRiskOverview 管理端巡检：跨用户汇总风险密钥（按风险降序截断）。
func BuildTokenRiskOverview(days int, limit int) (*TokenRiskReport, error) {
	days = NormalizeTokenRiskDays(days)
	usage, err := model.GetTokenIPUsageSince(time.Now().Unix() - int64(days)*86400)
	if err != nil {
		return nil, err
	}
	report := buildTokenRiskReport(usage, nil, days)
	ids := make([]int, 0, len(report.Items))
	for _, item := range report.Items {
		ids = append(ids, item.TokenId)
	}
	tokenList, err := model.GetTokensByIds(ids)
	if err != nil {
		return nil, err
	}
	tokenMap := make(map[int]*model.Token, len(tokenList))
	for _, t := range tokenList {
		tokenMap[t.Id] = t
	}
	for i := range report.Items {
		item := &report.Items[i]
		t := tokenMap[item.TokenId]
		if t == nil {
			continue
		}
		item.TokenName = t.Name
		item.UserId = t.UserId
		item.Status = t.Status
		if username, uerr := model.GetUsernameById(t.UserId, false); uerr == nil {
			item.Username = username
		}
	}
	if limit <= 0 {
		limit = TokenRiskOverviewLimit
	}
	if len(report.Items) > limit {
		report.Items = report.Items[:limit]
	}
	return report, nil
}

// buildTokenRiskReport 共同实现：token+IP 聚合行 → 风险报告。
// tokenMap 为 nil 时只做风险识别，元数据由调用方（管理端路径）补齐。
func buildTokenRiskReport(usage []model.TokenIPUsage, tokenMap map[int]*model.Token, days int) *TokenRiskReport {
	report := &TokenRiskReport{
		WindowDays:  days,
		GeneratedAt: time.Now().Unix(),
		Items:       make([]TokenRiskItem, 0),
	}

	type ipAgg struct {
		ips          []TokenRiskIP
		requestCount int64
		lastUsedAt   int64
	}
	aggs := make(map[int]*ipAgg)
	for _, row := range usage {
		a := aggs[row.TokenId]
		if a == nil {
			a = &ipAgg{}
			aggs[row.TokenId] = a
		}
		a.ips = append(a.ips, TokenRiskIP{Ip: row.Ip, RequestCount: row.RequestCount, LastSeenAt: row.LastSeenAt})
		a.requestCount += row.RequestCount
		if row.LastSeenAt > a.lastUsedAt {
			a.lastUsedAt = row.LastSeenAt
		}
	}
	report.TotalTokens = len(aggs)

	for tokenId, a := range aggs {
		var token *model.Token
		if tokenMap != nil {
			token = tokenMap[tokenId]
		}
		var allow []string
		allowSet := make(map[string]struct{})
		if token != nil {
			allow = token.GetIpLimits()
			for _, ip := range allow {
				allowSet[ip] = struct{}{}
			}
		}
		for i := range a.ips {
			_, allowed := allowSet[a.ips[i].Ip]
			a.ips[i].Allowed = len(allow) == 0 || allowed
		}

		distinctIPs := len(a.ips)
		outside := 0
		if len(allow) > 0 {
			for _, ip := range a.ips {
				if _, ok := allowSet[ip.Ip]; !ok {
					outside++
				}
			}
		}

		reasons := make([]string, 0, 2)
		level := ""
		if outside > 0 {
			reasons = append(reasons, TokenRiskReasonAllowIP)
			level = TokenRiskLevelHigh
		}
		if distinctIPs >= TokenRiskHighIPThreshold {
			reasons = append(reasons, TokenRiskReasonMultiIP)
			level = TokenRiskLevelHigh
		} else if distinctIPs >= TokenRiskMultiIPThreshold {
			reasons = append(reasons, TokenRiskReasonMultiIP)
			if level == "" {
				level = TokenRiskLevelMedium
			}
		}
		if len(reasons) == 0 {
			continue
		}

		item := TokenRiskItem{
			TokenId:        tokenId,
			RiskLevel:      level,
			Reasons:        reasons,
			DistinctIPs:    distinctIPs,
			RequestCount:   a.requestCount,
			AllowIps:       allow,
			IPs:            a.ips,
			LastUsedAt:     a.lastUsedAt,
			Recommendation: tokenRiskRecommendation(reasons, level),
		}
		if token != nil {
			item.TokenName = token.Name
			item.Status = token.Status
		}
		report.Items = append(report.Items, item)
		report.RiskyTokens++
	}

	// 风险降序、请求数降序。
	sort.Slice(report.Items, func(i, j int) bool {
		hi, hj := report.Items[i].RiskLevel == TokenRiskLevelHigh, report.Items[j].RiskLevel == TokenRiskLevelHigh
		if hi != hj {
			return hi
		}
		if report.Items[i].RequestCount != report.Items[j].RequestCount {
			return report.Items[i].RequestCount > report.Items[j].RequestCount
		}
		return report.Items[i].TokenId < report.Items[j].TokenId
	})
	return report
}

func tokenRiskRecommendation(reasons []string, level string) string {
	rec := "建议轮换该密钥（删除后新建 Token 并更新调用方），"
	if containsString(reasons, TokenRiskReasonAllowIP) {
		rec += "同时核查 AllowIps 白名单与实际调用来源是否一致；"
	} else {
		rec += "检查是否被多人共享或硬编码进了客户端代码；"
	}
	if level == TokenRiskLevelHigh {
		rec = "高风险：" + rec + "必要时立即禁用该密钥。"
	} else {
		rec += "并确认调用来源均为本人可控环境。"
	}
	return rec
}

func containsString(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}
