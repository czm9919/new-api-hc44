// Package prommetrics 暴露 Prometheus 指标（v1.1.0 M1 · B08）。
//
// 只用一个自建 registry，不用 prometheus 默认全局 registry：避免第三方库
// 悄悄注册同名指标造成重复注册 panic，也让 /metrics 的输出面完全可控
// （业务标签只到「路由模板」一级，不带具体 id，避免基数爆炸）。
package prommetrics

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the process-wide metrics registry served by /metrics.
var Registry = prometheus.NewRegistry()

var (
	// HTTPRequestsTotal counts finished HTTP requests by method, route
	// template, and status code.
	HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_http_requests_total",
		Help: "Total number of HTTP requests handled, by method, route template and status code.",
	}, []string{"method", "route", "status"})

	// HTTPRequestDuration observes request latency by method and route.
	HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "newapi_http_request_duration_seconds",
		Help:    "HTTP request latency in seconds, by method and route template.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"method", "route"})

	// UptimeSeconds is the process uptime in seconds.
	UptimeSeconds = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "newapi_uptime_seconds",
		Help: "Process uptime in seconds.",
	}, uptimeSeconds)

	// LogWriterEvents counts async consume-log writer lifecycle events
	// (v1.1.0 M1 · B09). event ∈ enqueued/persisted/degraded_sync/batch_retry/dropped.
	// degraded_sync 或 dropped 持续增长说明日志库跟不上请求量。
	LogWriterEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_log_writer_events_total",
		Help: "Async consume-log writer events, by event type.",
	}, []string{"event"})

	// BillingAnomalies counts suspicious billing outcomes (v1.1.0 M1 · B71).
	// kind ∈ zero_charge / zero_ratio / negative_charge / over_threshold /
	// usage_fallback. 计数本身不依赖告警通知开关：通知可能被限频或关闭，
	// 指标必须始终反映真实发生次数。
	BillingAnomalies = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_billing_anomalies_total",
		Help: "Billing outcomes that warrant an operator look, by anomaly kind.",
	}, []string{"kind"})

	// SystemTaskFailures counts failed scheduled tasks by task type
	// (v1.1.0 M1 · B72). 失败只落库时没人看得到，指标让它在看板上可见。
	SystemTaskFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_system_task_failures_total",
		Help: "Failed scheduled system tasks, by task type.",
	}, []string{"task_type"})

	// MonitorAlerts counts raised infrastructure alerts (v1.1.0 M1 · B73).
	// kind ∈ channel_error_rate / disk / memory. 具体渠道 id 只出现在告警
	// 文本里，不进标签，避免基数随渠道数增长。
	MonitorAlerts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_monitor_alerts_total",
		Help: "Infrastructure alerts raised by the built-in monitor, by kind.",
	}, []string{"kind"})

	// SystemDiskUsedPercent / SystemMemoryUsedPercent expose the samples the
	// overload sentry already collects (v1.1.0 M1 · B73)，无需重复采样。
	SystemDiskUsedPercent = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "newapi_system_disk_used_percent",
		Help: "Disk used percent from the last system monitor sample (0 when unavailable).",
	}, systemDiskUsedPercent)

	SystemMemoryUsedPercent = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "newapi_system_memory_used_percent",
		Help: "Memory used percent from the last system monitor sample (0 when unavailable).",
	}, systemMemoryUsedPercent)
)

func systemDiskUsedPercent() float64 {
	return common.GetSystemStatus().DiskUsage
}

func systemMemoryUsedPercent() float64 {
	return common.GetSystemStatus().MemoryUsage
}

// buildInfoCollector 是 newapi_build_info 的常量 1 gauge，version 标签在
// scrape 时实时读取 common.Version：版本号可能在包 init 之后才由 ldflags /
// embed 兜底确定（main.go 的 applyEmbeddedVersion），collector 保证输出
// 永远是当前生效值，而不是注册时刻的占位值。
type buildInfoCollector struct{}

func (buildInfoCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- buildInfoDesc
}

func (buildInfoCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(buildInfoDesc, prometheus.GaugeValue, 1, common.Version)
}

var buildInfoDesc = prometheus.NewDesc(
	"newapi_build_info",
	"Always 1; the version label carries the running build version.",
	[]string{"version"},
	nil,
)

func init() {
	Registry.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
		buildInfoCollector{},
		UptimeSeconds,
		LogWriterEvents,
		BillingAnomalies,
		SystemTaskFailures,
		MonitorAlerts,
		SystemDiskUsedPercent,
		SystemMemoryUsedPercent,
		dbStatsCollector{},
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

// Handler serves the registry in the Prometheus text exposition format.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{
		// 指标口径偶尔会因为升级而变，不因此让 scrape 全失败。
		ErrorHandling: promhttp.ContinueOnError,
	})
}
