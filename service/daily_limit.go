package service

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// 每日额度护栏（hc45）
//
// 设计取舍：
//   - 只判「已用 >= 上限」，不预估本次请求要花多少。流式请求的完成 token
//     数是未知的，预估会大量误伤正常用户；粗判最多多放一个请求的量。
//   - 不做内存累加，每次从 logs 聚合，带 60s 缓存。零状态，进程重启不丢，
//     代价是每用户最多每分钟一次聚合查询（走 idx_user_id_id 索引）。
//   - 查询失败一律放行。护栏的作用是限流，不该在数据库抖动时把整站打挂。

// ErrDailyQuotaExceeded 当日额度已用完。
var ErrDailyQuotaExceeded = errors.New("daily quota limit exceeded")

const dailyUsageCacheTTL = 60 * time.Second

type dailyUsageEntry struct {
	day  int64
	used int64
	at   time.Time
}

var dailyUsageCache sync.Map // userId -> dailyUsageEntry

var dailyLimitWarnOnce sync.Once

// CheckDailyQuota 在请求进入计费前调用；超限返回 ErrDailyQuotaExceeded。
func CheckDailyQuota(userId int, group string) error {
	if userId <= 0 {
		return nil
	}
	limit := operation_setting.GetDailyLimitSetting().DailyQuotaForGroup(group)
	if limit <= 0 {
		return nil
	}
	dailyLimitWarnOnce.Do(func() {
		if !common.LogConsumeEnabled {
			common.SysLog("daily quota limit is enabled but consume logs are disabled; the limit cannot be enforced")
		}
	})
	used, err := TodayConsumedQuota(userId)
	if err != nil {
		return nil
	}
	if used >= int64(limit) {
		return fmt.Errorf("%w: used %d of %d today", ErrDailyQuotaExceeded, used, limit)
	}
	return nil
}

// TodayConsumedQuota 返回用户今日已消耗额度（带 60 秒缓存）。
func TodayConsumedQuota(userId int) (int64, error) {
	day := model.StartOfTodayUnix()
	if cached, ok := dailyUsageCache.Load(userId); ok {
		if entry, valid := cached.(dailyUsageEntry); valid &&
			entry.day == day && time.Since(entry.at) < dailyUsageCacheTTL {
			return entry.used, nil
		}
	}
	used, err := model.GetUserConsumedQuotaSince(userId, day)
	if err != nil {
		return 0, err
	}
	dailyUsageCache.Store(userId, dailyUsageEntry{day: day, used: used, at: time.Now()})
	return used, nil
}

// InvalidateDailyQuotaCache 让某用户的当日用量缓存立刻失效。
// 管理员手动加额度、或用户刚充值后调用，避免最长 60 秒的旧值把请求拒掉。
func InvalidateDailyQuotaCache(userId int) {
	dailyUsageCache.Delete(userId)
}

// DailyQuotaStatus 给自助查询用的组合结果。
func DailyQuotaStatus(userId int, group string) (used int64, limit int64, err error) {
	limit = int64(operation_setting.GetDailyLimitSetting().DailyQuotaForGroup(group))
	used, err = TodayConsumedQuota(userId)
	return used, limit, err
}
