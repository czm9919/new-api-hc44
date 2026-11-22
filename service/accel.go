package service

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// 加速专线订阅服务（hc48）
//
// 这里只做「谁能用加速分组」的判定与计费，不碰链路本身：
//   - 链路是管理员在渠道上配 Proxy + 在 ratio_setting 配分组倍率；
//   - 本文件负责把「订阅有效」翻译成「该用户可以使用 GroupName 分组」。
//
// 为什么必须有这一层：TokenAuth 只会检查 token.Group 是否在
// GetUserUsableGroups() 里，那是静态配置。若把加速分组直接塞进静态列表，
// 所有用户都能免费用；若不塞，付费用户又永远进不去。订阅表就是补上
// 「按用户动态放行」这一块。
const (
	AccelSubCacheTTL = 15 * time.Second
	AccelMaxPrice    = 100_000_000 // 单月费用上限，防手滑填出天价
	AccelMaxConc     = 1000        // 并发上限上限值
	accelCacheMaxLen = 5000
)

var (
	ErrAccelPlanDisabled     = errors.New("accel plan is disabled")
	ErrAccelPlanNotFound     = errors.New("accel plan not found")
	ErrAccelGroupMissing     = errors.New("accel group has no ratio configured")
	ErrAccelGroupPublic      = errors.New("accel group is already available to all users")
	ErrAccelAlreadyActive    = errors.New("accel subscription is still active")
	ErrAccelNoSubscription   = errors.New("accel subscription not found")
	ErrAccelInsufficientFund = errors.New("insufficient paid balance")
	ErrAccelInvalidDays      = errors.New("invalid extend days")
)

// AccelMaxPriceUSD 单月费用上限的美元报价，供管理端表单与错误文案复用。
// 上限本身（AccelMaxPrice）是额度常量，换算随 QuotaPerUnit 变化，所以用函数。
func AccelMaxPriceUSD() float64 {
	return common.RoundUSD(common.QuotaToUSD(AccelMaxPrice))
}

// AccelPriceStepUSD 管理端价格输入的最小步长（1 美分）。
const AccelPriceStepUSD = 0.01

// AccelMaxExtendDays 管理端单次补时长上限（10 年），防止手滑输入 999999。
const AccelMaxExtendDays = 3650

type accelCacheEntry struct {
	sub *model.UserAccelSub
	at  time.Time
}

var (
	accelCacheMu sync.RWMutex
	accelCache   = make(map[int]accelCacheEntry)
	// accelGroups 是「哪些分组受套餐保护」的内存快照。
	// 每条 relay 请求都要判断一次，所以绝不能在这里查库：表不存在或 DB 抖动时
	// 会把普通分组的请求一起打成 500。快照为空 = 没有任何分组受保护，全部放行；
	// 真正的保护判定在 UserCanUseGroup（那里才查订阅）。
	accelGroupsMu     sync.RWMutex
	accelGroups       = map[string]*model.AccelPlan{}
	accelGroupsLoaded bool
	// renewMu 串行化惰性续费：同一时刻多个请求同时发现订阅过期时，
	// 只有第一个真正扣费，其余直接读它写好的结果。
	renewMu sync.Mutex
	// inflight 记录每个用户当前在途的加速请求数（进程内计数）。
	// hc50 起优先走 Redis 原子槽位（多实例共享上限）；Redis 未配置或报错时
	// 降级到这里，保证缓存故障不会把付费用户挡在门外。
	inflight sync.Map // map[int]*int64
	// slotWarnOnce 限频 Redis 槽位降级告警：Redis 抖动时每条请求都打日志会把
	// 日志盘刷满，改成一分钟内最多一条。
	slotWarnOnce atomic.Int64 // 上次告警的 unix 秒
)

func accelCacheGet(userId int) (*model.UserAccelSub, bool) {
	accelCacheMu.RLock()
	entry, ok := accelCache[userId]
	accelCacheMu.RUnlock()
	if !ok || time.Since(entry.at) > AccelSubCacheTTL {
		return nil, false
	}
	return entry.sub, true
}

func accelCacheSet(userId int, sub *model.UserAccelSub) {
	accelCacheMu.Lock()
	if len(accelCache) >= accelCacheMaxLen {
		// 容量兜底：整表清空只会造成一次集中回源，比无界增长安全。
		accelCache = make(map[int]accelCacheEntry, accelCacheMaxLen/2)
	}
	accelCache[userId] = accelCacheEntry{sub: sub, at: time.Now()}
	accelCacheMu.Unlock()
}

