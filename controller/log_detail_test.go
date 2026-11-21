package controller

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func perTokenBreakdown(other map[string]any, promptTokens, completionTokens, quota int) map[string]any {
	log := &model.Log{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		Quota:            quota,
	}
	return buildLogBillingBreakdown(log, other)
}

// mapOf 把 gin.H（= map[string]interface{} 的定义类型）或 map[string]any 归一化。
// 注意：gin.H 是定义类型，直接断言成 map[string]any 会失败。
func mapOf(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case gin.H:
		return map[string]any(m)
	}
	return nil
}

func num(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	}
	return 0
}

func TestBuildLogBillingBreakdownPerToken(t *testing.T) {
	other := map[string]any{
		"model_ratio":      1.25,
		"completion_ratio": 4.0,
		"group_ratio":      0.8,
		"cache_ratio":      0.5,
		"cache_tokens":     100,
	}
	breakdown := perTokenBreakdown(other, 1000, 500, 3000)

	assert.Equal(t, "per_token", breakdown["billing_mode"])
	// billable prompt = 1000 - 100 = 900
	// input  = 900 × 1.25 × 0.8 = 900
	// output = 500 × 4 × 1.25 × 0.8 = 2000
	// cache  = 100 × 0.5 × 1.25 × 0.8 = 50 → 共 2950
	assert.Equal(t, 2950, breakdown["recomputed_quota"])
	assert.Equal(t, 3000-2950, breakdown["difference"])

	components := mapOf(breakdown["components"])
	require.NotNil(t, components)
	assert.Equal(t, 900.0, num(components["billable_prompt_tokens"]))
	assert.Equal(t, 500.0, num(components["completion_tokens"]))
	assert.Equal(t, 100.0, num(components["cache_tokens"]))
	assert.Equal(t, 900.0, num(components["input_quota"]))
	assert.Equal(t, 2000.0, num(components["output_quota"]))
	assert.Equal(t, 50.0, num(components["cache_quota"]))

	// 百万 token 单价：input 1.25×0.8/500000×1e6 = 2 USD；output ×4 = 8；cache 0.5 → 1
	prices := mapOf(breakdown["unit_price_usd_per_million"])
	require.NotNil(t, prices)
	assert.InDelta(t, 2.0, num(prices["input"]), 1e-9)
	assert.InDelta(t, 8.0, num(prices["output"]), 1e-9)
	assert.InDelta(t, 1.0, num(prices["cache"]), 1e-9)
}

func TestBuildLogBillingBreakdownPerCall(t *testing.T) {
	other := map[string]any{
		"model_price": 0.02,
		"group_ratio": 0.8,
	}
	breakdown := perTokenBreakdown(other, 1000, 500, 7500)

	assert.Equal(t, "per_call", breakdown["billing_mode"])
	// 0.02 × 500000 × 0.8 = 8000
	assert.Equal(t, 8000, breakdown["recomputed_quota"])
	assert.Equal(t, 7500-8000, breakdown["difference"])

	components := mapOf(breakdown["components"])
	require.NotNil(t, components)
	assert.Equal(t, 0.02, num(components["per_call_price_usd"]))
}

func TestParseLogBreakdownInputDefaults(t *testing.T) {
	in := parseLogBreakdownInput(map[string]any{})
	assert.Equal(t, 1.0, in.CompletionRatio, "缺失补全倍率默认 1")
	assert.Equal(t, 1.0, in.GroupRatio, "缺失分组倍率默认 1")
	assert.Equal(t, 1.0, in.CacheRatio, "缺失缓存倍率默认 1")
	assert.Equal(t, in.GroupRatio, in.UserGroupRatio, "缺失用户分组倍率沿用分组倍率")
	assert.False(t, in.HasModelPrice, "未配置按次价不算 per_call")
	assert.Equal(t, 0.0, in.ModelPrice)

	// 非正 model_price 不进 per_call 分支
	in = parseLogBreakdownInput(map[string]any{"model_price": -1})
	assert.False(t, in.HasModelPrice)

	// 字符串数值能被解析（ClickHouse some drivers 返回字符串）
	in = parseLogBreakdownInput(map[string]any{"model_ratio": "2.5"})
	assert.Equal(t, 2.5, in.ModelRatio)

	// 缓存 token 超过 prompt 时截断在展示层处理，这里原样保留
	in = parseLogBreakdownInput(map[string]any{"cache_tokens": 5000})
	assert.Equal(t, 5000.0, in.CacheTokens)
}

func TestLatencyPercentiles(t *testing.T) {
	stats := latencyPercentiles([]int{500, 100, 400, 200, 300})
	assert.Equal(t, 5, stats["count"])
	assert.Equal(t, 100, stats["min"])
	assert.Equal(t, 500, stats["max"])
	assert.Equal(t, 300.0, stats["avg"])
	// 最近邻秩法：p50 → 第 3 个 = 300；p75 → 第 4 个 = 400；p90/p95/p99 → 第 5 个 = 500
	assert.Equal(t, 300, stats["p50"])
	assert.Equal(t, 400, stats["p75"])
	assert.Equal(t, 500, stats["p90"])
	assert.Equal(t, 500, stats["p95"])
	assert.Equal(t, 500, stats["p99"])

	// 单样本
	stats = latencyPercentiles([]int{42})
	assert.Equal(t, 1, stats["count"])
	assert.Equal(t, 42, stats["p50"])

	// 空样本
	stats = latencyPercentiles(nil)
	assert.Equal(t, 0, stats["count"])
	_, hasAvg := stats["avg"]
	assert.False(t, hasAvg)
}

func TestOtherFloatTypes(t *testing.T) {
	other := map[string]any{
		"int":    3,
		"int64":  int64(4),
		"float":  5.5,
		"string": "6.25",
		"jnum":   json.Number("7.125"),
		"bad":    struct{}{},
	}
	assert.Equal(t, 3.0, otherFloat(other, "int"))
	assert.Equal(t, 4.0, otherFloat(other, "int64"))
	assert.Equal(t, 5.5, otherFloat(other, "float"))
	assert.Equal(t, 6.25, otherFloat(other, "string"))
	assert.Equal(t, 7.125, otherFloat(other, "jnum"))
	assert.Equal(t, 0.0, otherFloat(other, "bad"))
	assert.Equal(t, 0.0, otherFloat(other, "missing"))
}
