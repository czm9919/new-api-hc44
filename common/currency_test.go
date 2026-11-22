package common

import (
	"math"
	"testing"
)

func withQuotaPerUnit(t *testing.T, value float64) {
	t.Helper()
	previous := QuotaPerUnit
	QuotaPerUnit = value
	t.Cleanup(func() { QuotaPerUnit = previous })
}

func TestQuotaToUSD(t *testing.T) {
	withQuotaPerUnit(t, 500000)
	cases := []struct {
		quota int64
		want  float64
	}{
		{quota: 0, want: 0},
		{quota: 5000, want: 0.01},
		{quota: 250000, want: 0.5},
		{quota: 500000, want: 1},
		{quota: 1000000, want: 2},
	}
	for _, tc := range cases {
		if got := QuotaToUSD(tc.quota); math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("QuotaToUSD(%d) = %v, want %v", tc.quota, got, tc.want)
		}
	}
}

func TestUSDToQuota(t *testing.T) {
	withQuotaPerUnit(t, 500000)
	cases := []struct {
		usd  float64
		want int64
	}{
		{usd: 0, want: 0},
		{usd: -1, want: 0},
		{usd: 0.01, want: 5000},
		{usd: 0.5, want: 250000},
		{usd: 1, want: 500000},
		{usd: 2, want: 1000000},
	}
	for _, tc := range cases {
		if got := USDToQuota(tc.usd); got != tc.want {
			t.Fatalf("USDToQuota(%v) = %d, want %d", tc.usd, got, tc.want)
		}
	}
}

// 往返换算必须稳定：管理端填 $0.01 → 存 5000 额度 → 回显 $0.01。
func TestUSDQuotaRoundTrip(t *testing.T) {
	withQuotaPerUnit(t, 500000)
	for _, usd := range []float64{0.01, 0.5, 1, 1.37, 2} {
		quota := USDToQuota(usd)
		back := RoundUSD(QuotaToUSD(quota))
		if math.Abs(back-usd) > 1e-4 {
			t.Fatalf("round trip %v -> %d -> %v", usd, quota, back)
		}
	}
}

func TestQuotaToUSDInvalidUnit(t *testing.T) {
	withQuotaPerUnit(t, 0)
	if got := QuotaToUSD(500000); got != 0 {
		t.Fatalf("QuotaToUSD with zero unit = %v, want 0", got)
	}
	if got := USDToQuota(1); got != 0 {
		t.Fatalf("USDToQuota with zero unit = %d, want 0", got)
	}
}
