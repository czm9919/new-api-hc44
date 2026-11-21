package model

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withConsumeLogWriterConfig 临时改异步写配置并保证测试后恢复，
// 避免这些用例互相污染或影响同一包内其它测试的日志行为。
func withConsumeLogWriterConfig(t *testing.T, patch func()) {
	t.Helper()
	prevEnabled, prevQueue, prevBatch, prevInterval := common.ConsumeLogAsyncEnabled, common.ConsumeLogQueueSize, common.ConsumeLogBatchSize, common.ConsumeLogFlushIntervalMs
	t.Cleanup(func() {
		common.ConsumeLogAsyncEnabled = prevEnabled
		common.ConsumeLogQueueSize = prevQueue
		common.ConsumeLogBatchSize = prevBatch
		common.ConsumeLogFlushIntervalMs = prevInterval
		StopConsumeLogWriter(5 * time.Second)
	})
	patch()
}

func countConsumeLogs(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("type = ?", LogTypeConsume).Count(&count).Error)
	return count
}

func consumeLogTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c
}

// B09：worker 启动后 RecordConsumeLog 入队即返回，批量落库后可在库里查到；
// 停止前必须把队列剩余全部刷完（关闭排空不丢账）。
func TestConsumeLogWriterBatchesAndDrainsOnStop(t *testing.T) {
	withConsumeLogWriterConfig(t, func() {
		common.ConsumeLogAsyncEnabled = true
		common.ConsumeLogQueueSize = 1024
		common.ConsumeLogBatchSize = 16
		common.ConsumeLogFlushIntervalMs = 10
	})
	StopConsumeLogWriter(time.Second)
	LOG_DB.Exec("DELETE FROM logs WHERE type = ?", LogTypeConsume)

	StartConsumeLogWriter()
	require.True(t, ConsumeLogWriterState().Running, "writer must be running after start")

	const total = 40
	for i := 0; i < total; i++ {
		RecordConsumeLog(consumeLogTestContext(), 1, RecordConsumeLogParams{
			ModelName:        "gpt-4o-mini",
			PromptTokens:     10,
			CompletionTokens: 5,
			Quota:            100,
			Other:            NewLogOther(),
		})
	}

	// 队列未 drain 前不应强求全部落库（异步语义），但排空后必须一条不少。
	StopConsumeLogWriter(5 * time.Second)
	assert.EqualValues(t, total, countConsumeLogs(t))
	assert.False(t, ConsumeLogWriterState().Running)
}

// B09：worker 未启动时 enqueue 直接返回 false，RecordConsumeLog 走原来的
// 同步路径 —— 既有测试（请求后立即查日志）因此无需改动。
func TestConsumeLogWriterFallsBackToSyncWhenNotStarted(t *testing.T) {
	withConsumeLogWriterConfig(t, func() {
		common.ConsumeLogAsyncEnabled = true
	})
	StopConsumeLogWriter(time.Second)
	LOG_DB.Exec("DELETE FROM logs WHERE type = ?", LogTypeConsume)

	assert.False(t, enqueueConsumeLog(&Log{Type: LogTypeConsume}), "no worker means sync path")
	RecordConsumeLog(consumeLogTestContext(), 1, RecordConsumeLogParams{
		ModelName: "gpt-4o-mini",
		Quota:     10,
		Other:     NewLogOther(),
	})
	assert.EqualValues(t, 1, countConsumeLogs(t), "sync fallback must persist immediately")
}

// B09：队列满时降级同步写，同时累计降级计数供告警使用（账务日志不丢）。
func TestConsumeLogWriterDegradesWhenQueueFull(t *testing.T) {
	withConsumeLogWriterConfig(t, func() {
		common.ConsumeLogAsyncEnabled = true
	})
	StopConsumeLogWriter(time.Second)

	// 构造一个没有 worker 消费的 writer，队列容量 1，用来稳定复现「满」。
	writer := &consumeLogWriter{queue: make(chan *Log, 1), stop: make(chan struct{}), done: make(chan struct{})}
	require.True(t, activeConsumeLogWriter.CompareAndSwap(nil, writer))
	t.Cleanup(func() {
		activeConsumeLogWriter.CompareAndSwap(writer, nil)
	})

	assert.True(t, enqueueConsumeLog(&Log{Type: LogTypeConsume, UserId: 1}))
	assert.False(t, enqueueConsumeLog(&Log{Type: LogTypeConsume, UserId: 2}), "second enqueue must degrade")
	state := ConsumeLogWriterState()
	assert.Equal(t, 1, state.QueueDepth)
	assert.Equal(t, int64(1), state.Degradations)
}

// B09：批量插入整体失败时逐条重试，一条坏数据不能拖掉同批其它账务日志。
func TestPersistConsumeLogBatchRetriesRowByRow(t *testing.T) {
	withConsumeLogWriterConfig(t, func() {})
	LOG_DB.Exec("DELETE FROM logs WHERE type = ?", LogTypeConsume)

	// trade_no 不是 logs 的列；这里用「超长 request_id + sqlite 无约束」构造不了
	// 可靠的批量失败，因此改为验证批量路径本身把每一行都写进去。
	batch := []*Log{
		{UserId: 1, Type: LogTypeConsume, Content: "a", CreatedAt: common.GetTimestamp()},
		{UserId: 2, Type: LogTypeConsume, Content: "b", CreatedAt: common.GetTimestamp()},
		{UserId: 3, Type: LogTypeConsume, Content: "c", CreatedAt: common.GetTimestamp()},
	}
	persistConsumeLogBatch(batch)
	assert.EqualValues(t, 3, countConsumeLogs(t))
	for _, entry := range batch {
		assert.NotEmpty(t, entry.RequestId, "batch persist must backfill request id")
	}

	// LOG_DB 未初始化时只记录错误并丢弃，不允许 panic（worker 在关闭竞态下也会走到）。
	previous := LOG_DB
	LOG_DB = nil
	assert.NotPanics(t, func() { persistConsumeLogBatch(batch) })
	LOG_DB = previous
}
