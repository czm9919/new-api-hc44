package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func TestChannelServesGroup(t *testing.T) {
	cases := []struct {
		channelGroup string
		group        string
		want         bool
	}{
		{channelGroup: "accel", group: "accel", want: true},
		{channelGroup: "default,accel", group: "accel", want: true},
		{channelGroup: "accel , vip", group: "accel", want: true},
		{channelGroup: "accelhk", group: "accel", want: false},
		{channelGroup: "default", group: "accel", want: false},
		{channelGroup: "", group: "accel", want: false},
		{channelGroup: "accel", group: "", want: false},
	}
	for _, tc := range cases {
		if got := channelServesGroup(tc.channelGroup, tc.group); got != tc.want {
			t.Fatalf("channelServesGroup(%q, %q) = %v, want %v", tc.channelGroup, tc.group, got, tc.want)
		}
	}
}

func TestCountChannelModels(t *testing.T) {
	cases := []struct {
		models string
		want   int
	}{
		{models: "", want: 0},
		{models: "all", want: 0},
		{models: "gpt-4o-mini", want: 1},
		{models: "gpt-4o-mini, claude-3", want: 2},
		{models: "gpt-4o-mini, ,claude-3 ,", want: 2},
	}
	for _, tc := range cases {
		if got := countChannelModels(tc.models); got != tc.want {
			t.Fatalf("countChannelModels(%q) = %d, want %d", tc.models, got, tc.want)
		}
	}
}

func TestSummarizeAccelLines(t *testing.T) {
	lines := []AccelLine{
		{ChannelId: 1, Status: 1, Degraded: false},
		{ChannelId: 2, Status: 1, Degraded: true, CoolingDown: true},
		{ChannelId: 3, Status: 2},
	}
	summary := SummarizeAccelLines(lines)
	if summary.Total != 3 || summary.Available != 2 || summary.Degraded != 1 || summary.CoolingDown != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.AllDown {
		t.Fatal("AllDown must be false when at least one channel is enabled")
	}

	down := SummarizeAccelLines([]AccelLine{{ChannelId: 9, Status: 3}})
	if !down.AllDown {
		t.Fatal("AllDown must be true when every channel is disabled")
	}
	// 分组下一条线路都没有时不算「全挂」：这是配置问题，不是故障。
	if empty := SummarizeAccelLines(nil); empty.AllDown || empty.Total != 0 {
		t.Fatalf("empty lines must not be reported as all down: %+v", empty)
	}
}

// 线路聚合是「每条 relay 请求之外的只读快照」，这里只验证分组过滤与降级阈值，
// 不依赖真实渠道表：直接替换内存缓存并打开内存缓存开关（否则会走 DB 兜底），
// 跑完还原。
func TestCollectAccelLinesFiltersGroupAndMarksDegraded(t *testing.T) {
	prev := channelsIDM
	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	defer func() {
		channelsIDM = prev
		common.MemoryCacheEnabled = prevCache
	}()

	channelsIDM = map[int]*Channel{
		1: {Id: 1, Name: "hk-01", Group: "accel", Status: 1, Models: "gpt-4o-mini"},
		2: {Id: 2, Name: "sg-01", Group: "default", Status: 1, Models: "all"},
		3: {Id: 3, Name: "jp-01", Group: "accel,default", Status: 2},
	}

	lines := CollectAccelLines("accel", time.Now(), 50, 5)
	if len(lines) != 2 {
		t.Fatalf("expected 2 accel lines, got %d: %+v", len(lines), lines)
	}
	// 启用中的排前面，禁用的排后面。
	if lines[0].ChannelId != 1 || lines[1].ChannelId != 3 {
		t.Fatalf("unexpected order: %+v", lines)
	}
	if lines[0].Name != "hk-01" || lines[0].Models != 1 || lines[0].StatusText != "enabled" {
		t.Fatalf("unexpected line fields: %+v", lines[0])
	}
	if lines[1].StatusText != "disabled" {
		t.Fatalf("expected disabled status text, got %q", lines[1].StatusText)
	}
	// 无样本的线路不能被误判为异常（否则新上线线路一上来就标红）。
	if lines[0].Degraded {
		t.Fatal("a channel without samples must not be marked degraded")
	}
	// 空分组必须返回空切片而不是 nil，避免前端拿到 null 崩在 .length 上。
	if got := CollectAccelLines("", time.Now(), 50, 5); got == nil || len(got) != 0 {
		t.Fatalf("empty group must return empty slice, got %#v", got)
	}
}
