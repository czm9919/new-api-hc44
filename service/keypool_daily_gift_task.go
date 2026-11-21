package service

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	// keyPoolDailyGiftTickInterval 轮询间隔。任务每分钟检查一次，但借助「全局发放日期」Option 与
	// token 上的 daily_quota_date 两道幂等闸门，保证每个自然日（北京时间）真正重置一次，避免进程重启后重复重置。
	keyPoolDailyGiftTickInterval = 1 * time.Minute
	keyPoolDailyGiftBatchSize    = 300
	// keyPoolDailyGiftOptionKey 记录「最近一次每日重置的日期(yyyy-mm-dd, 北京时间)」，用于快速短路（今天已整体处理过则跳过扫描）。
	keyPoolDailyGiftOptionKey = "keypool_daily_gift_last_date"
	// beijingLocation 北京时间（UTC+8），号池每日重置以北京时间为准，模仿示例 freeshare 的 00:00 重置语义。
	beijingLocation = "Asia/Shanghai"
)

var (
	keyPoolDailyGiftOnce    sync.Once
	keyPoolDailyGiftRunning atomic.Bool
)

// keypoolTodayBeijing 返回当前北京时间的日期字符串(yyyy-mm-dd)。
// 若系统无法加载 Asia/Shanghai，则回退到固定的 UTC+8 时区，保证语义一致。
func keypoolTodayBeijing() string {
	loc, err := time.LoadLocation(beijingLocation)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return time.Now().In(loc).Format("2006-01-02")
}

// StartKeyPoolDailyGiftTask 启动「每日重置号池额度」定时任务。
// 仅主节点执行。每天 00:00(北京时间)把每个号池 token 的 remain_quota 重置为 daily_gift_quota
// （上游 key 仍可用时）或 0（上游 key 已失效/被风控/过期时，即「死 key 当天归零/摘除」），
// 但前提是「先重新验活其关联的上游 key 仍可用」才给满额（验活可以用才发放），且每个 token 每天只重置一次
// （用 daily_quota_date 标记，避免重复刷）。重置语义严格模仿示例 freeshare：每日额度当天消耗、次日重置，不累加。
func StartKeyPoolDailyGiftTask() {
	keyPoolDailyGiftOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), common.LogText("key pool daily gift task started: tick=%s", keyPoolDailyGiftTickInterval))
			ticker := time.NewTicker(keyPoolDailyGiftTickInterval)
			defer ticker.Stop()
			for range ticker.C {
				runKeyPoolDailyGiftOnce()
			}
		})
	})
}

// keypoolChannelKeyAlive 判断某个号池渠道当前是否仍可用（重新验活）。
// 号池渠道可能含有多个上游 key（合并渠道），只要「任一」key 验活通过即视为渠道可用，
// 不会因其中某一条死 key 而误杀整池。仅对 tag 以 "keypool-" 开头的渠道生效；
// 若 tag 缺失或无法解析供应商，则视为无法验活、为安全起见当作「可用」返回 true（不阻断发放，避免误杀）。
func keypoolChannelKeyAlive(ch *model.Channel) bool {
	if ch == nil {
		return false
	}
	if ch.Tag == nil || !strings.HasPrefix(*ch.Tag, "keypool-") {
		// 非号池渠道（理论上不会发生，因为 SourceChannelId 只指向号池渠道）：无法验活，放行。
		return true
	}
	keys := ch.GetKeys()
	if len(keys) == 0 {
		return false
	}
	provider := KeyPoolProvider(strings.TrimPrefix(*ch.Tag, "keypool-"))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		ok, err := ValidateUpstreamKey(provider, k)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func runKeyPoolDailyGiftOnce() {
	if !keyPoolDailyGiftRunning.CompareAndSwap(false, true) {
		return
	}
	defer keyPoolDailyGiftRunning.Store(false)

	ctx := context.Background()
	today := keypoolTodayBeijing()

	// 快速短路：若今天已整体重置过，跳过（避免每分钟空跑）。
	last, _ := model.GetOptionValue(keyPoolDailyGiftOptionKey)
	if last == today {
		return
	}

	resetAlive := 0
	zeroedDead := 0
	aliveCache := make(map[int]bool) // 单次运行内缓存渠道验活结果，避免同渠道被重复验活
	for {
		tokens, err := model.GetEligibleDailyResetTokens(keyPoolDailyGiftBatchSize, today)
		if err != nil {
			logger.LogWarn(ctx, common.LogText("key pool daily gift: fetch tokens failed: %v", err))
			return
		}
		if len(tokens) == 0 {
			break
		}

		for _, t := range tokens {
			// 先按「重置」语义落库：把 remain_quota 归位到「上游 alive → daily_gift_quota / 上游 dead → 0」，
			// 并记录 daily_quota_date=today 作为幂等标记（无论最终是否给满额，今天都算已处理过）。
			alive := true
			if t.SourceChannelId > 0 {
				if v, ok := aliveCache[t.SourceChannelId]; ok {
					alive = v
				} else {
					ch, e := model.GetChannelById(t.SourceChannelId, true) // selectAll=true 取回 key 用于验活
					if e != nil {
						alive = false
					} else {
						alive = keypoolChannelKeyAlive(ch)
					}
					aliveCache[t.SourceChannelId] = alive
				}
			}
			// SourceChannelId==0（无关联上游，例如遗留 token）按「可用」处理，给满额。
			newRemain := 0
			if alive {
				newRemain = t.DailyGiftQuota
			}
			if err := model.ResetTokenDailyQuota(t.Id, newRemain, today); err != nil {
				logger.LogWarn(ctx, common.LogText("key pool daily gift: reset token %d failed: %v", t.Id, err))
				continue
			}
			if alive {
				resetAlive++
			} else {
				zeroedDead++
			}
		}

		// 本批不足一整批，说明已经扫完，结束本轮。
		if len(tokens) < keyPoolDailyGiftBatchSize {
			break
		}
	}

	// 记录「今天已整体重置过」，作为快速短路；真正的幂等保证来自每个 token 的 daily_quota_date。
	if err := model.UpdateOption(keyPoolDailyGiftOptionKey, today); err != nil {
		logger.LogWarn(ctx, common.LogText("key pool daily gift: failed to persist last date: %v", err))
		return
	}
	if common.DebugEnabled {
		logger.LogDebug(ctx, "key pool daily gift done: reset_alive=%d zeroed_dead=%d date=%s", resetAlive, zeroedDead, today)
	}
}