// InvalidateAccelCache 开通/取消/续费后必须调用，否则最长 15 秒内
// 用户仍看到旧状态（这个坑和排行榜 60 秒缓存是同一类）。
func InvalidateAccelCache(userId int) {
	accelCacheMu.Lock()
	delete(accelCache, userId)
	accelCacheMu.Unlock()
}

// GetActiveSubscription 返回用户当前有效的加速订阅；没有则返回 (nil, nil)。
// 到期且开着自动续费时会就地尝试扣费续期，失败则把订阅置为 expired
// 并把原因写进 LastChargeError —— 这样不需要定时任务扫全表。
func GetActiveSubscription(userId int) (*model.UserAccelSub, error) {
	if userId <= 0 {
		return nil, nil
	}
	if sub, ok := accelCacheGet(userId); ok {
		return sub, nil
	}
	sub, err := model.GetUserAccelSub(userId)
	if err != nil {
		// 查库失败时 fail-closed：宁可误伤一次，也不放行未付费通道。
		return nil, err
	}
	if sub == nil {
		accelCacheSet(userId, nil)
		return nil, nil
	}
	now := time.Now().Unix()
	if sub.Active(now) {
		accelCacheSet(userId, sub)
		return sub, nil
	}
	if sub.Status != model.AccelSubStatusActive {
		accelCacheSet(userId, nil)
		return nil, nil
	}
	// 已过期但仍标记 active：要么自动续费，要么落成 expired。
	renewed, err := renewExpiredSubscription(userId, now)
	if err != nil {
		return nil, err
	}
	if renewed == nil {
		accelCacheSet(userId, nil)
		return nil, nil
	}
	accelCacheSet(userId, renewed)
	return renewed, nil
}

// renewExpiredSubscription 处理「到期」这一瞬间，返回续期后的订阅；
// 返回 nil 表示已失效。
func renewExpiredSubscription(userId int, now int64) (*model.UserAccelSub, error) {
	renewMu.Lock()
	defer renewMu.Unlock()

	// 加锁后重读，避免并发请求各自扣一次月费。
	sub, err := model.GetUserAccelSub(userId)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, nil
	}
	if sub.Active(now) {
		return sub, nil
	}
	if !sub.AutoRenew || sub.Status != model.AccelSubStatusActive {
		markAccelExpired(sub, "自动续费已关闭")
		return nil, nil
	}
	price := sub.PricePerMonth
	if plan, planErr := model.GetAccelPlanById(sub.PlanId); planErr == nil && plan.Enabled && plan.PricePerMonth > 0 {
		price = plan.PricePerMonth
	}
	if price > 0 {
		ok, chargeErr := chargePaidBalance(userId, price)
		if chargeErr != nil {
			markAccelExpired(sub, "续费扣费失败："+chargeErr.Error())
			return nil, nil
		}
		if !ok {
			markAccelExpired(sub, "付费余额不足，自动续费失败")
			return nil, nil
		}
	}
	// 从原到期时间顺延，用户不会因为提前续费而损失剩余时长。
	base := sub.ExpireAt
	if base < now {
		base = now
	}
	newExpire := base + model.AccelPeriodSeconds
	fields := map[string]any{
		"expire_at":         newExpire,
		"last_charged_at":   now,
		"last_charge_error": "",
		"status":            model.AccelSubStatusActive,
		"total_charged":     sub.TotalCharged + price,
	}
	if err := model.UpdateUserAccelSubFields(userId, fields); err != nil {
		return nil, err
	}
	sub.ExpireAt = newExpire
	sub.LastChargedAt = now
	sub.LastChargeError = ""
	sub.Status = model.AccelSubStatusActive
	sub.TotalCharged += price
	return sub, nil
}

func markAccelExpired(sub *model.UserAccelSub, reason string) {
	fields := map[string]any{
		"status":            model.AccelSubStatusExpired,
		"last_charge_error": truncateAccelText(reason, 240),
	}
	_ = model.UpdateUserAccelSubFields(sub.UserId, fields)
	sub.Status = model.AccelSubStatusExpired
	sub.LastChargeError = reason
}

