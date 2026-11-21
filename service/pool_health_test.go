package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func channelWith(status int, group string) *model.Channel {
	return &model.Channel{Status: status, Group: group}
}

func TestCollectPoolGroupStats(t *testing.T) {
	channels := []*model.Channel{
		channelWith(common.ChannelStatusEnabled, "default"),
		channelWith(common.ChannelStatusAutoDisabled, "default,vip"),
		channelWith(common.ChannelStatusManuallyDisabled, ""),
	}

	stats := collectPoolGroupStats(channels)

	def := stats["default"]
	if def == nil {
		t.Fatal("default 分组应存在")
	}
	if def.Total != 3 || def.Enabled != 1 || def.AutoDisabled != 1 || def.ManualDisabled != 1 {
		t.Fatalf("default 统计错误: %+v", def)
	}
	vip := stats["vip"]
	if vip == nil || vip.Total != 1 || vip.AutoDisabled != 1 {
		t.Fatalf("多分组渠道应同时计入 vip: %+v", vip)
	}
}

func TestEvaluatePoolHealthDownAndRecover(t *testing.T) {
	prev := map[string]string{}

	// 第一轮：还有一个可用，正常。
	stats := collectPoolGroupStats([]*model.Channel{
		channelWith(common.ChannelStatusEnabled, "default"),
		channelWith(common.ChannelStatusAutoDisabled, "default"),
	})
	next, alerts := evaluatePoolHealth(stats, prev)
	if len(alerts) != 0 {
		t.Fatalf("有可用渠道时不该告警: %+v", alerts)
	}
	prev = next

	// 第二轮：唯一可用的也被自动禁用 → 熔断告警。
	stats = collectPoolGroupStats([]*model.Channel{
		channelWith(common.ChannelStatusAutoDisabled, "default"),
		channelWith(common.ChannelStatusAutoDisabled, "default"),
	})
	next, alerts = evaluatePoolHealth(stats, prev)
	if len(alerts) != 1 || alerts[0].State != "down" {
		t.Fatalf("应产生一条 down 告警，得到 %+v", alerts)
	}
	if !strings.Contains(alerts[0].Subject, "default") {
		t.Fatalf("告警文案应带分组名: %s", alerts[0].Subject)
	}
	prev = next

	// 第三轮：还是全挂，不重复告警。
	_, alerts = evaluatePoolHealth(stats, prev)
	if len(alerts) != 0 {
		t.Fatalf("状态没变化不该重复告警: %+v", alerts)
	}

	// 第四轮：补了新 key → 恢复通知。
	stats = collectPoolGroupStats([]*model.Channel{
		channelWith(common.ChannelStatusEnabled, "default"),
		channelWith(common.ChannelStatusAutoDisabled, "default"),
	})
	_, alerts = evaluatePoolHealth(stats, prev)
	if len(alerts) != 1 || alerts[0].State != "ok" {
		t.Fatalf("恢复时应发一条 ok 通知，得到 %+v", alerts)
	}
	if !strings.Contains(alerts[0].Subject, "恢复") {
		t.Fatalf("恢复文案不对: %s", alerts[0].Subject)
	}
}

func TestEvaluatePoolHealthDegraded(t *testing.T) {
	prev := map[string]string{}

	// 4 个里 3 个自动禁用（75% >= 60%）→ 提前预警。
	stats := collectPoolGroupStats([]*model.Channel{
		channelWith(common.ChannelStatusAutoDisabled, "vip"),
		channelWith(common.ChannelStatusAutoDisabled, "vip"),
		channelWith(common.ChannelStatusAutoDisabled, "vip"),
		channelWith(common.ChannelStatusEnabled, "vip"),
	})
	next, alerts := evaluatePoolHealth(stats, prev)
	if len(alerts) != 1 || alerts[0].State != "degraded" {
		t.Fatalf("应产生一条 degraded 预警，得到 %+v", alerts)
	}

	// 好渠道再挂一个 → 变成 down，也要告警（不是被 degraded 吃掉）。
	stats = collectPoolGroupStats([]*model.Channel{
		channelWith(common.ChannelStatusAutoDisabled, "vip"),
		channelWith(common.ChannelStatusAutoDisabled, "vip"),
		channelWith(common.ChannelStatusAutoDisabled, "vip"),
		channelWith(common.ChannelStatusAutoDisabled, "vip"),
	})
	_, alerts = evaluatePoolHealth(stats, next)
	if len(alerts) != 1 || alerts[0].State != "down" {
		t.Fatalf("全挂时应升级为 down 告警，得到 %+v", alerts)
	}
}

func TestEvaluatePoolHealthIgnoresManualOnly(t *testing.T) {
	// 手动禁用不算事故：管理员主动关的渠道不触发「熔断」。
	stats := collectPoolGroupStats([]*model.Channel{
		channelWith(common.ChannelStatusManuallyDisabled, "default"),
	})
	_, alerts := evaluatePoolHealth(stats, map[string]string{})
	if len(alerts) != 1 || alerts[0].State != "down" {
		t.Fatalf("没有任何启用渠道时仍要告警（含手动禁用全部）: %+v", alerts)
	}
	// 但一个渠道都没有的分组不该进来（stats 为空）。
	if got := collectPoolGroupStats(nil); len(got) != 0 {
		t.Fatalf("空渠道列表应返回空统计: %+v", got)
	}
}

func TestEvaluatePoolHealthDropsDeletedGroup(t *testing.T) {
	prev := map[string]string{"default": "down", "old": "down"}
	stats := collectPoolGroupStats([]*model.Channel{
		channelWith(common.ChannelStatusEnabled, "default"),
	})
	next, _ := evaluatePoolHealth(stats, prev)
	if _, ok := next["old"]; ok {
		t.Fatal("已删除的分组不该留在状态表里")
	}
	if next["default"] != "ok" {
		t.Fatalf("default 应恢复为 ok，得到 %q", next["default"])
	}
}
