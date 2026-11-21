package service

import (
	"math"
	"sort"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// =============================================================================
// v1.1.0 M2 · B90 成本优化建议
//
// 基于用户近 N 天的逐模型消费聚合（model.GetUserModelUsageSince）与当前倍率
// （ratio_setting），识别「占比高 / 单价高」的模型，并在用户可用模型集合里找
// 更便宜的替代，给出预计可省比例。估算口径：假设 token 构成不变，仅按倍率差
// 折算，不含缓存/音频等特殊计费，属于保守近似。
// =============================================================================

const (
	// CostInsightMaxDays 单次分析的最长回看窗口（天），防止全表扫描。
	CostInsightMaxDays = 90
	// CostInsightDefaultDays 未显式指定窗口时默认回看天数。
	CostInsightDefaultDays = 30
	// CostInsightMinShare 仅对占比不低于该值的模型给出建议，避免长尾噪音。
	CostInsightMinShare = 0.10
	// CostInsightMaxSuggestions 每次最多给出的建议条数。
	CostInsightMaxSuggestions = 3
	// CostInsightMaxAlternatives 每条建议最多列出的替代模型数。
	CostInsightMaxAlternatives = 3
)

// CostInsightModel 单个模型在窗口内的用量与计价信息。
type CostInsightModel struct {
	ModelName        string  `json:"model_name"`
	UsedQuota        int64   `json:"used_quota"`
	QuotaShare       float64 `json:"quota_share"`
	RequestCount     int64   `json:"request_count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	ModelRatio       float64 `json:"model_ratio"`
	CompletionRatio  float64 `json:"completion_ratio"`
	// EffectiveRatio 按「该模型自身 token 构成」折算出的综合倍率，
	// 即 model_ratio * (prompt + completion_ratio * completion) / total_tokens；
	// token 全为 0（按次计费等）时退化为 model_ratio。
	EffectiveRatio float64 `json:"effective_ratio"`
}

// CostInsightAlternative 单个更便宜替代模型的预估收益。
type CostInsightAlternative struct {
	ModelName      string  `json:"model_name"`
	ModelRatio     float64 `json:"model_ratio"`
	EffectiveRatio float64 `json:"effective_ratio"`
	// EstimatedSavingQuota 假设把该高消耗模型的全部流量迁到替代模型，可省额度。
	EstimatedSavingQuota int64 `json:"estimated_saving_quota"`
	// EstimatedSavingPercent = 1 - alt_effective / top_effective，0~1。
	EstimatedSavingPercent float64 `json:"estimated_saving_percent"`
}

// CostInsightSuggestion 一条成本优化建议。
type CostInsightSuggestion struct {
	ModelName    string                   `json:"model_name"`
	ReasonCode   string                   `json:"reason_code"` // cheaper_alternative / dominant_model
	Reason       string                   `json:"reason"`
	Model        CostInsightModel         `json:"model"`
	Alternatives []CostInsightAlternative `json:"alternatives"`
}

// CostInsight 成本优化建议总览。
type CostInsight struct {
	WindowDays     int                     `json:"window_days"`
	GeneratedAt    int64                   `json:"generated_at"`
	TotalUsedQuota int64                   `json:"total_used_quota"`
	TotalRequests  int64                   `json:"total_requests"`
	Models         []CostInsightModel      `json:"models"`
	Suggestions    []CostInsightSuggestion `json:"suggestions"`
}

// NormalizeCostInsightDays 校正回看天数：非法/缺省回 30 天，上限 90 天。
func NormalizeCostInsightDays(days int) int {
	if days <= 0 {
		return CostInsightDefaultDays
	}
	if days > CostInsightMaxDays {
		return CostInsightMaxDays
	}
	return days
}

// BuildCostInsight 生成用户侧成本优化建议。
func BuildCostInsight(userId int, days int) (*CostInsight, error) {
	days = NormalizeCostInsightDays(days)
	since := time.Now().Unix() - int64(days)*86400
	rows, err := model.GetUserModelUsageSince(userId, since)
	if err != nil {
		return nil, err
	}

	insight := &CostInsight{
		WindowDays:  days,
		GeneratedAt: time.Now().Unix(),
		Models:      make([]CostInsightModel, 0, len(rows)),
		Suggestions: make([]CostInsightSuggestion, 0),
	}
	var totalQuota int64
	var totalRequests int64
	for _, row := range rows {
		totalQuota += row.UsedQuota
		totalRequests += row.RequestCount
	}
	insight.TotalUsedQuota = totalQuota
	insight.TotalRequests = totalRequests
	if totalQuota <= 0 || len(rows) == 0 {
		return insight, nil
	}

	usable := userCostInsightModelSet(userId)
	for _, row := range rows {
		m := CostInsightModel{
			ModelName:        row.ModelName,
			UsedQuota:        row.UsedQuota,
			QuotaShare:       float64(row.UsedQuota) / float64(totalQuota),
			RequestCount:     row.RequestCount,
			PromptTokens:     row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
		}
		if ratio, ok, _ := ratio_setting.GetModelRatio(row.ModelName); ok && ratio > 0 {
			m.ModelRatio = ratio
		}
		m.CompletionRatio = ratio_setting.GetCompletionRatio(row.ModelName)
		m.EffectiveRatio = costEffectiveRatio(m.ModelRatio, m.CompletionRatio, row.PromptTokens, row.CompletionTokens)
		insight.Models = append(insight.Models, m)
	}

	// 占比降序，高占比模型优先出建议。
	sort.Slice(insight.Models, func(i, j int) bool {
		if insight.Models[i].UsedQuota != insight.Models[j].UsedQuota {
			return insight.Models[i].UsedQuota > insight.Models[j].UsedQuota
		}
		return insight.Models[i].ModelName < insight.Models[j].ModelName
	})

	for _, m := range insight.Models {
		if m.QuotaShare < CostInsightMinShare || m.EffectiveRatio <= 0 {
			continue
		}
		alternatives := costAlternatives(m, usable)
		if len(alternatives) == 0 {
			continue
		}
		reasonCode := "cheaper_alternative"
		reason := "存在更便宜的可用替代模型"
		if m.QuotaShare >= 0.5 {
			reasonCode = "dominant_model"
			reason = "该模型占近段消耗的比例过高，且存在更便宜的可用替代"
		}
		insight.Suggestions = append(insight.Suggestions, CostInsightSuggestion{
			ModelName:    m.ModelName,
			ReasonCode:   reasonCode,
			Reason:       reason,
			Model:        m,
			Alternatives: alternatives,
		})
		if len(insight.Suggestions) >= CostInsightMaxSuggestions {
			break
		}
	}
	return insight, nil
}

// costAlternatives 在可用模型集合里找比 top 便宜、且当前倍率表里存在的替代模型。
func costAlternatives(top CostInsightModel, usable map[string]struct{}) []CostInsightAlternative {
	candidates := make([]CostInsightAlternative, 0, 4)
	for name := range usable {
		if name == top.ModelName {
			continue
		}
		ratio, ok, _ := ratio_setting.GetModelRatio(name)
		if !ok || ratio <= 0 {
			continue
		}
		// 按次计费模型与倍率计费模型不可直接比价，跳过。
		if _, isPrice, _ := ratio_setting.GetModelRatioOrPrice(name); isPrice {
			continue
		}
		altRatio := ratio_setting.GetCompletionRatio(name)
		altEffective := costEffectiveRatio(ratio, altRatio, top.PromptTokens, top.CompletionTokens)
		if altEffective <= 0 || altEffective >= top.EffectiveRatio {
			continue
		}
		percent := 1 - altEffective/top.EffectiveRatio
		if percent <= 0 {
			continue
		}
		candidates = append(candidates, CostInsightAlternative{
			ModelName:              name,
			ModelRatio:             ratio,
			EffectiveRatio:         altEffective,
			EstimatedSavingQuota:   int64(math.Round(float64(top.UsedQuota) * percent)),
			EstimatedSavingPercent: math.Round(percent*10000) / 10000,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].EffectiveRatio != candidates[j].EffectiveRatio {
			return candidates[i].EffectiveRatio < candidates[j].EffectiveRatio
		}
		return candidates[i].ModelName < candidates[j].ModelName
	})
	if len(candidates) > CostInsightMaxAlternatives {
		candidates = candidates[:CostInsightMaxAlternatives]
	}
	return candidates
}

// costEffectiveRatio 按固定 token 构成折算综合倍率；无 token 信息时退化为 modelRatio。
func costEffectiveRatio(modelRatio, completionRatio float64, promptTokens, completionTokens int64) float64 {
	if modelRatio <= 0 {
		return 0
	}
	total := promptTokens + completionTokens
	if total <= 0 {
		return modelRatio
	}
	return modelRatio * (float64(promptTokens) + completionRatio*float64(completionTokens)) / float64(total)
}

// userCostInsightModelSet 用户可用（分组内启用渠道提供）的模型集合。
// 走 GetUserCache 取用户分组，与 /api/user/models 的口径一致。
func userCostInsightModelSet(userId int) map[string]struct{} {
	usable := make(map[string]struct{}, 32)
	user, err := model.GetUserCache(userId)
	if err != nil || user == nil || user.Group == "" {
		return usable
	}
	groups := GetUserUsableGroups(user.Group)
	groupNames := make([]string, 0, len(groups))
	for name := range groups {
		groupNames = append(groupNames, name)
	}
	for _, name := range GetGroupsEnabledModels(groupNames) {
		usable[name] = struct{}{}
	}
	return usable
}
