package model

import (
	"sort"
	"strings"
	"time"
)

// ============================================================================
// 渠道健康总览（hc35 · B13）
// ============================================================================
//
// 渠道/模型健康记忆原本只服务于智能路由（channel_health.go），运营台看不到。
// 本文件把它以只读快照的形式暴露出来：
//   - 渠道级：谁在持续失败、谁在冷却、谁还没被调用过；
//   - 模型级：auto 智能选型正在使用哪些 (group, model) 健康信号。
//
// 纯读：不写库、不修改路由状态；进程重启后记忆冷启动，与路由行为一致。
// ============================================================================

// ChannelHealthRow 渠道健康单行（运营台展示用，JSON 键为 snake_case）。
type ChannelHealthRow struct {
	ChannelId   int     `json:"channel_id"`
	Name        string  `json:"name"`
	Type        int     `json:"type"`
	Status      int     `json:"status"`
	Tag         string  `json:"tag"`
	Group       string  `json:"group"`
	Priority    int64   `json:"priority"`
	Weight      int     `json:"weight"`
	HasSamples  bool    `json:"has_samples"`
	SuccessRate float64 `json:"success_rate"`
	Samples     float64 `json:"samples"`
	TtftMs      float64 `json:"ttft_ms"`
	LatencyMs   float64 `json:"latency_ms"`
	CoolingDown bool    `json:"cooling_down"`
	ActiveCount int64   `json:"active_count"`
}

// CollectChannelHealthOverview 收集全部渠道的健康快照。
//
// 排序：有样本的渠道在前，按成功率升序（持续失败的渠道置顶，运营先看到
// 需要处理的）；同成功率按样本数降序（样本多的更可信）。无样本渠道排在
// 末尾按 id 升序，方便确认「哪些渠道还没被流量覆盖到」。
func CollectChannelHealthOverview(now time.Time) []ChannelHealthRow {
	channelSyncLock.RLock()
	rows := make([]ChannelHealthRow, 0, len(channelsIDM))
	for id, channel := range channelsIDM {
		if channel == nil {
			continue
		}
		snapshot := GetChannelHealthSnapshot(id, now)
		row := ChannelHealthRow{
			ChannelId:   channel.Id,
			Name:        channel.Name,
			Type:        channel.Type,
			Status:      channel.Status,
			Group:       channel.Group,
			Priority:    channel.GetPriority(),
			Weight:      channel.GetWeight(),
			HasSamples:  snapshot.Samples > 0,
			SuccessRate: snapshot.SuccessRate,
			Samples:     snapshot.Samples,
			TtftMs:      snapshot.TtftMs,
			LatencyMs:   snapshot.LatencyMs,
			CoolingDown: snapshot.CoolingDown,
			ActiveCount: GetChannelActiveCount(id),
		}
		if channel.Tag != nil {
			row.Tag = *channel.Tag
		}
		rows = append(rows, row)
	}
	channelSyncLock.RUnlock()

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.HasSamples != b.HasSamples {
			return a.HasSamples
		}
		if a.HasSamples {
			if a.SuccessRate != b.SuccessRate {
				return a.SuccessRate < b.SuccessRate
			}
			if a.Samples != b.Samples {
				return a.Samples > b.Samples
			}
		}
		return a.ChannelId < b.ChannelId
	})
	return rows
}

// ModelHealthRow 模型级健康单行（key: group|model，auto 智能选型的信号源）。
type ModelHealthRow struct {
	Group       string  `json:"group"`
	Model       string  `json:"model"`
	SuccessRate float64 `json:"success_rate"`
	Samples     float64 `json:"samples"`
	TtftMs      float64 `json:"ttft_ms"`
}

// CollectModelHealthOverview 收集全部模型级健康记忆。
// 排序：样本数降序（信号最强的在前），同样本按成功率升序（问题模型靠前）。
func CollectModelHealthOverview(now time.Time) []ModelHealthRow {
	rows := make([]ModelHealthRow, 0, 16)
	modelHealthStore.Range(func(key, value any) bool {
		rawKey, ok := key.(string)
		if !ok {
			return true
		}
		group, modelName, found := strings.Cut(rawKey, "|")
		if !found || modelName == "" {
			return true
		}
		entry, ok := value.(*channelHealthEntry)
		if !ok {
			return true
		}
		entry.mu.Lock()
		channelHealthDecay(entry, now)
		succ := entry.succWeight
		row := ModelHealthRow{
			Group:   group,
			Model:   modelName,
			Samples: succ + entry.failWeight,
			TtftMs:  entry.ewmaTtftMs,
		}
		entry.mu.Unlock()
		if row.Samples > 0 {
			row.SuccessRate = succ / row.Samples
		}
		rows = append(rows, row)
		return true
	})

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Samples != b.Samples {
			return a.Samples > b.Samples
		}
		if a.SuccessRate != b.SuccessRate {
			return a.SuccessRate < b.SuccessRate
		}
		return a.Group+a.Model < b.Group+b.Model
	})
	return rows
}
