package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/prommetrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestAlertThrottleAllowsWindowQuota(t *testing.T) {
	var throttle alertThrottle
	base := time.Now()

	for i := 0; i < 5; i++ {
		assert.True(t, throttle.allow("billing:zero_charge", 5, base), "call %d should pass", i)
	}
	// 配额用尽后窗口内其余调用全部静默。
	assert.False(t, throttle.allow("billing:zero_charge", 5, base.Add(time.Minute)))
	// 不同 kind 的配额相互独立。
	assert.True(t, throttle.allow("billing:zero_ratio", 5, base))
	// 窗口滑出后配额恢复。
	assert.True(t, throttle.allow("billing:zero_charge", 5, base.Add(time.Hour+time.Second)))
	// limit <= 0 表示关闭通知。
	assert.False(t, throttle.allow("billing:zero_charge", 0, base))
}

func TestCheckBillingAnomaliesClassifications(t *testing.T) {
	// 测试只验证指标计数与分类，不触发通知。
	oldEnabled := common.BillingAlertEnabled
	common.BillingAlertEnabled = false
	t.Cleanup(func() { common.BillingAlertEnabled = oldEnabled })

	base := billingAnomalyInput{
		UserId:     1,
		ChannelId:  2,
		TokenId:    3,
		ModelName:  "gpt-x",
		Quota:      500,
		Billable:   true,
		ModelRatio: 1,
		GroupRatio: 1,
	}

	cases := []struct {
		name   string
		mutate func(*billingAnomalyInput)
		kind   string
	}{
		{name: "negative charge", mutate: func(in *billingAnomalyInput) { in.Quota = -5 }, kind: BillingAnomalyNegativeCharge},
		{name: "zero charge", mutate: func(in *billingAnomalyInput) { in.Quota = 0; in.Billable = false }, kind: BillingAnomalyZeroCharge},
		{name: "zero model ratio", mutate: func(in *billingAnomalyInput) { in.ModelRatio = 0 }, kind: BillingAnomalyZeroRatio},
		{name: "zero group ratio", mutate: func(in *billingAnomalyInput) { in.GroupRatio = 0 }, kind: BillingAnomalyZeroRatio},
		{name: "over threshold", mutate: func(in *billingAnomalyInput) { in.Quota = int(common.BillingAlertSingleRequestQuota) + 1 }, kind: BillingAnomalyOverThreshold},
		{name: "normal charge", mutate: func(in *billingAnomalyInput) {}, kind: ""},
		{name: "zero ratio skipped for fixed price", mutate: func(in *billingAnomalyInput) { in.ModelRatio = 0; in.FixedPriceBilling = true }, kind: ""},
		{name: "zero ratio skipped for use price", mutate: func(in *billingAnomalyInput) { in.ModelRatio = 0; in.UsePrice = true }, kind: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mutate(&in)
			// 计数器是进程级共享的，记录子测试前的值，只断言增量。
			kinds := []string{
				BillingAnomalyZeroCharge, BillingAnomalyZeroRatio,
				BillingAnomalyNegativeCharge, BillingAnomalyOverThreshold,
			}
			before := make(map[string]float64, len(kinds))
			for _, kind := range kinds {
				before[kind] = testutil.ToFloat64(prommetrics.BillingAnomalies.WithLabelValues(kind))
			}
			checkBillingAnomalies(context.Background(), in)
			for _, kind := range kinds {
				want := before[kind]
				if tc.kind != "" && kind == tc.kind {
					want = before[kind] + 1
				}
				assert.Equal(t, want, testutil.ToFloat64(prommetrics.BillingAnomalies.WithLabelValues(kind)), kind)
			}
		})
	}
}
