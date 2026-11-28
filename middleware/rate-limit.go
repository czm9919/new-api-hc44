package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

const redisRateLimitNamespace = "rateLimit:v2"

// Redis rate limiting intentionally uses a fixed window. The single Lua script
// makes increment, expiry, and the limit decision atomic, while retaining the
// simple fixed-window behavior: traffic at a window boundary can burst up to
// twice the configured limit. Do not replace this with a sliding-window ZSET
// unless that externally visible behavior is intentionally changed.
const redisFixedWindowScript = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
local ttl = redis.call('TTL', KEYS[1])
if ttl < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
  ttl = redis.call('TTL', KEYS[1])
end
if count > tonumber(ARGV[1]) then
  return {0, count, ttl}
end
return {1, count, ttl}
`

var inMemoryRateLimiter common.InMemoryRateLimiter

var defNext = func(c *gin.Context) {
	c.Next()
}

func redisIPRateLimitKey(mark string, clientIP string) string {
	return fmt.Sprintf("%s:ip:%s:%s", redisRateLimitNamespace, mark, clientIP)
}

func redisUserRateLimitKey(mark string, userID int) string {
	return fmt.Sprintf("%s:user:%s:%d", redisRateLimitNamespace, mark, userID)
}

func redisReplyInteger(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis integer reply type %T", value)
	}
}

func redisFixedWindowTake(ctx context.Context, key string, maxRequestNum int, duration int64) (bool, int64, int64, error) {
	if common.RDB == nil {
		return false, 0, 0, errors.New("Redis client is not initialized")
	}
	if key == "" {
		return false, 0, 0, errors.New("rate limit key is empty")
	}
	if maxRequestNum <= 0 {
		return false, 0, 0, errors.New("rate limit maximum must be positive")
	}
	if duration <= 0 {
		return false, 0, 0, errors.New("rate limit duration must be positive")
	}

	values, err := common.RDB.Eval(
		ctx,
		redisFixedWindowScript,
		[]string{key},
		maxRequestNum,
		duration,
	).Slice()
	if err != nil {
		return false, 0, 0, err
	}
	if len(values) != 3 {
		return false, 0, 0, fmt.Errorf("unexpected Redis rate limit reply length %d", len(values))
	}

	allowedValue, err := redisReplyInteger(values[0])
	if err != nil {
		return false, 0, 0, err
	}
	count, err := redisReplyInteger(values[1])
	if err != nil {
		return false, 0, 0, err
	}
	ttlSeconds, err := redisReplyInteger(values[2])
	if err != nil {
		return false, 0, 0, err
	}

	return allowedValue == 1, count, ttlSeconds, nil
}

func redisRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	allowed, _, ttlSeconds, err := redisFixedWindowTake(
		c.Request.Context(),
		redisIPRateLimitKey(mark, c.ClientIP()),
		maxRequestNum,
		duration,
	)
	if err != nil {
		// v1.1.0 M2 · B12：Redis 故障不再返回 500——限流是可用性组件，
		// 把外部依赖抖动放大成全站不可用是本末倒置。改为记录健康状态后
		// 显式降级到内存限流器（同一 key 口径，恢复时行为一致）。
		logger.LogError(c.Request.Context(), common.LogText("rate limit check failed (mark=%s), falling back to memory limiter: %v", mark, err))
		common.MarkRedisFailure("rate_limit_ip", err)
		fallbackMemoryIPRateLimiter(c, maxRequestNum, duration, mark)
		return
	}
	common.MarkRedisSuccess()
	if !allowed {
		writeRateLimited(c, ttlSeconds)
	}
}

// fallbackMemoryIPRateLimiter 是 Redis 出错时的降级路径：按 IP 走内存限流。
// key 口径与 memoryRateLimiter 保持一致（mark + IP），Redis 恢复后计数语义不变。
func fallbackMemoryIPRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	key := mark + c.ClientIP()
	if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
		writeRateLimited(c, duration)
	}
}

func memoryRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	key := mark + c.ClientIP()
	if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
		writeRateLimited(c, duration)
		return
	}
}

// writeRateLimited rejects the request with 429 and a Retry-After hint so
// clients can back off instead of treating the rejection as a fatal error.
// The in-memory limiter cannot report the remaining window, so callers
// without a TTL pass the full window duration as a conservative upper bound.
// Console requests get a JSON body with a translatable message: an empty 429
// otherwise shows up in the UI as axios's raw "Request failed with status
// code 429", which is neither localized nor actionable. Protocol paths (/v1
// and friends) keep the bare status so OpenAI-compatible clients only see it.
func writeRateLimited(c *gin.Context, retryAfterSeconds int64) {
	if retryAfterSeconds > 0 {
		c.Header("Retry-After", strconv.FormatInt(retryAfterSeconds, 10))
	}
	if c.Request != nil && c.Request.URL != nil &&
		strings.HasPrefix(c.Request.URL.Path, "/api/") {
		const tooManyRequests = "Too many requests. Please try again later."
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success":     false,
			"message":     common.LocalizeRawText(c, tooManyRequests),
			"message_key": tooManyRequests,
		})
	} else {
		c.Status(http.StatusTooManyRequests)
	}
	c.Abort()
}

func rateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	if common.RedisEnabled {
		return func(c *gin.Context) {
			redisRateLimiter(c, maxRequestNum, duration, mark)
		}
	}
	// It's safe to call multi times.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		memoryRateLimiter(c, maxRequestNum, duration, mark)
	}
}

// GlobalWebRateLimit limits dashboard pages and other fallback paths per IP.
// Files that exist in the frontend build count against a separate static
// limiter instead, so lazy-loaded chunks and icons do not use up the page
// budget. The static limiter is disabled by default.
func GlobalWebRateLimit(frontendFS static.ServeFileSystem) func(c *gin.Context) {
	webLimit := defNext
	if common.GlobalWebRateLimitEnable {
		webLimit = rateLimitFactory(common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration, "GW")
	}
	staticLimit := defNext
	if common.GlobalStaticRateLimitEnable {
		staticLimit = rateLimitFactory(common.GlobalStaticRateLimitNum, common.GlobalStaticRateLimitDuration, "GS")
	}
	return func(c *gin.Context) {
		if frontendFS.Exists("/", c.Request.URL.Path) {
			staticLimit(c)
			return
		}
		webLimit(c)
	}
}

func GlobalAPIRateLimit() func(c *gin.Context) {
	if common.GlobalApiRateLimitEnable {
		return rateLimitFactory(common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration, "GA")
	}
	return defNext
}

func CriticalRateLimit() func(c *gin.Context) {
	if common.CriticalRateLimitEnable {
		return rateLimitFactory(common.CriticalRateLimitNum, common.CriticalRateLimitDuration, "CT")
	}
	return defNext
}

func UserCriticalRateLimit(scope string) func(c *gin.Context) {
	if !common.CriticalRateLimitEnable {
		return defNext
	}
	return userRateLimitFactory(
		common.CriticalRateLimitNum,
		common.CriticalRateLimitDuration,
		"UC:"+scope,
	)
}

func DownloadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.DownloadRateLimitNum, common.DownloadRateLimitDuration, "DW")
}

func UploadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.UploadRateLimitNum, common.UploadRateLimitDuration, "UP")
}

// QQAgentRateLimit 保护面向 AstrBot 机器人的 /api/qq/agent/* 接口。
//
// 这些接口的调用方不是人而是机器人：出口 IP 固定，一个 IP 代表整群成员，
// 所以走 IP 固定窗口（按群规模给预算），而不是按用户维度。
//
// 它挡的是「令牌泄露之后」的滥用：令牌校验是常数时间比较，爆破不可行，
// 但泄露的令牌可以无限次调用 verify，每次都要查一次 qq_bind_codes 再写一次
// users。限流把这条路压成有上界的速率，同时给正常群留足余量。
//
// 注意：ClientIP() 的取值依赖 Gin 的 TrustedProxies 配置。站点在反向代理后
// 且代理未受信时，所有请求会落到同一个 key（等价全局限流）；反过来若代理
// 被信任而 X-Forwarded-For 未清洗，限流可被伪造头绕过——这与站内其它 IP
// 限流同一口径，部署侧按需配置即可。
func QQAgentRateLimit() func(c *gin.Context) {
	if !common.QQAgentRateLimitEnable {
		return defNext
	}
	return rateLimitFactory(common.QQAgentRateLimitNum, common.QQAgentRateLimitDuration, "QA")
}

// userRateLimitFactory creates a rate limiter keyed by authenticated user ID
// instead of client IP, making it resistant to proxy rotation attacks.
// Must be used AFTER authentication middleware (UserAuth).
func userRateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	if common.RedisEnabled {
		return func(c *gin.Context) {
			userID := c.GetInt("id")
			if userID == 0 {
				c.Status(http.StatusUnauthorized)
				c.Abort()
				return
			}
			userRedisRateLimiter(c, maxRequestNum, duration, redisUserRateLimitKey(mark, userID))
		}
	}
	// It's safe to call multi times.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		userID := c.GetInt("id")
		if userID == 0 {
			c.Status(http.StatusUnauthorized)
			c.Abort()
			return
		}
		key := fmt.Sprintf("%s:user:%d", mark, userID)
		if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
			writeRateLimited(c, duration)
			return
		}
	}
}

// userRedisRateLimiter is like redisRateLimiter but accepts a pre-built key
// (to support user-ID-based keys).
func userRedisRateLimiter(c *gin.Context, maxRequestNum int, duration int64, key string) {
	allowed, _, ttlSeconds, err := redisFixedWindowTake(c.Request.Context(), key, maxRequestNum, duration)
	if err != nil {
		// B12：与 redisRateLimiter 相同的显式降级——内存限流沿用同一 key。
		logger.LogError(c.Request.Context(), common.LogText("rate limit check failed (key=%s), falling back to memory limiter: %v", key, err))
		common.MarkRedisFailure("rate_limit_user", err)
		inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
		if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
			writeRateLimited(c, duration)
		}
		return
	}
	common.MarkRedisSuccess()
	if !allowed {
		writeRateLimited(c, ttlSeconds)
	}
}

// SearchRateLimit returns a per-user rate limiter for search endpoints.
// Configurable via SEARCH_RATE_LIMIT_ENABLE / SEARCH_RATE_LIMIT / SEARCH_RATE_LIMIT_DURATION.
func SearchRateLimit() func(c *gin.Context) {
	if !common.SearchRateLimitEnable {
		return defNext
	}
	return userRateLimitFactory(common.SearchRateLimitNum, common.SearchRateLimitDuration, "SR")
}