func truncateAccelText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// chargePaidBalance 只扣付费余额（quota - free_quota），不碰免费额度。
// 不能用 DecreaseUserQuota：那会优先消耗免费额度，等于把免费额度变现。
func chargePaidBalance(userId int, price int64) (bool, error) {
	if price <= 0 {
		return true, nil
	}
	if price > AccelMaxPrice {
		return false, fmt.Errorf("price %d exceeds limit %d", price, AccelMaxPrice)
	}
	return model.TryReserveUserQuotaUseFree(userId, int(price), false)
}

// UserCanUseGroup 在静态可用分组之外补上「订阅解锁」的判断。
// TokenAuth / playground 的分组校验都必须走这里，否则用户手工改 token 的
// group 字段就能白嫖专线（前端隐藏入口不构成任何限制）。
func UserCanUseGroup(userId int, userGroup string, groupName string) bool {
	if groupName == "" {
		return false
	}
	if _, ok := GetUserUsableGroups(userGroup)[groupName]; ok {
		return true
	}
	if groupName == "auto" {
		return false
	}
	sub, err := GetActiveSubscription(userId)
	if err != nil || sub == nil {
		return false
	}
	return sub.GroupName == groupName
}

// SubscribeAccelPlan 开通/续费/换套餐：先原子扣月费，再落订阅；写库失败则退款。
//
// allowSwitch 为 true 时允许「生效中换套餐」：按新套餐价格重新扣一个月，
// 周期从当下重新计算（原剩余时长作废）。这是不可逆的资损动作，所以必须
// 由前端二次确认后显式传 switch=true；默认 false 仍返回 409，避免连点重复扣费。
func SubscribeAccelPlan(userId, planId int, allowSwitch bool) (*model.UserAccelSub, error) {
	plan, err := model.GetAccelPlanById(planId)
	if err != nil {
		return nil, ErrAccelPlanNotFound
	}
	if !plan.Enabled {
		return nil, ErrAccelPlanDisabled
	}
	if !ratio_setting.ContainsGroupRatio(plan.GroupName) {
		return nil, ErrAccelGroupMissing
	}

	existing, err := model.GetUserAccelSub(userId)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if existing != nil && existing.Active(now) {
		// 同一套餐重复提交 = 连点，直接拒绝；不同套餐才谈得上「换」。
		if !allowSwitch || existing.PlanId == plan.Id {
			return nil, ErrAccelAlreadyActive
		}
	}

	ok, err := chargePaidBalance(userId, plan.PricePerMonth)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrAccelInsufficientFund
	}

	if existing == nil {
		sub := &model.UserAccelSub{
			UserId:        userId,
			PlanId:        plan.Id,
			GroupName:     plan.GroupName,
			PricePerMonth: plan.PricePerMonth,
			StartAt:       now,
			ExpireAt:      now + model.AccelPeriodSeconds,
			AutoRenew:     true,
			Status:        model.AccelSubStatusActive,
			LastChargedAt: now,
			TotalCharged:  plan.PricePerMonth,
		}
		if err := model.CreateUserAccelSub(sub); err != nil {
			refundAccelCharge(userId, plan.PricePerMonth)
			return nil, err
		}
		InvalidateAccelCache(userId)
		sub.FillPriceUSD()
		return sub, nil
	}

	// 过期订阅重新开通：分组/价格按当前套餐重算。
	fields := map[string]any{
		"plan_id":           plan.Id,
		"group_name":        plan.GroupName,
		"price_per_month":   plan.PricePerMonth,
		"start_at":          now,
		"expire_at":         now + model.AccelPeriodSeconds,
		"auto_renew":        true,
		"status":            model.AccelSubStatusActive,
		"last_charged_at":   now,
		"last_charge_error": "",
		"total_charged":     existing.TotalCharged + plan.PricePerMonth,
	}
	if err := model.UpdateUserAccelSubFields(userId, fields); err != nil {
		refundAccelCharge(userId, plan.PricePerMonth)
		return nil, err
	}
	InvalidateAccelCache(userId)
	existing.PlanId = plan.Id
	existing.GroupName = plan.GroupName
	existing.PricePerMonth = plan.PricePerMonth
	existing.StartAt = now
	existing.ExpireAt = now + model.AccelPeriodSeconds
	existing.AutoRenew = true
	existing.Status = model.AccelSubStatusActive
	existing.LastChargedAt = now
	existing.LastChargeError = ""
	existing.TotalCharged += plan.PricePerMonth
	existing.FillPriceUSD()
	return existing, nil
}

