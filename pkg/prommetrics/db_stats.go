// DB 连接池指标（v1.1.0 M2 · B138）。
//
// prommetrics 不反向 import model（会成环），所以由 model 层在启动时通过
// SetDBStatsProvider 注入快照函数；scrape 时若尚未注入（比如单测环境没有
// 初始化数据库），collector 不产出任何样本。
package prommetrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// DBStatsSnapshot 是一个数据库连接池的瞬时状态。
// Kind 取 main（业务库）或 log（日志库），二者可能是不同的实例。
type DBStatsSnapshot struct {
	Kind        string
	MaxOpen     int64
	Open        int64
	InUse       int64
	Idle        int64
	WaitCount   int64
	WaitSeconds float64
}

var dbStatsProvider func() []DBStatsSnapshot

// SetDBStatsProvider 注册连接池快照函数；由 model 层在数据库初始化后调用。
func SetDBStatsProvider(fn func() []DBStatsSnapshot) {
	dbStatsProvider = fn
}

var dbStatsDescs = map[string]*prometheus.Desc{
	"max_open": prometheus.NewDesc(
		"newapi_db_max_open_connections",
		"Maximum number of open connections to the database.",
		[]string{"db"}, nil,
	),
	"open": prometheus.NewDesc(
		"newapi_db_open_connections",
		"Current number of open connections to the database.",
		[]string{"db"}, nil,
	),
	"in_use": prometheus.NewDesc(
		"newapi_db_in_use_connections",
		"Connections currently in use.",
		[]string{"db"}, nil,
	),
	"idle": prometheus.NewDesc(
		"newapi_db_idle_connections",
		"Idle connections in the pool.",
		[]string{"db"}, nil,
	),
	"wait_count": prometheus.NewDesc(
		"newapi_db_wait_count_total",
		"Total connections waited for (pool exhausted at least once when > 0).",
		[]string{"db"}, nil,
	),
	"wait_seconds": prometheus.NewDesc(
		"newapi_db_wait_duration_seconds_total",
		"Total time blocked waiting for a connection.",
		[]string{"db"}, nil,
	),
}

// dbStatsCollector 在每次 scrape 时读取连接池快照，保证指标是实时的。
type dbStatsCollector struct{}

func (dbStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range dbStatsDescs {
		ch <- desc
	}
}

func (dbStatsCollector) Collect(ch chan<- prometheus.Metric) {
	if dbStatsProvider == nil {
		return
	}
	for _, snapshot := range dbStatsProvider() {
		values := map[string]float64{
			"max_open":     float64(snapshot.MaxOpen),
			"open":         float64(snapshot.Open),
			"in_use":       float64(snapshot.InUse),
			"idle":         float64(snapshot.Idle),
			"wait_count":   float64(snapshot.WaitCount),
			"wait_seconds": snapshot.WaitSeconds,
		}
		for name, value := range values {
			ch <- prometheus.MustNewConstMetric(dbStatsDescs[name], prometheus.GaugeValue, value, snapshot.Kind)
		}
	}
}
