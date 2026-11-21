package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/prommetrics"
	"github.com/bytedance/gopkg/util/gopool"
)

// 基础监控告警（v1.1.0 M1 · B73）：渠道失败率 / 磁盘 / 内存。
// 与 B08 的 /metrics 分工：指标供面板与阈值告警规则，这里负责在没有外部
// 告警系统时也能把明显故障推给 root（自托管部署的常态）。

const (
	// MonitorAlertChannelErrorRate：渠道在巡检窗口内的失败率超阈值。
	MonitorAlertChannelErrorRate = "channel_error_rate"
	// MonitorAlertDisk：磁盘使用率超阈值。
	MonitorAlertDisk = "disk"
	// MonitorAlertMemory：内存使用率超阈值。
	MonitorAlertMemory = "memory"

	// 系统采样超过该时长未刷新视为监控未运行（与 common 的采样周期匹配），
	// 此时磁盘/内存判断跳过，避免把 0 值当成"很健康"或误报。
	monitorSampleMaxAge = 2 * time.Minute
)

var (
	monitorAlertMu   sync.Mutex
	monitorAlertStop chan struct{}
	monitorAlertDone chan struct{}
)

// StartMonitorAlerts 启动基础监控巡检协程。重复调用无副作用；
// 巡检周期在每次循环重新读取配置，运行期改配置无需重启。
func StartMonitorAlerts() {
	monitorAlertMu.Lock()
	defer monitorAlertMu.Unlock()
	if monitorAlertStop != nil {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	monitorAlertStop = stop
	monitorAlertDone = done

	gopool.Go(func() {
		defer close(done)
		for {
			interval := monitorAlertInterval()
			select {
			case <-stop:
				return
			case <-time.After(interval):
			}
			checkMonitorAlerts(time.Now())
		}
	})
}

// StopMonitorAlerts 停止巡检并最多等待 timeout。未启动时立即返回。
func StopMonitorAlerts(timeout time.Duration) {
	monitorAlertMu.Lock()
	stop, done := monitorAlertStop, monitorAlertDone
	monitorAlertStop, monitorAlertDone = nil, nil
	monitorAlertMu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	if done == nil {
		return
	}
	if timeout <= 0 {
		<-done
		return
	}
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func monitorAlertInterval() time.Duration {
	minutes := common.MonitorAlertIntervalMinutes
	if minutes <= 0 {
		minutes = 15
	}
	return time.Duration(minutes) * time.Minute
}

// checkMonitorAlerts 执行一轮巡检。窗口长度与巡检周期一致，
// 保证相邻两轮不重叠也不留空档。
func checkMonitorAlerts(now time.Time) {
	if !common.MonitorAlertEnabled {
		return
	}
	checkChannelErrorRates(now)
	checkSystemResourceUsage(now)
}

func checkChannelErrorRates(now time.Time) {
	threshold := common.MonitorChannelErrorRatePercent
	if threshold <= 0 {
		return
	}
	minRequests := common.MonitorChannelMinRequests
	if minRequests < 1 {
		minRequests = 1
	}
	since := now.Add(-monitorAlertInterval()).Unix()
	stats, err := model.GetChannelErrorStats(since)
	if err != nil {
		logAlert(nil, common.LogText("monitor alert: failed to query channel error stats: %s", err.Error()))
		return
	}
	for _, stat := range stats {
		total := stat.ConsumeCount + stat.ErrorCount
		if total < int64(minRequests) || stat.ErrorCount == 0 {
			continue
		}
		rate := float64(stat.ErrorCount) * 100 / float64(total)
		if rate <= float64(threshold) {
			continue
		}
		prommetrics.MonitorAlerts.WithLabelValues(MonitorAlertChannelErrorRate).Inc()
		detail := fmt.Sprintf(
			"channel failure rate alert: channelId %d failed %d of %d requests (%.1f%%) in the last %d minutes, threshold %d%%; at %s",
			stat.ChannelId, stat.ErrorCount, total, rate, common.MonitorAlertIntervalMinutes, threshold, now.Format(time.RFC3339))
		logAlert(nil, detail)
		notifyAdminAlert(nil, fmt.Sprintf("monitor:%s:%d", MonitorAlertChannelErrorRate, stat.ChannelId),
			fmt.Sprintf("Channel %d failure rate %.1f%%", stat.ChannelId, rate), detail)
	}
}

func checkSystemResourceUsage(now time.Time) {
	status := common.GetSystemStatus()
	if status.SampledAt.IsZero() || now.Sub(status.SampledAt) > monitorSampleMaxAge {
		return
	}
	// 采样为 0 说明该项未采到（监控关闭或容器口径不可用），不做判断。
	if threshold := common.MonitorDiskUsedPercent; threshold > 0 && status.DiskUsage > 0 && status.DiskUsage > float64(threshold) {
		reportResourceAlert(MonitorAlertDisk, "disk", status.DiskUsage, threshold, now)
	}
	if threshold := common.MonitorMemoryUsedPercent; threshold > 0 && status.MemoryUsage > 0 && status.MemoryUsage > float64(threshold) {
		reportResourceAlert(MonitorAlertMemory, "memory", status.MemoryUsage, threshold, now)
	}
}

func reportResourceAlert(kind string, resource string, usage float64, threshold int, now time.Time) {
	prommetrics.MonitorAlerts.WithLabelValues(kind).Inc()
	detail := fmt.Sprintf("%s usage alert: %.1f%% used, threshold %d%%; at %s",
		resource, usage, threshold, now.Format(time.RFC3339))
	logAlert(nil, detail)
	notifyAdminAlert(nil, "monitor:"+kind,
		fmt.Sprintf("%s usage %.1f%%", resource, usage), detail)
}
