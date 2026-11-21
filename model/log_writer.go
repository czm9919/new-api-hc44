package model

import (
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/prommetrics"
)

// 消费日志异步批量写（v1.1.0 M1 · B09）。
//
// 背景：RecordConsumeLog 在请求路径上直接 LOG_DB.Create，每个流式/非流式
// 请求都要等一次日志 INSERT 才能返回，是尾延迟的稳定来源。这里把「记账」
// 与「落库」解耦：
//
//	RecordConsumeLog ──enqueue──▶ [有界队列] ──worker──▶ CreateInBatches
//
// 三条不可破坏的不变量：
//  1. 账务日志不能丢。队列满、异步未启动、writer 已关闭时一律退化为同步写，
//     宁可让这一条请求慢一点，也不能让消费记录凭空消失。
//  2. 单元测试行为不变。worker 只在 main() 里显式 StartConsumeLogWriter()
//     后才存在；没启动时 enqueue 直接返回 false，走原来的同步路径，因此
//     既有测试（请求后立即查日志）不需要任何改动。
//  3. 关闭时排空。StopConsumeLogWriter 会先标记 closed 拒绝新入队，再让
//     worker 把队列剩余批次刷完，最后才返回；超时则显式报错而不是静默丢。

// consumeLogWriter 是后台批量写日志的 worker 状态。
type consumeLogWriter struct {
	queue chan *Log
	stop  chan struct{}
	done  chan struct{}
	// closed 一旦置位，enqueue 立刻退化为同步写；与 stop 分开是因为
	// select 里 queue 与 stop 同时就绪时无法保证优先级，先用原子标志兜住。
	closed       atomic.Bool
	degradations atomic.Int64
}

// activeConsumeLogWriter 为 nil 表示异步写未启动（测试、CLI 子命令、或配置关闭）。
var activeConsumeLogWriter atomic.Pointer[consumeLogWriter]

// consumeLogFlushInterval 把毫秒配置转成 Duration，配置非法时回落默认值。
func consumeLogFlushInterval() time.Duration {
	ms := common.ConsumeLogFlushIntervalMs
	if ms < 10 {
		ms = 200
	}
	if ms > 60000 {
		ms = 60000
	}
	return time.Duration(ms) * time.Millisecond
}

// consumeLogQueueSize 返回有界队列容量，配置非法时回落默认值。
func consumeLogQueueSize() int {
	size := common.ConsumeLogQueueSize
	if size < 16 {
		size = 8192
	}
	return size
}

// consumeLogBatchSize 返回单批落库条数，配置非法时回落默认值。
func consumeLogBatchSize() int {
	size := common.ConsumeLogBatchSize
	if size < 1 {
		size = 100
	}
	if size > 1000 {
		size = 1000
	}
	return size
}

