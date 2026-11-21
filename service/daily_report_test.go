package service

import (
	"testing"
	"time"
)

// 每日战报的时间窗口最容易出错的地方：跨天、错过窗口、同一天重复发送。
// 这里只测纯函数，不碰数据库。
func TestShouldSendDailyReport(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, 11, day, hour, minute, 0, 0, loc)
	}

	cases := []struct {
		name     string
		now      time.Time
		lastDate string
		want     bool
	}{
		{"到点前不发", at(21, 8, 59), "", false},
		{"到点发", at(21, 9, 0), "", true},
		{"宽限期内发", at(21, 11, 30), "", true},
		{"超过宽限期不补发", at(21, 12, 1), "", false},
		{"当天已发过不重复", at(21, 9, 30), "2026-11-21", false},
		{"昨天发过今天照发", at(21, 9, 30), "2026-11-20", true},
		{"半夜不发", at(22, 0, 5), "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldSendDailyReport(tc.now, 9, 0, tc.lastDate); got != tc.want {
				t.Fatalf("shouldSendDailyReport(%s, last=%q) = %v, want %v",
					tc.now.Format("2006-01-02 15:04"), tc.lastDate, got, tc.want)
			}
		})
	}
}

func TestShouldSendDailyReportCustomMinute(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 11, 21, 9, 29, 59, 0, loc)
	if shouldSendDailyReport(now, 9, 30, "") {
		t.Fatal("9:29:59 不该在 9:30 的战报窗口内")
	}
	now = time.Date(2026, 11, 21, 9, 30, 0, 0, loc)
	if !shouldSendDailyReport(now, 9, 30, "") {
		t.Fatal("9:30:00 应该触发")
	}
}

func TestFormatQuotaCount(t *testing.T) {
	cases := map[int64]string{
		0:          "0",
		999:        "999",
		1000:       "1,000",
		1234567:    "1,234,567",
		1000000000: "1,000,000,000",
	}
	for in, want := range cases {
		if got := formatQuotaCount(in); got != want {
			t.Fatalf("formatQuotaCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestAvgQuota(t *testing.T) {
	if got := avgQuota(100, 0); got != 0 {
		t.Fatalf("无请求时均值应为 0，得到 %d", got)
	}
	if got := avgQuota(101, 2); got != 50 {
		t.Fatalf("101/2 应向下取整为 50，得到 %d", got)
	}
}
