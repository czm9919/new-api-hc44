package common

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// resetRedisHealthForTest 把跟踪器状态复位，测试互不影响。
func resetRedisHealthForTest(t *testing.T) {
	t.Helper()
	redisHealth.mu.Lock()
	redisHealth.consecutiveFailures = 0
	redisHealth.degraded = false
	redisHealth.degradeCount = 0
	redisHealth.lastError = ""
	redisHealth.lastErrorTime = time.Time{}
	redisHealth.lastSuccessTime = time.Time{}
	redisHealth.lastStateLogTime = time.Time{}
	redisHealth.mu.Unlock()
}

// 单次失败不应触发降级：网络抖动属于常态。
func TestMarkRedisFailureSingleNotDegraded(t *testing.T) {
	resetRedisHealthForTest(t)
	MarkRedisFailure("op", errors.New("boom"))
	assert.False(t, RedisDegraded())
}

// 连续失败达到阈值进入 degraded，继续失败不重复计数 degrade_count。
func TestMarkRedisFailureDegradesAfterThreshold(t *testing.T) {
	resetRedisHealthForTest(t)
	for i := 0; i < redisDegradeFailureThreshold; i++ {
		MarkRedisFailure("op", errors.New("boom"))
	}
	assert.True(t, RedisDegraded())

	snapshot := RedisHealthSnapshot()
	assert.Equal(t, int64(1), snapshot["degrade_count"])
	assert.Contains(t, snapshot["last_error"], "op: boom")

	// 第 4 次失败：仍在 degraded，但 degrade_count 不再累加。
	MarkRedisFailure("op", errors.New("boom"))
	assert.Equal(t, int64(1), RedisHealthSnapshot()["degrade_count"])
}

// 一次成功即从 degraded 恢复，失败计数清零。
func TestMarkRedisSuccessRecovers(t *testing.T) {
	resetRedisHealthForTest(t)
	for i := 0; i < redisDegradeFailureThreshold; i++ {
		MarkRedisFailure("op", errors.New("boom"))
	}
	assert.True(t, RedisDegraded())

	MarkRedisSuccess()
	assert.False(t, RedisDegraded())
	assert.Equal(t, 0, RedisHealthSnapshot()["consecutive_failures"])
}

// 恢复后再连续失败会再次进入 degraded，degrade_count 累加。
func TestDegradeRecoverCycleCounts(t *testing.T) {
	resetRedisHealthForTest(t)
	for i := 0; i < redisDegradeFailureThreshold; i++ {
		MarkRedisFailure("op", errors.New("boom"))
	}
	MarkRedisSuccess()
	for i := 0; i < redisDegradeFailureThreshold; i++ {
		MarkRedisFailure("op", errors.New("boom"))
	}
	assert.True(t, RedisDegraded())
	assert.Equal(t, int64(2), RedisHealthSnapshot()["degrade_count"])
}

// nil 错误是调用方笔误，不改变状态。
func TestMarkRedisFailureNilIgnored(t *testing.T) {
	resetRedisHealthForTest(t)
	MarkRedisFailure("op", nil)
	assert.False(t, RedisDegraded())
	assert.Empty(t, RedisHealthSnapshot()["last_error"])
}

// 快照始终带 enabled 字段（测试环境 RedisEnabled 默认 true）。
func TestRedisHealthSnapshotEnabled(t *testing.T) {
	resetRedisHealthForTest(t)
	assert.Equal(t, RedisEnabled, RedisHealthSnapshot()["enabled"])
}