func refundAccelCharge(userId int, price int64) {
	if price <= 0 {
		return
	}
	if err := model.IncreaseUserQuota(userId, int(price), true); err != nil {
		// 退款失败只能留痕，不能吞掉：这是资损，必须进日志人工核对。
		model.RecordLog(userId, model.LogTypeSystem,
			common.NewMessage("Accel refund failed, manual top-up required: {{error}}", map[string]any{"error": err.Error()}))
	}
}

// SetAccelAutoRenew 关闭后订阅仍在有效期内可用，到期自然失效。
func SetAccelAutoRenew(userId int, autoRenew bool) error {
	sub, err := model.GetUserAccelSub(userId)
	if err != nil {
		return err
	}
	if sub == nil {
		return ErrAccelNoSubscription
	}
	if err := model.UpdateUserAccelSubFields(userId, map[string]any{"auto_renew": autoRenew}); err != nil {
		return err
	}
	InvalidateAccelCache(userId)
	return nil
}

// ExtendAccelSubscription 管理端补时长（售后/补偿），不扣费。
// 已过期的订阅从当下起算，避免补偿的天数被「过去」吃掉。
func ExtendAccelSubscription(userId int, days int) (*model.UserAccelSub, error) {
	if days <= 0 || days > AccelMaxExtendDays {
		return nil, ErrAccelInvalidDays
	}
	sub, err := model.GetUserAccelSub(userId)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, ErrAccelNoSubscription
	}
	now := time.Now().Unix()
	base := sub.ExpireAt
	if base < now {
		base = now
	}
	newExpire := base + int64(days)*86400
	fields := map[string]any{
		"expire_at":         newExpire,
		"status":            model.AccelSubStatusActive,
		"last_charge_error": "",
	}
	if err := model.UpdateUserAccelSubFields(userId, fields); err != nil {
		return nil, err
	}
	InvalidateAccelCache(userId)
	sub.ExpireAt = newExpire
	sub.Status = model.AccelSubStatusActive
	sub.LastChargeError = ""
	return sub, nil
}

// RevokeAccelSubscription 管理端强制停用（违规、退款、风控）。
// 只改状态不退款：退款是资金动作，必须走人工核对，代码里不自动执行。
func RevokeAccelSubscription(userId int, reason string) error {
	sub, err := model.GetUserAccelSub(userId)
	if err != nil {
		return err
	}
	if sub == nil {
		return ErrAccelNoSubscription
	}
	if strings.TrimSpace(reason) == "" {
		reason = "已被管理员停用"
	}
	fields := map[string]any{
		"status":            model.AccelSubStatusExpired,
		"auto_renew":        false,
		"last_charge_error": truncateAccelText(reason, 240),
	}
	if err := model.UpdateUserAccelSubFields(userId, fields); err != nil {
		return err
	}
	InvalidateAccelCache(userId)
	return nil
}

// AccelSlotBackend 当前生效的并发计数后端：`redis` 表示多实例共享上限，
// `memory` 表示进程内计数（单实例准确，多实例会各算各的）。
func AccelSlotBackend() string {
	if common.RedisSlotAvailable() {
		return "redis"
	}
	return "memory"
}

// accelSlotKey Redis 里的并发槽位键。按用户维度计：套餐的并发上限是
// 「单用户能同时开几个请求」，与分组/套餐无关，换套餐不该重置计数。
func accelSlotKey(userId int) string {
	return common.SlotKey("accel:slot", strconv.Itoa(userId))
}

// warnAccelSlotFallback 限频输出降级告警（每分钟最多一条）。
// Redis 抖动时不能让每条 relay 请求都写一行日志。
func warnAccelSlotFallback(err error) {
	now := time.Now().Unix()
	last := slotWarnOnce.Load()
	if now-last < 60 {
		return
	}
	if !slotWarnOnce.CompareAndSwap(last, now) {
		return
	}
	common.SysError(common.LogText("accel slot: redis unavailable, fallback to in-process counter: %s", err.Error()))
}