// StartConsumeLogWriter 启动后台批量写 worker，重复调用安全。
// 只有显式调用后 RecordConsumeLog 才会走异步入队；不调用就是原来的同步行为。
func StartConsumeLogWriter() {
	if !common.ConsumeLogAsyncEnabled {
		return
	}
	writer := &consumeLogWriter{
		queue: make(chan *Log, consumeLogQueueSize()),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	// CompareAndSwap 保证并发调用只有一个 worker 起来。
	if !activeConsumeLogWriter.CompareAndSwap(nil, writer) {
		return
	}
	go writer.run()
}

// StopConsumeLogWriter 停止接收新日志并排空队列，最长等待 timeout。
// 关闭后 enqueue 退化为同步写，进程继续运行也不会丢日志（例如重启失败回滚场景）。
func StopConsumeLogWriter(timeout time.Duration) {
	writer := activeConsumeLogWriter.Swap(nil)
	if writer == nil {
		return
	}
	writer.closed.Store(true)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	close(writer.stop)
	select {
	case <-writer.done:
	case <-time.After(timeout):
		common.SysError(common.LogText("consume log writer drain timed out, %d queued entries may be lost", len(writer.queue)))
	}
}

// enqueueConsumeLog 尝试把一条消费日志交给后台 worker。
// 返回 false 表示调用方应立即同步写库（异步未启动 / 已关闭 / 队列已满）。
func enqueueConsumeLog(entry *Log) bool {
	writer := activeConsumeLogWriter.Load()
	if writer == nil || writer.closed.Load() {
		return false
	}
	select {
	case writer.queue <- entry:
		prommetrics.LogWriterEvents.WithLabelValues("enqueued").Inc()
		return true
	default:
		// 队列满：这一条降级同步写，同时计数供告警使用。
		writer.degradations.Add(1)
		prommetrics.LogWriterEvents.WithLabelValues("degraded_sync").Inc()
		return false
	}
}

// ConsumeLogWriterStats 供 /metrics 与运维排查使用。
type ConsumeLogWriterStats struct {
	Running      bool
	QueueDepth   int
	QueueSize    int
	Degradations int64
}

// ConsumeLogWriterState 返回当前 worker 状态快照。
func ConsumeLogWriterState() ConsumeLogWriterStats {
	writer := activeConsumeLogWriter.Load()
	if writer == nil {
		return ConsumeLogWriterStats{}
	}
	return ConsumeLogWriterStats{
		Running:      !writer.closed.Load(),
		QueueDepth:   len(writer.queue),
		QueueSize:    cap(writer.queue),
		Degradations: writer.degradations.Load(),
	}
}

// run 是 worker 主循环：攒够 batchSize 立即刷，否则按 ticker 定期刷；
// 收到 stop 后把队列里剩下的全部排空再退出。
func (writer *consumeLogWriter) run() {
	defer close(writer.done)
	batchSize := consumeLogBatchSize()
	ticker := time.NewTicker(consumeLogFlushInterval())
	defer ticker.Stop()
	batch := make([]*Log, 0, batchSize)
	for {
		select {
		case entry := <-writer.queue:
			batch = append(batch, entry)
			if len(batch) >= batchSize {
				batch = writer.flush(batch)
			}
		case <-ticker.C:
			if len(batch) > 0 {
				batch = writer.flush(batch)
			}
		case <-writer.stop:
			// 排空队列里剩余的日志后再退出，避免关闭时丢账。
		drain:
			for {
				select {
				case entry := <-writer.queue:
					batch = append(batch, entry)
					if len(batch) >= batchSize {
						batch = writer.flush(batch)
					}
				default:
					break drain
				}
			}
			if len(batch) > 0 {
				writer.flush(batch)
			}
			return
		}
	}
}

// flush 落库当前批次并清空（复用底层数组，避免每个批次都重新分配）。
func (writer *consumeLogWriter) flush(batch []*Log) []*Log {
	if len(batch) == 0 {
		return batch
	}
	persistConsumeLogBatch(batch)
	for i := range batch {
		batch[i] = nil
	}
	return batch[:0]
}

// persistConsumeLogBatch 批量插入，整批失败后逐条重试：
// 一行超长/类型不合法不该拖掉同批次其余账务日志。
func persistConsumeLogBatch(batch []*Log) {
	if len(batch) == 0 {
		return
	}
	if LOG_DB == nil {
		prommetrics.LogWriterEvents.WithLabelValues("dropped").Add(float64(len(batch)))
		common.SysError(common.LogText("consume log writer: log database is not initialized, dropping %d entries", len(batch)))
		return
	}
	rows := make([]Log, len(batch))
	for i, entry := range batch {
		ensureLogRequestId(entry)
		rows[i] = *entry
	}
	// CreateInBatches 一次 INSERT 多行；批量大小就等于本批行数。
	if err := LOG_DB.CreateInBatches(&rows, len(rows)).Error; err == nil {
		prommetrics.LogWriterEvents.WithLabelValues("persisted").Add(float64(len(rows)))
		return
	} else {
		common.SysError(common.LogText("consume log writer: batch insert failed, retrying row by row: %s", err.Error()))
		prommetrics.LogWriterEvents.WithLabelValues("batch_retry").Inc()
	}
	for i := range rows {
		if err := LOG_DB.Create(&rows[i]).Error; err != nil {
			prommetrics.LogWriterEvents.WithLabelValues("dropped").Inc()
			common.SysError(common.LogText("consume log writer: failed to persist consume log for user %d: %s", rows[i].UserId, err.Error()))
			continue
		}
		prommetrics.LogWriterEvents.WithLabelValues("persisted").Inc()
	}
}
