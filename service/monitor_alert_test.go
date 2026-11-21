package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/prommetrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedChannelLog(t *testing.T, channelId int, logType int, createdAt int64) {
	t.Helper()
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId:    900,
		Type:      logType,
		ChannelId: channelId,
		CreatedAt: createdAt,
		Quota:     1,
	}).Error)
}

func withMonitorAlertConfig(t *testing.T, mutate func()) {
	t.Helper()
	oldEnabled := common.MonitorAlertEnabled
	oldInterval := common.MonitorAlertIntervalMinutes
	oldRate := common.MonitorChannelErrorRatePercent
	oldMin := common.MonitorChannelMinRequests
	oldDisk := common.MonitorDiskUsedPercent
	oldMemory := common.MonitorMemoryUsedPercent
	oldBilling := common.BillingAlertEnabled
	oldFallback := common.UsageFallbackEnabled
	mutate()
	t.Cleanup(func() {
		common.MonitorAlertEnabled = oldEnabled
		common.MonitorAlertIntervalMinutes = oldInterval
		common.MonitorChannelErrorRatePercent = oldRate
		common.MonitorChannelMinRequests = oldMin
		common.MonitorDiskUsedPercent = oldDisk
		common.MonitorMemoryUsedPercent = oldMemory
		common.BillingAlertEnabled = oldBilling
		common.UsageFallbackEnabled = oldFallback
	})
}

func TestGetChannelErrorStatsCountsConsumeAndError(t *testing.T) {
	truncate(t)
	now := time.Now()
	seedChannelLog(t, 11, model.LogTypeConsume, now.Unix()-60)
	seedChannelLog(t, 11, model.LogTypeConsume, now.Unix()-120)
	seedChannelLog(t, 11, model.LogTypeError, now.Unix()-60)

	stats, err := model.GetChannelErrorStats(now.Unix() - 600)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, 11, stats[0].ChannelId)
	assert.Equal(t, int64(2), stats[0].ConsumeCount)
	assert.Equal(t, int64(1), stats[0].ErrorCount)
}

func TestCheckMonitorAlertsChannelFailureRate(t *testing.T) {
	withMonitorAlertConfig(t, func() {
		common.MonitorAlertEnabled = true
		common.MonitorAlertIntervalMinutes = 15
		common.MonitorChannelErrorRatePercent = 50
		common.MonitorChannelMinRequests = 10
		// 测试只验证巡检判定与指标，不实际发通知。
		common.BillingAlertEnabled = false
	})
	truncate(t)
	now := time.Now()
	inWindow := now.Unix() - 60
	outOfWindow := now.Unix() - int64(30*60)

	// 渠道 21：20 成功 / 30 失败，60% > 50%，达到告警条件。
	for i := 0; i < 20; i++ {
		seedChannelLog(t, 21, model.LogTypeConsume, inWindow)
	}
	for i := 0; i < 30; i++ {
		seedChannelLog(t, 21, model.LogTypeError, inWindow)
	}
	// 渠道 22：请求数不足，不进入判定。
	for i := 0; i < 8; i++ {
		seedChannelLog(t, 22, model.LogTypeConsume, inWindow)
		seedChannelLog(t, 22, model.LogTypeError, inWindow)
	}
	// 渠道 23：失败率只有 20%。
	for i := 0; i < 8; i++ {
		seedChannelLog(t, 23, model.LogTypeConsume, inWindow)
	}
	for i := 0; i < 2; i++ {
		seedChannelLog(t, 23, model.LogTypeError, inWindow)
	}
	// 渠道 25：恰好等于阈值（50%）不告警，阈值语义是"超过"。
	for i := 0; i < 10; i++ {
		seedChannelLog(t, 25, model.LogTypeConsume, inWindow)
		seedChannelLog(t, 25, model.LogTypeError, inWindow)
	}
	// 渠道 24：窗口外的历史失败不计入。
	seedChannelLog(t, 24, model.LogTypeConsume, inWindow)
	for i := 0; i < 5; i++ {
		seedChannelLog(t, 24, model.LogTypeError, outOfWindow)
	}

	before := testutil.ToFloat64(prommetrics.MonitorAlerts.WithLabelValues(MonitorAlertChannelErrorRate))
	checkMonitorAlerts(now)
	after := testutil.ToFloat64(prommetrics.MonitorAlerts.WithLabelValues(MonitorAlertChannelErrorRate))
	assert.Equal(t, before+1, after, "only channel 21 crosses the failure rate threshold")

	// 关闭巡检后不再产生新计数。
	common.MonitorAlertEnabled = false
	checkMonitorAlerts(now)
	assert.Equal(t, after, testutil.ToFloat64(prommetrics.MonitorAlerts.WithLabelValues(MonitorAlertChannelErrorRate)))
}

func TestStartStopMonitorAlertsIsIdempotent(t *testing.T) {
	withMonitorAlertConfig(t, func() {
		common.MonitorAlertEnabled = false
		common.MonitorAlertIntervalMinutes = 1
		common.BillingAlertEnabled = false
	})

	StartMonitorAlerts()
	StartMonitorAlerts() // 第二次调用必须无副作用
	StopMonitorAlerts(5 * time.Second)
	// 重复停止不 panic。
	StopMonitorAlerts(0)
}
