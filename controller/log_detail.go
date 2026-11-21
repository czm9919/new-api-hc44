package controller

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// B60 扣费明细下钻
// ---------------------------------------------------------------------------

// logBreakdownInput 汇集计算扣费明细所需的 other 公共元数据。
// 公共键与 service/log_info_generate.go 的 SetPublic 写入一一对应。
type logBreakdownInput struct {
	ModelRatio      float64
	CompletionRatio float64
	GroupRatio      float64
	UserGroupRatio  float64
	CacheTokens     float64
	CacheRatio      float64
	ModelPrice      float64
	HasModelPrice   bool
}

func parseLogBreakdownInput(other map[string]any) logBreakdownInput {
	in := logBreakdownInput{
		ModelRatio:      otherFloat(other, "model_ratio"),
		CompletionRatio: otherFloat(other, "completion_ratio"),
		GroupRatio:      otherFloat(other, "group_ratio"),
		UserGroupRatio:  otherFloat(other, "user_group_ratio"),
		CacheTokens:     otherFloat(other, "cache_tokens"),
		CacheRatio:      otherFloat(other, "cache_ratio"),
	}
	if v := otherFloat(other, "model_price"); v > 0 {
		in.ModelPrice = v
		in.HasModelPrice = true
	}
	if in.CompletionRatio <= 0 {
		in.CompletionRatio = 1
	}
	if in.CacheRatio <= 0 {
		in.CacheRatio = 1
	}
	if in.GroupRatio <= 0 {
		in.GroupRatio = 1
	}
	if in.UserGroupRatio <= 0 {
		in.UserGroupRatio = in.GroupRatio
	}
	return in
}

func otherFloat(other map[string]any, key string) float64 {
	value, ok := other[key]
	if !ok {
		return 0
	}
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	}
	return 0
}

// buildLogBillingBreakdown 把一笔扣费日志拆成「输入/输出/缓存 × 单价 × 分组倍率」，
// 并用当前倍率重算一遍总额与日志扣费对比（B60）。
// 重算只用于展示解释，不改变任何计费结果。
func buildLogBillingBreakdown(log *model.Log, other map[string]any) gin.H {
	in := parseLogBreakdownInput(other)
	quotaPerUnit := float64(common.QuotaPerUnit)

	promptTokens := float64(log.PromptTokens)
	completionTokens := float64(log.CompletionTokens)
	cacheTokens := math.Min(in.CacheTokens, promptTokens)
	billablePrompt := math.Max(promptTokens-cacheTokens, 0)

	breakdown := gin.H{
		"quota_per_unit": quotaPerUnit,
		"logged_quota":   log.Quota,
		"factors": gin.H{
			"model_ratio":      in.ModelRatio,
			"completion_ratio": in.CompletionRatio,
			"group_ratio":      in.GroupRatio,
			"user_group_ratio": in.UserGroupRatio,
			"cache_tokens":     cacheTokens,
			"cache_ratio":      in.CacheRatio,
			"model_price":      in.ModelPrice,
		},
	}

	if in.HasModelPrice {
		// 按次计费：单次固定价 × 分组倍率（other 里的加权倍率已含在重算误差里说明）。
		recomputed := in.ModelPrice * quotaPerUnit * in.GroupRatio
		breakdown["billing_mode"] = "per_call"
		breakdown["components"] = gin.H{
			"per_call_price_usd": in.ModelPrice,
			"quota":              recomputed,
		}
		breakdown["recomputed_quota"] = int(math.Round(recomputed))
		breakdown["formula"] = "单次价(USD) × 额度单位 × 分组倍率"
	} else {
		// 按量计费：billable prompt + completion×补全倍率 + 缓存读×缓存倍率，再乘模型倍率与分组倍率。
		inputQuota := billablePrompt * in.ModelRatio * in.GroupRatio
		outputQuota := completionTokens * in.CompletionRatio * in.ModelRatio * in.GroupRatio
		cacheQuota := cacheTokens * in.CacheRatio * in.ModelRatio * in.GroupRatio
		recomputed := inputQuota + outputQuota + cacheQuota
		breakdown["billing_mode"] = "per_token"
		breakdown["components"] = gin.H{
			"billable_prompt_tokens": billablePrompt,
			"completion_tokens":      completionTokens,
			"cache_tokens":           cacheTokens,
			"input_quota":            math.Round(inputQuota),
			"output_quota":           math.Round(outputQuota),
			"cache_quota":            math.Round(cacheQuota),
		}
		// 单价（USD / 百万 token）：quota / QuotaPerUnit 换算。
		breakdown["unit_price_usd_per_million"] = gin.H{
			"input":  in.ModelRatio * in.GroupRatio / quotaPerUnit * 1_000_000,
			"output": in.ModelRatio * in.CompletionRatio * in.GroupRatio / quotaPerUnit * 1_000_000,
			"cache":  in.ModelRatio * in.CacheRatio * in.GroupRatio / quotaPerUnit * 1_000_000,
		}
		breakdown["recomputed_quota"] = int(math.Round(recomputed))
		breakdown["formula"] = "(输入 + 输出×补全倍率 + 缓存×缓存倍率) × 模型倍率 × 分组倍率"
	}
	breakdown["difference"] = log.Quota - breakdown["recomputed_quota"].(int)
	return breakdown
}

