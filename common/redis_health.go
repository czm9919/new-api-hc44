package common

import (
	"fmt"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// v1.1.0 M2 · B12 Redis 降级路径与告警
// ---------------------------------------------------------------------------

// Redis 在站点里承担两类职责：限流（rate-limit / model-rate-limit）和缓存
// （token/user/session/quota reserve）。缓存类调用点本来就有「失败回源数据
// 库」的降级路径；限流类调用点此前在 Redis 故障时直接 500，等于把一个外部
// 依赖的抖动放大成了全站不可用。
//
// 这里的健康跟踪器做两件事：
//   1. 显式降级——限流调用点在 Redis 出错时改走内存限流器，并连续失败达到
//      阈值后进入 degraded 状态，只打一次告警日志（带冷却期，防日志风暴）；
//   2. 显式恢复——成功调用把计数清零；从 degraded 恢复时再打一条恢复日志。
//
// 状态经 /api/health/redis（root）暴露，半夜排障时不用翻日志就能确认
// Redis 是不是正在降级。

const (
	// redisDegradeFailureThreshold 连续失败多少次进入 degraded。
	// 取 3：单次网络抖动不应触发降级告警。
	redisDegradeFailureThreshold = 3
	// redisHealthLogCooldown 状态转移日志的最小间隔，防止故障期间日志风暴。
	redisHealthLogCooldown = time.Minute
)

type redisHealthTracker struct {
	mu                  sync.Mutex
	consecutiveFailures int
	degraded            bool
	degradeCount        int64 // 累计进入 degraded 的次数（含恢复后再降）
	lastError           string
	lastErrorTime       time.Time
	lastSuccessTime     time.Time
	lastStateLogTime    time.Time
}

var redisHealth = &redisHealthTracker{}

// MarkRedisFailure 记录一次 Redis 操作失败。连续失败达到阈值时进入 degraded
// 并输出告警日志；已在 degraded 期间按冷却期合并日志。
func MarkRedisFailure(op string, err error) {
	if err == nil {
		return
	}
	redisHealth.mu.Lock()
	defer redisHealth.mu.Unlock()

	redisHealth.consecutiveFailures++
	redisHealth.lastError = fmt.Sprintf("%s: %s", op, err.Error())
	redisHealth.lastErrorTime = time.Now()

	if redisHealth.degraded {
		logRedisStateLocked()
		return
	}
	if redisHealth.consecutiveFailures >= redisDegradeFailureThreshold {
		redisHealth.degraded = true
		redisHealth.degradeCount++
		logRedisStateLocked()
	}
}

// MarkRedisSuccess 记录一次 Redis 操作成功：清零失败计数；从 degraded 恢复
// 时输出恢复日志。
func MarkRedisSuccess() {
	redisHealth.mu.Lock()
	defer redisHealth.mu.Unlock()

	wasDegraded := redisHealth.degraded
	redisHealth.consecutiveFailures = 0
	redisHealth.degraded = false
	redisHealth.lastSuccessTime = time.Now()
	if wasDegraded {
		logRedisStateLocked()
	}
}

// RedisDegraded 返回 Redis 当前是否处于降级状态。
func RedisDegraded() bool {
	redisHealth.mu.Lock()
	defer redisHealth.mu.Unlock()
	return redisHealth.degraded
}

// RedisHealthSnapshot 返回健康状态快照（管理端接口用），字段名即 JSON key。
func RedisHealthSnapshot() map[string]any {
	redisHealth.mu.Lock()
	defer redisHealth.mu.Unlock()

	snapshot := map[string]any{
		"enabled":              RedisEnabled,
		"degraded":             redisHealth.degraded,
		"consecutive_failures": redisHealth.consecutiveFailures,
		"degrade_count":        redisHealth.degradeCount,
		"failure_threshold":    redisDegradeFailureThreshold,
		"log_cooldown_seconds": int(redisHealthLogCooldown.Seconds()),
	}
	if !redisHealth.lastErrorTime.IsZero() {
		snapshot["last_error"] = redisHealth.lastError
		snapshot["last_error_time"] = redisHealth.lastErrorTime.Unix()
	}
	if !redisHealth.lastSuccessTime.IsZero() {
		snapshot["last_success_time"] = redisHealth.lastSuccessTime.Unix()
	}
	return snapshot
}

// logRedisStateLocked 输出状态转移日志。调用方必须已持有 mu。
// degraded 进入/恢复各打一条；degraded 持续期间按冷却期合并，避免每次请求
// 都刷一行。
func logRedisStateLocked() {
	now := time.Now()
	if !redisHealth.lastStateLogTime.IsZero() && now.Sub(redisHealth.lastStateLogTime) < redisHealthLogCooldown {
		return
	}
	redisHealth.lastStateLogTime = now
	if redisHealth.degraded {
		SysError(LogText(
			"Redis degraded: falling back to in-memory limiter after %d consecutive failures (degrade #%d), last error: %s",
			redisHealth.consecutiveFailures, redisHealth.degradeCount, redisHealth.lastError))
		return
	}
	SysLog(LogText("Redis recovered after degradation (last error: %s)", redisHealth.lastError))
}
