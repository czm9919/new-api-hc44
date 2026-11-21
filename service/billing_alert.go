package service

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/prommetrics"
)

// 计费异常告警（v1.1.0 M1 · B71）：扣费为 0、倍率为 0、金额为负、单请求超阈值
// 这四类异常在结算时当场识别并上报。动机：hc346 那类倍率/usage 缺失导致的
// 大面积 0 扣费 bug，不能等用户投诉或对账才发现。
//
// 上报三件套：Prometheus 计数（不看开关）→ 错误日志（逐条）→ root 通知
// （按 kind 每小时限频，B71 通知开关与全局告警限频共用配置）。

const (
	// BillingAnomalyZeroCharge：本次请求没有可计费用量，扣费被强制为 0。
	BillingAnomalyZeroCharge = "zero_charge"
	// BillingAnomalyZeroRatio：按倍率计费的模型倍率/分组倍率为 0，账单金额会被抹零。
	BillingAnomalyZeroRatio = "zero_ratio"
	// BillingAnomalyNegativeCharge：结算金额为负，属于账务错误。
	BillingAnomalyNegativeCharge = "negative_charge"
	// BillingAnomalyOverThreshold：单请求扣费超过阈值，可能是滥用或倍率配错。
	BillingAnomalyOverThreshold = "over_threshold"
	// BillingAnomalyUsageFallback：上游 usage 缺失，按预估算兜底计费（B82）。
	BillingAnomalyUsageFallback = "usage_fallback"
)

// billingAnomalyInput 是一次结算的结果切片，供异常判定使用。
type billingAnomalyInput struct {
	UserId            int
	ChannelId         int
	TokenId           int
	ModelName         string
	Quota             int
	PreConsumedQuota  int
	Billable          bool
	UsePrice          bool
	FixedPriceBilling bool
	ModelRatio        float64
	GroupRatio        float64
}

// checkBillingAnomalies 在结算完成后判定本次扣费是否属于异常，并逐条上报。
// 一次请求最多命中一类（优先级：负数 > 零扣费 > 零倍率 > 超阈值），
// 避免同一次结算触发重复告警。
func checkBillingAnomalies(ctx context.Context, in billingAnomalyInput) {
	var kind, reason string
	switch {
	case in.Quota < 0:
		kind, reason = BillingAnomalyNegativeCharge, "charge is negative"
	case !in.Billable:
		kind, reason = BillingAnomalyZeroCharge, "nothing billable, charge forced to 0"
	case !in.UsePrice && !in.FixedPriceBilling && (in.ModelRatio == 0 || in.GroupRatio == 0):
		kind, reason = BillingAnomalyZeroRatio, "model or group ratio is 0"
	default:
		threshold := int(common.BillingAlertSingleRequestQuota)
		if threshold <= 0 || in.Quota <= threshold {
			return
		}
		kind, reason = BillingAnomalyOverThreshold, "charge above the single-request threshold"
	}

	detail := fmt.Sprintf(
		"billing anomaly [%s]: %s; userId %d, channelId %d, tokenId %d, model %s, quota %d, pre-consumed %d, model ratio %.4f, group ratio %.4f, at %s",
		kind, reason, in.UserId, in.ChannelId, in.TokenId, in.ModelName,
		in.Quota, in.PreConsumedQuota, in.ModelRatio, in.GroupRatio, time.Now().Format(time.RFC3339))
	ReportBillingAnomaly(ctx, kind, detail)
}

// ReportBillingAnomaly 记录一条计费异常：指标 + 错误日志 + 限频 root 通知。
func ReportBillingAnomaly(ctx context.Context, kind string, detail string) {
	prommetrics.BillingAnomalies.WithLabelValues(kind).Inc()
	logAlert(ctx, detail)
	notifyAdminAlert(ctx, "billing:"+kind,
		"Billing anomaly detected: "+kind, detail)
}