// GetSelfLogBillingDetail 用户视角的扣费明细下钻：只能看自己的日志，
// other 里的 admin/root 信息一律不透出（复用用户可见性过滤）。
// 支持 ?request_id= 定位（ClickHouse 日志库 id 非稳定自增时的可靠查询方式）。
func GetSelfLogBillingDetail(c *gin.Context) {
	userId := c.GetInt("id")
	var (
		log *model.Log
		err error
	)
	if requestId := c.Query("request_id"); requestId != "" {
		log, err = model.GetUserLogByRequestId(userId, requestId)
	} else {
		var id int
		id, err = strconv.Atoi(c.Param("id"))
		if err != nil || id <= 0 {
			common.ApiError(c, common.NewMessage("invalid log id"))
			return
		}
		log, err = model.GetUserLogById(userId, id)
	}
	if err != nil || log == nil {
		common.ApiError(c, common.NewMessage("日志不存在"))
		return
	}
	other := map[string]any{}
	_ = json.Unmarshal([]byte(log.Other), &other)
	common.ApiSuccess(c, gin.H{
		"log":       log,
		"breakdown": buildLogBillingBreakdown(log, other),
	})
}

// ---------------------------------------------------------------------------
// B114 请求耗时分布
// ---------------------------------------------------------------------------

// LatencyPercentile 百分位（最近邻秩法），输入任意顺序的样本，返回升序切片复用。
func latencyPercentiles(samples []int) gin.H {
	if len(samples) == 0 {
		return gin.H{"count": 0}
	}
	sorted := make([]int, len(samples))
	copy(sorted, samples)
	sort.Ints(sorted)
	pick := func(p float64) int {
		idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		return sorted[idx]
	}
	sum := 0
	for _, v := range sorted {
		sum += v
	}
	return gin.H{
		"count": len(sorted),
		"avg":   float64(sum) / float64(len(sorted)),
		"min":   sorted[0],
		"max":   sorted[len(sorted)-1],
		"p50":   pick(50),
		"p75":   pick(75),
		"p90":   pick(90),
		"p95":   pick(95),
		"p99":   pick(99),
	}
}

// GetSelfLatencyStats 个人视角的请求耗时分布（B114）：
// 默认最近 7 天，样本上限 10000 条，只统计成功计费且 use_time>0 的请求。
func GetSelfLatencyStats(c *gin.Context) {
	userId := c.GetInt("id")
	now := common.GetTimestamp()
	start, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if end <= 0 || end > now {
		end = now
	}
	if start <= 0 {
		start = end - 7*24*3600
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	samples, err := model.GetUserLatencySamples(userId, start, end, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, _ := model.CountUserConsumeLogs(userId, start, end)
	stats := latencyPercentiles(samples)
	stats["window_start"] = start
	stats["window_end"] = end
	stats["total_requests"] = total
	common.ApiSuccess(c, stats)
}
