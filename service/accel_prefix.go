package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// ============================================================================
// 加速路径水印（hc52 · 方案 ⑤′）
// ============================================================================
//
// 加速域名共用一个，但每个订阅用户拿到自己的路径前缀：
//
//	https://accel.example.com/a3f2b91c/v1/chat/completions
//
// 前缀是 HMAC(站点盐, userId) 截断的 8 位 hex——不暴露账号名，但泄露后
// 能反查出是哪个订阅者传出去的（域名即水印），并且可以只封他一个人。
//
// 守卫侧的绑定校验：前缀反查到的 userId 必须与令牌对应用户一致，
// 拿别人的前缀配自己的令牌照样 403。
// ============================================================================

// AccelPrefixLen 前缀长度（hex 字符数）。8 位 = 2^32 空间，订阅量级下碰撞
// 概率可忽略；再长只是让用户多复制几个字符。
const AccelPrefixLen = 8

// accelPrefixEntry 反查缓存条目。found=false 是负缓存：乱造的前缀
// （扫描器、泄露探测）会以同样的miss 打 DB，负缓存把打点频率压住。
type accelPrefixEntry struct {
	userId int
	found  bool
	at     time.Time
}

const (
	accelPrefixHitTTL  = 5 * time.Minute  // 前缀生成后不变，正向缓存可以长
	accelPrefixMissTTL = 15 * time.Second // 新订阅刚生成前缀要尽快可查
	accelPrefixCacheMax = 20000
)

var (
	accelPrefixMu    sync.RWMutex
	accelPrefixCache = make(map[string]accelPrefixEntry)
)

// accelPrefixSalt 站点级盐。复用 CryptoSecret：本来就用它派生 HMAC/AEAD。
// 注意 CryptoSecret 未显式配置时随进程随机——但前缀生成后立即写库，
// 存量前缀不依赖盐的跨重启稳定，盐只影响「下一次惰性生成」。
func accelPrefixSalt() []byte {
	return []byte("new-api:accel-prefix:v1:" + common.CryptoSecret)
}

// GenerateAccelAccessPrefix 生成用户的水印前缀（纯函数，不落库）。
func GenerateAccelAccessPrefix(userId int) string {
	mac := hmac.New(sha256.New, accelPrefixSalt())
	mac.Write([]byte(strconv.Itoa(userId)))
	sum := mac.Sum(nil)
	return hex.EncodeToString(sum)[:AccelPrefixLen]
}

// EnsureAccelAccessPrefix 返回订阅者的水印前缀，没有就生成并写回。
// 惰性生成让存量订阅（hc52 升级前创建的）无需迁移脚本。
func EnsureAccelAccessPrefix(userId int) (string, error) {
	sub, err := model.GetUserAccelSub(userId)
	if err != nil {
		return "", err
	}
	if sub == nil {
		return "", ErrAccelNoSubscription
	}
	if sub.AccessPrefix != "" {
		return sub.AccessPrefix, nil
	}
	prefix := GenerateAccelAccessPrefix(userId)
	if err := model.UpdateUserAccelSubFields(userId, map[string]any{"access_prefix": prefix}); err != nil {
		return "", err
	}
	accelPrefixCacheSet(prefix, userId, true)
	return prefix, nil
}

func accelPrefixCacheSet(prefix string, userId int, found bool) {
	accelPrefixMu.Lock()
	if len(accelPrefixCache) >= accelPrefixCacheMax {
		// 容量兜底：整表清空只造成一轮集中回源，比无界增长安全。
		accelPrefixCache = make(map[string]accelPrefixEntry, accelPrefixCacheMax/2)
	}
	accelPrefixCache[prefix] = accelPrefixEntry{userId: userId, found: found, at: time.Now()}
	accelPrefixMu.Unlock()
}

func accelPrefixCacheGet(prefix string) (int, bool, bool) {
	accelPrefixMu.RLock()
	entry, ok := accelPrefixCache[prefix]
	accelPrefixMu.RUnlock()
	if !ok {
		return 0, false, false
	}
	ttl := accelPrefixHitTTL
	if !entry.found {
		ttl = accelPrefixMissTTL
	}
	if time.Since(entry.at) > ttl {
		return 0, false, false
	}
	return entry.userId, entry.found, true
}

var ErrAccelPrefixNotFound = errors.New("accel access prefix not found")

// RevokeAccelPrefixCache 撤销/停用订阅后把该用户的前缀标为负缓存，
// 让「单点封禁」立即生效，而不是等 5 分钟正向缓存过期。
// 重新开通订阅时会重新标正，不影响该用户前缀的稳定性。
func RevokeAccelPrefixCache(userId int) {
	sub, err := model.GetUserAccelSub(userId)
	if err != nil || sub == nil || sub.AccessPrefix == "" {
		return
	}
	accelPrefixCacheSet(sub.AccessPrefix, 0, false)
}

// AccelUserIdByPrefix 反查前缀对应的订阅用户。
// 返回 ErrAccelPrefixNotFound 表示查无此人（乱造的前缀或已撤销订阅）。
func AccelUserIdByPrefix(prefix string) (int, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if len(prefix) != AccelPrefixLen {
		return 0, ErrAccelPrefixNotFound
	}
	if userId, found, ok := accelPrefixCacheGet(prefix); ok {
		if !found {
			return 0, ErrAccelPrefixNotFound
		}
		return userId, nil
	}
	sub, err := model.GetUserAccelSubByPrefix(prefix)
	if err != nil {
		return 0, err
	}
	if sub == nil {
		accelPrefixCacheSet(prefix, 0, false)
		return 0, ErrAccelPrefixNotFound
	}
	accelPrefixCacheSet(prefix, sub.UserId, true)
	return sub.UserId, nil
}
