package model

import (
	"github.com/QuantumNous/new-api/pkg/prommetrics"

	"gorm.io/gorm"
)

// 注册连接池指标提供者（v1.1.0 M2 · B138）。
//
// /metrics 上暴露 main 与 log 两个连接池的 open/in_use/idle/max_open，以及
// 等待计数与等待时长：wait_count > 0 说明连接池至少被榨干过一次，调
// SQL_MAX_OPEN_CONNS 之前先看它。DB 未初始化（单测、纯 CLI 场景）时跳过，
// 不产出该实例的样本。
func init() {
	prommetrics.SetDBStatsProvider(func() []prommetrics.DBStatsSnapshot {
		var snapshots []prommetrics.DBStatsSnapshot
		if snapshot, ok := dbStatsSnapshot(DB, "main"); ok {
			snapshots = append(snapshots, snapshot)
		}
		if snapshot, ok := dbStatsSnapshot(LOG_DB, "log"); ok {
			snapshots = append(snapshots, snapshot)
		}
		return snapshots
	})
}

// dbStatsSnapshot 读取一个 gorm 句柄背后的连接池状态。句柄或 sql.DB 不可用
// 时返回 false，让调用方跳过该实例而不是输出一堆 0 误导看板。
func dbStatsSnapshot(db *gorm.DB, kind string) (prommetrics.DBStatsSnapshot, bool) {
	if db == nil {
		return prommetrics.DBStatsSnapshot{}, false
	}
	sqlDB, err := db.DB()
	if err != nil || sqlDB == nil {
		return prommetrics.DBStatsSnapshot{}, false
	}
	stats := sqlDB.Stats()
	return prommetrics.DBStatsSnapshot{
		Kind:        kind,
		MaxOpen:     int64(stats.MaxOpenConnections),
		Open:        int64(stats.OpenConnections),
		InUse:       int64(stats.InUse),
		Idle:        int64(stats.Idle),
		WaitCount:   stats.WaitCount,
		WaitSeconds: stats.WaitDuration.Seconds(),
	}, true
}
