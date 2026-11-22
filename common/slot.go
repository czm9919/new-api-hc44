package common

import (
	"context"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

// ============================================================================
// 跨实例并发槽位（hc50）
// ============================================================================
//
// 加速专线原先的并发计数是进程内 map（atomic.Int64），多实例部署时每个实例
// 各算各的，用户开 N 个实例就能拿到 N 倍并发。本文件提供 Redis 侧的原子实现：
//
//	INCR key
//	  首次自增时设 TTL（进程崩溃/被 kill 也能自然回收，不会永久泄漏槽位）
//	  超过上限 → DECR 回滚并返回 0（未拿到槽位）
//
// 必须用脚本原子执行：先 INCR 再判断再 DECR 的三次往返在并发下会超卖。
// 调用方负责在 Redis 不可用时降级到进程内计数（见 service.AcquireAccelSlot）。

// slotReserveScript 原子占位：返回 1 表示拿到槽位，0 表示已满。
var slotReserveScript = redis.NewScript(`
local cur = redis.call('INCR', KEYS[1])
if cur == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
if cur > tonumber(ARGV[1]) then
  redis.call('DECR', KEYS[1])
  return 0
end
return 1
`)

// slotReleaseScript 原子释放：减到 0 或以下直接删键，避免残留空计数器占内存。
var slotReleaseScript = redis.NewScript(`
local cur = redis.call('DECR', KEYS[1])
if cur <= 0 then
  redis.call('DEL', KEYS[1])
  return 0
end
return cur
`)

// RedisSlotAvailable 判断能否走 Redis 槽位。RDB 未初始化（未配
// REDIS_CONN_STRING）或显式关闭时返回 false，调用方应降级。
func RedisSlotAvailable() bool {
	return RedisEnabled && RDB != nil
}

// RedisReserveSlot 尝试在 Redis 中占一个并发槽位。
//
// limit <= 0 视为不限并发，直接返回 true 且不写 Redis（release 也会变成空操作）。
// ttl <= 0 时用 10 分钟兜底：正常请求远短于它，异常进程退出后最多占用 10 分钟。
//
// 返回 (reserved, err)：err != nil 表示 Redis 侧失败，调用方**必须**降级到本地计数，
// 不能因为缓存故障把用户挡在门外。
func RedisReserveSlot(key string, limit int, ttl time.Duration) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	if !RedisSlotAvailable() {
		return false, redis.ErrClosed
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	res, err := slotReserveScript.Run(context.Background(), RDB,
		[]string{key}, limit, ttl.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

// RedisReleaseSlot 释放一个槽位。重复释放是安全的（不会减成负数），
// 但调用方仍应保证一次占位对应一次释放。
func RedisReleaseSlot(key string) error {
	if !RedisSlotAvailable() {
		return redis.ErrClosed
	}
	return slotReleaseScript.Run(context.Background(), RDB, []string{key}).Err()
}

// SlotKey 拼一个带业务前缀的槽位键，顺便把分隔符替换掉，
// 防止 userId 之外的维度（未来可能带模型名）把键拼错层。
func SlotKey(prefix string, parts ...string) string {
	cleaned := make([]string, 0, len(parts)+1)
	cleaned = append(cleaned, prefix)
	for _, part := range parts {
		cleaned = append(cleaned, strings.NewReplacer(":", "_", " ", "_").Replace(part))
	}
	return strings.Join(cleaned, ":")
}