// AcquireAccelSlot 取一个并发槽位；返回的 release 必须 defer 调用。
// limit <= 0 表示不限并发，返回空释放函数。
//
// hc50：优先 Redis 原子槽位（多实例共享），Redis 未配置/报错时降级为进程内
// 计数。注意降级后上限会变成「每实例 limit」，这正是降级应有的语义——
// 宁可放宽也不能因为缓存故障把付费用户全部 429。
func AcquireAccelSlot(userId int, limit int) (func(), bool) {
	if limit <= 0 {
		return func() {}, true
	}
	if common.RedisSlotAvailable() {
		ok, err := common.RedisReserveSlot(accelSlotKey(userId), limit, accelSlotTTL())
		if err == nil {
			if !ok {
				return nil, false
			}
			var once sync.Once
			return func() {
				once.Do(func() {
					if relErr := common.RedisReleaseSlot(accelSlotKey(userId)); relErr != nil {
						// 释放失败不阻断请求：TTL 兜底会把僵尸槽位自然收回。
						common.SysError(common.LogText("accel slot: redis release failed: %s", relErr.Error()))
					}
				})
			}, true
		}
		warnAccelSlotFallback(err)
	}

	var counter *int64
	if v, ok := inflight.Load(userId); ok {
		counter = v.(*int64)
	} else {
		fresh := new(int64)
		actual, _ := inflight.LoadOrStore(userId, fresh)
		counter = actual.(*int64)
	}
	if atomic.AddInt64(counter, 1) > int64(limit) {
		atomic.AddInt64(counter, -1)
		return nil, false
	}
	var once sync.Once
	return func() {
		once.Do(func() { atomic.AddInt64(counter, -1) })
	}, true
}

// accelSlotTTL 槽位兜底存活时间，取自运营配置（默认 10 分钟）。
func accelSlotTTL() time.Duration {
	seconds := operation_setting.GetAccelSetting().SlotTTLSeconds
	if seconds <= 0 {
		seconds = 600
	}
	return time.Duration(seconds) * time.Second
}

// AccelPlanGroup 返回分组对应的启用套餐；没有套餐时返回 (nil, nil)。
// 只读内存快照，不查库 —— 这是每条 relay 请求都会走的判断。
func AccelPlanGroup(groupName string) (*model.AccelPlan, error) {
	if groupName == "" {
		return nil, nil
	}
	ensureAccelGroups()
	accelGroupsMu.RLock()
	plan := accelGroups[groupName]
	accelGroupsMu.RUnlock()
	return plan, nil
}

// ensureAccelGroups 懒加载「受保护分组」快照。
//   - 表还不存在（测试库/未迁移）：标记已加载但集合为空，放行所有分组；
//   - 查库失败：同样降级为空集合，宁可这一次不拦，也不能把全站打成 500。
//
// 真正的拦截在 UserCanUseGroup：它按用户查订阅，失败时 fail-closed。
func ensureAccelGroups() {
	accelGroupsMu.RLock()
	loaded := accelGroupsLoaded
	accelGroupsMu.RUnlock()
	if loaded {
		return
	}
	accelGroupsMu.Lock()
	defer accelGroupsMu.Unlock()
	if accelGroupsLoaded {
		return
	}
	plans := make([]model.AccelPlan, 0)
	if model.AccelPlansTableReady() {
		if result, err := model.ListAccelPlans(true); err == nil {
			plans = result
		}
	}
	groups := make(map[string]*model.AccelPlan, len(plans))
	for i := range plans {
		groups[plans[i].GroupName] = &plans[i]
	}
	accelGroups = groups
	accelGroupsLoaded = true
}

// InvalidateAccelPlanCache 套餐配置变更后调用，下一次请求会重新加载分组快照。
func InvalidateAccelPlanCache() {
	accelGroupsMu.Lock()
	accelGroupsLoaded = false
	accelGroupsMu.Unlock()
}

// ValidateAccelPlanGroup 管理端建套餐时的护栏：
//   - 分组必须在 ratio_setting 里配了倍率，否则请求会被判成已废弃分组；
//   - 分组不能出现在「用户可用分组」里，否则等于对所有用户免费开放，
//     套餐就卖不动了。
func ValidateAccelPlanGroup(groupName string) error {
	if groupName == "" {
		return ErrAccelGroupMissing
	}
	if !ratio_setting.ContainsGroupRatio(groupName) {
		return ErrAccelGroupMissing
	}
	if _, ok := setting.GetUserUsableGroupsCopy()[groupName]; ok {
		return ErrAccelGroupPublic
	}
	return nil
}
