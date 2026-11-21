package service

import (
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 登录失败锁定（v1.1.0 M1 · B77）。
//
// 连续失败达到阈值后，该用户名在窗口内拒绝密码登录尝试（429），成功登录
// 立即清零计数。按用户名（而非 IP）计数，是因为撞库攻击通常跨多个 IP 轮换；
// 代价是攻击者可以恶意锁定别人的账号——所以阈值与窗口都可由管理员调整，
// 且默认窗口短（15 分钟），不接受长时间离线锁定。
//
// 状态存内存：单实例（默认部署形态）下正确；多实例部署时各节点独立计数，
// 等效于把阈值按节点数放大。需要跨节点一致时接 Redis 即可（现有 redis
// 固定窗口工具可复用）。

type loginFailureState struct {
	failures  int
	lockUntil time.Time
	lastFail  time.Time
}

var (
	loginGuardMu     sync.Mutex
	loginGuardStates = map[string]*loginFailureState{}
	loginGuardSwept  time.Time
)

// normalizeLoginGuardKey 统一用户名大小写与首尾空白，避免 "Admin" 与 "admin"
// 被当成两个账号分别计数。
func normalizeLoginGuardKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func sweepLoginGuardLocked(now time.Time) {
	if now.Sub(loginGuardSwept) < time.Minute {
		return
	}
	loginGuardSwept = now
	for key, state := range loginGuardStates {
		if state.lockUntil.IsZero() && now.Sub(state.lastFail) > 30*time.Minute {
			delete(loginGuardStates, key)
		}
	}
}

// LoginLockRemainingSeconds reports how many seconds the username must wait
// before another password attempt is accepted. 0 means not locked.
func LoginLockRemainingSeconds(username string) int {
	threshold, minutes := common.LoginFailureLockThreshold, common.LoginFailureLockMinutes
	if threshold <= 0 || minutes <= 0 {
		return 0
	}
	now := time.Now()
	loginGuardMu.Lock()
	defer loginGuardMu.Unlock()
	state, ok := loginGuardStates[normalizeLoginGuardKey(username)]
	if !ok || state.lockUntil.IsZero() {
		return 0
	}
	if now.Before(state.lockUntil) {
		return int(state.lockUntil.Sub(now).Seconds()) + 1
	}
	state.lockUntil = time.Time{}
	state.failures = 0
	return 0
}

// RecordLoginFailure counts one failed password attempt for username and locks
// it once the configured threshold is reached.
func RecordLoginFailure(username string) {
	threshold, minutes := common.LoginFailureLockThreshold, common.LoginFailureLockMinutes
	if threshold <= 0 || minutes <= 0 {
		return
	}
	now := time.Now()
	loginGuardMu.Lock()
	defer loginGuardMu.Unlock()
	sweepLoginGuardLocked(now)
	key := normalizeLoginGuardKey(username)
	if key == "" {
		return
	}
	state := loginGuardStates[key]
	if state == nil {
		state = &loginFailureState{}
		loginGuardStates[key] = state
	}
	state.failures++
	state.lastFail = now
	if state.failures >= threshold {
		state.lockUntil = now.Add(time.Duration(minutes) * time.Minute)
	}
}

// ResetLoginFailures clears the failure counter after a successful login.
func ResetLoginFailures(username string) {
	loginGuardMu.Lock()
	defer loginGuardMu.Unlock()
	state := loginGuardStates[normalizeLoginGuardKey(username)]
	if state != nil {
		state.failures = 0
		state.lockUntil = time.Time{}
	}
}

// ResetLoginGuard drops all lockout state. Tests use it between scenarios.
func ResetLoginGuard() {
	loginGuardMu.Lock()
	defer loginGuardMu.Unlock()
	loginGuardStates = map[string]*loginFailureState{}
}
