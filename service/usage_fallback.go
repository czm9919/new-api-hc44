package service

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/prommetrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// 上游 usage 缺失兜底（v1.1.0 M1 · B82）：
// 上游明确返回了 usage 但所有参与计费的 token 计数都是 0 时（典型的上游
// 超时/截断，hc346 的根因之一），整单会被按 0 扣费。开启兜底后用请求侧
// 已经预估算的 prompt tokens 计费，completion 侧不做估算——central 结算处
// 拿不到响应文本，造假数只会让对账更乱；残余的零扣费由 B71 zero_charge
// 告警兜住。
//
// 上游完全没返回 usage（nil）时不介入：结算路径本就按同一估算值兜底，
// 在这里改写反而会把 tiered 结算的判定路径带偏。

// usageHasBillableTokens 报告 usage 是否携带任何参与计费的 token 计数。
func usageHasBillableTokens(usage *dto.Usage) bool {
	if usage == nil {
		return false
	}
	if usage.PromptTokens > 0 || usage.CompletionTokens > 0 ||
		usage.InputTokens > 0 || usage.OutputTokens > 0 {
		return true
	}
	details := usage.PromptTokensDetails
	if details.CachedTokens > 0 || details.CachedCreationTokens > 0 ||
		details.CacheWriteTokens > 0 || details.ImageTokens > 0 || details.AudioTokens > 0 {
		return true
	}
	if usage.ClaudeCacheCreation5mTokens > 0 || usage.ClaudeCacheCreation1hTokens > 0 {
		return true
	}
	if usage.CompletionTokenDetails.AudioTokens > 0 {
		return true
	}
	return false
}

// applyUsageFallback 把全零 usage 改写成按估算 prompt tokens 计费的 usage，
// 返回改写结果与是否发生了兜底。改写会清掉 BillingUsage 快照：它指向的
// 原始 payload 也是零，留着会让 effectiveBillingUsage 绕回零 usage。
func applyUsageFallback(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) (*dto.Usage, bool) {
	if !common.UsageFallbackEnabled || usage == nil {
		return usage, false
	}
	if usageHasBillableTokens(usage) {
		return usage, false
	}
	estimate := relayInfo.GetEstimatePromptTokens()
	if estimate <= 0 {
		return usage, false
	}
	fallback := *usage
	fallback.BillingUsage = nil
	fallback.PromptTokens = estimate
	fallback.CompletionTokens = 0
	fallback.TotalTokens = estimate
	return &fallback, true
}

// reportUsageFallback 在日志里标注本次兜底，并计入异常指标（不发通知：
// 兜底本身是正常降级，通知留给真正被抹零的场景）。
func reportUsageFallback(ctx context.Context, other *model.LogOther, fallbackPromptTokens int) {
	prommetrics.BillingAnomalies.WithLabelValues(BillingAnomalyUsageFallback).Inc()
	if other != nil {
		other.SetAdmin("usage_fallback", true)
		other.SetAdmin("usage_fallback_prompt_tokens", fallbackPromptTokens)
	}
	logAlert(ctx, fmt.Sprintf(
		"usage fallback applied: upstream returned no token counts, billed an estimated %d prompt tokens", fallbackPromptTokens))
}
