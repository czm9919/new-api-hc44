package model

import (
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
)

// ============================================================================
// 加速线路可见性（hc50 · P2）
// ============================================================================
//
// 「线路」不是新概念：加速分组下的每个渠道就是一条出口线路，new-api 原生
// 已经按优先级/权重在它们之间选路，并自带失败重试与自动禁用。这一层只做
// **只读聚合**：把渠道状态与智能路由的健康记忆拼成人能看懂的一行。
//
// 为什么不让用户查 /api/channel：渠道里带 base_url、密钥、代理地址，
// 泄露等于把上游出口拱手让人。这里只取名称、状态、优先级和健康度，
// 用户能判断「我这条线是不是好的」，但拿不到任何可滥用信息。
// ============================================================================

// AccelLine 一条加速线路（渠道）的对外只读视图。
type AccelLine struct {
	ChannelId   int     `json:"channel_id"`
	Name        string  `json:"name"`
	Status      int     `json:"status"`
	StatusText  string  `json:"status_text"`
	Priority    int64   `json:"priority"`
	Weight      int     `json:"weight"`
	Models      int     `json:"models"`
	HasSamples  bool    `json:"has_samples"`
	SuccessRate float64 `json:"success_rate"`
	Samples     float64 `json:"samples"`
	TtftMs      float64 `json:"ttft_ms"`
	LatencyMs   float64 `json:"latency_ms"`
	CoolingDown bool    `json:"cooling_down"`
	Active      int64   `json:"active"`
	Degraded    bool    `json:"degraded"`
}

// AccelLineSummary 线路总览。AllDown 供前端提示「当前线路维护中」，
// 避免用户对着 429/503 猜是不是自己令牌配错了。
type AccelLineSummary struct {
	Total       int  `json:"total"`
	Available   int  `json:"available"`
	Degraded    int  `json:"degraded"`
	CoolingDown int  `json:"cooling_down"`
	AllDown     bool `json:"all_down"`
}

// accelChannelStatusText 把渠道状态翻译成前端可直接展示的短标签。
func accelChannelStatusText(status int) string {
	switch status {
	case 1:
		return "enabled"
	case 2:
		return "disabled"
	case 3:
		return "auto_disabled"
	default:
		return "unknown"
	}
}

// channelServesGroup 判断渠道是否属于某分组（channel.Group 是逗号分隔的多分组）。
func channelServesGroup(channelGroup string, group string) bool {
	if group == "" {
		return false
	}
	for _, item := range strings.Split(channelGroup, ",") {
		if strings.TrimSpace(item) == group {
			return true
		}
	}
	return false
}

// countChannelModels 渠道覆盖的模型数（"all" 或空视为未知，返回 0）。
func countChannelModels(models string) int {
	trimmed := strings.TrimSpace(models)
	if trimmed == "" || trimmed == "all" {
		return 0
	}
	count := 0
	for _, item := range strings.Split(trimmed, ",") {
		if strings.TrimSpace(item) != "" {
			count++
		}
	}
	return count
}

// CollectAccelLines 收集某加速分组下的全部线路（含已禁用，管理员要能看见）。
//
// degradedRate 的成功率阈值是百分比（50 = 50%），样本不足时不算异常：
// 刚上线的线路第一次调用失败就标红，只会制造噪音。
func CollectAccelLines(group string, now time.Time, degradedRate int, minSamples float64) []AccelLine {
	if group == "" {
		return []AccelLine{}
	}
	if minSamples <= 0 {
		minSamples = 5
	}
	threshold := float64(degradedRate) / 100

	var candidateChannels []*Channel
	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		candidateChannels = make([]*Channel, 0, len(channelsIDM))
		for _, channel := range channelsIDM {
			if channel != nil {
				candidateChannels = append(candidateChannels, channel)
			}
		}
		channelSyncLock.RUnlock()
	} else {
		// 内存缓存关闭时 channelsIDM 是空的，必须直查 DB，
		// 否则线路页在 MEMORY_CACHE_ENABLED != true 的部署上永远空白。
		// group 是逗号分隔串没法精确匹配，取全量后在内存里过滤（渠道量级有限）。
		if err := DB.Find(&candidateChannels).Error; err != nil {
			logger.LogError(nil, "CollectAccelLines: load channels from db failed: "+err.Error())
			candidateChannels = nil
		}
	}

	lines := make([]AccelLine, 0, 4)
	for _, channel := range candidateChannels {
		if channel == nil || !channelServesGroup(channel.Group, group) {
			continue
		}
		snapshot := GetChannelHealthSnapshot(channel.Id, now)
		line := AccelLine{
			ChannelId:   channel.Id,
			Name:        channel.Name,
			Status:      channel.Status,
			StatusText:  accelChannelStatusText(channel.Status),
			Priority:    channel.GetPriority(),
			Weight:      channel.GetWeight(),
			Models:      countChannelModels(channel.Models),
			HasSamples:  snapshot.Samples > 0,
			SuccessRate: snapshot.SuccessRate,
			Samples:     snapshot.Samples,
			TtftMs:      snapshot.TtftMs,
			LatencyMs:   snapshot.LatencyMs,
			CoolingDown: snapshot.CoolingDown,
			Active:      GetChannelActiveCount(channel.Id),
		}
		line.Degraded = channel.Status == 1 &&
			snapshot.Samples >= minSamples &&
			snapshot.SuccessRate < threshold
		lines = append(lines, line)
	}

	// 排序：启用中的排前面（用户先看到自己能用的），其次按成功率降序，
	// 同理再按优先级降序；最后按 id 保证顺序稳定（前端不做二次排序）。
	sort.SliceStable(lines, func(i, j int) bool {
		a, b := lines[i], lines[j]
		aUsable := a.Status == 1 && !a.CoolingDown
		bUsable := b.Status == 1 && !b.CoolingDown
		if aUsable != bUsable {
			return aUsable
		}
		if a.HasSamples != b.HasSamples {
			return a.HasSamples
		}
		if a.HasSamples && a.SuccessRate != b.SuccessRate {
			return a.SuccessRate > b.SuccessRate
		}
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		return a.ChannelId < b.ChannelId
	})
	return lines
}

// SummarizeAccelLines 汇总线路状态。
func SummarizeAccelLines(lines []AccelLine) AccelLineSummary {
	summary := AccelLineSummary{Total: len(lines)}
	for _, line := range lines {
		usable := line.Status == 1
		if usable {
			summary.Available++
		}
		if line.Degraded {
			summary.Degraded++
		}
		if line.CoolingDown {
			summary.CoolingDown++
		}
	}
	summary.AllDown = summary.Total > 0 && summary.Available == 0
	return summary
}
