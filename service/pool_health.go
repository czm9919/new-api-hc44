package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/bytedance/gopkg/util/gopool"
)

// ---------------------------------------------------------------------------
// 号池健康守卫
//
// 渠道自动禁用与定时探测在 fork 里已经有了（monitor_setting + 渠道巡检任务），
// 缺的是「最后一根也被拔掉时有人知道」：
//   - processChannelError 只会通知「关注了这个渠道」的人，root 默认没关注；
//   - 巡检正常跑完、全部渠道都被禁用，也不会有人主动告警。
//
// 这里只做状态判定与告警，不替代巡检：
//   - 每 2 分钟按分组统计可用渠道数；
//   - 某分组从「有可用」变成「0 个可用」→ 立即告警（熔断）；
//   - 恢复 → 发一条恢复通知；
//   - 自动禁用占比过高（>= 60%）→ 提前预警，别等全挂。
// 告警只在状态跳变时发一次，并复用 adminAlertLimiter 的限频，不会刷屏。
// ---------------------------------------------------------------------------

const (
	poolHealthTickInterval = 2 * time.Minute
	// poolHealthDegradedRatio 自动禁用占比达到该百分比就提前预警。
	poolHealthDegradedRatio = 60
)

type poolGroupStat struct {
	Group          string
	Enabled        int
	AutoDisabled   int
	ManualDisabled int
	Total          int
}

type poolHealthAlert struct {
	Group   string
	State   string // down / degraded / ok
	Subject string
	Body    string
}

var (
	poolHealthOnce      sync.Once
	poolHealthStateMu   sync.Mutex
	poolHealthLastState = make(map[string]string)
)

// StartPoolHealthGuard 启动号池健康守卫，仅主节点执行。
func StartPoolHealthGuard() {
	poolHealthOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), common.LogText("pool health guard started: tick=%s", poolHealthTickInterval))
			// 启动后先等一个周期，避开刚启动时数据库/迁移还没就绪的窗口。
			time.Sleep(30 * time.Second)
			ticker := time.NewTicker(poolHealthTickInterval)
			defer ticker.Stop()
			for range ticker.C {
				runPoolHealthOnce()
			}
		})
	})
}

func runPoolHealthOnce() {
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		logger.LogWarn(context.Background(), common.LogText("pool health: list channels failed: %s", err.Error()))
		return
	}
	stats := collectPoolGroupStats(channels)
	if len(stats) == 0 {
		return
	}

	poolHealthStateMu.Lock()
	next, alerts := evaluatePoolHealth(stats, poolHealthLastState)
	poolHealthLastState = next
	poolHealthStateMu.Unlock()

	for _, alert := range alerts {
		notifyPoolAlert(alert)
	}
}

// collectPoolGroupStats 按分组汇总渠道状态。
// 一个渠道可以属于多个分组（group 字段逗号分隔），每个分组都算一次。
func collectPoolGroupStats(channels []*model.Channel) map[string]*poolGroupStat {
	stats := make(map[string]*poolGroupStat)
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		groups := channel.GetGroups()
		if len(groups) == 0 {
			groups = []string{"default"}
		}
		for _, group := range groups {
			if group == "" {
				group = "default"
			}
			stat, ok := stats[group]
			if !ok {
				stat = &poolGroupStat{Group: group}
				stats[group] = stat
			}
			stat.Total++
			switch channel.Status {
			case common.ChannelStatusEnabled:
				stat.Enabled++
			case common.ChannelStatusAutoDisabled:
				stat.AutoDisabled++
			case common.ChannelStatusManuallyDisabled:
				stat.ManualDisabled++
			}
		}
	}
	return stats
}

// evaluatePoolHealth 计算状态并返回需要发出的告警（只在跳变时产生）。
// prev 为上一轮的状态表，返回的 next 供下一轮使用。
func evaluatePoolHealth(stats map[string]*poolGroupStat, prev map[string]string) (map[string]string, []poolHealthAlert) {
	next := make(map[string]string, len(stats))
	alerts := make([]poolHealthAlert, 0, 2)

	for group, stat := range stats {
		if stat.Total == 0 {
			continue
		}
		state := "ok"
		switch {
		case stat.Enabled == 0:
			state = "down"
		case stat.AutoDisabled*100 >= stat.Total*poolHealthDegradedRatio && stat.AutoDisabled > 0:
			state = "degraded"
		}
		next[group] = state

		before, known := prev[group]
		if known && before == state {
			continue
		}
		switch state {
		case "down":
			alerts = append(alerts, poolHealthAlert{
				Group:   group,
				State:   state,
				Subject: fmt.Sprintf("[号池熔断] 分组 %s 已无可用渠道", group),
				Body: fmt.Sprintf(
					"分组 %s 的渠道全部不可用，新请求会直接失败。\n\n"+
						"启用 0 · 自动禁用 %d · 手动禁用 %d · 共 %d 个\n\n"+
						"处理：到「渠道」页看自动禁用的原因（401/余额/超时）；"+
						"key 失效就换 key，恢复了再点启用。",
					group, stat.AutoDisabled, stat.ManualDisabled, stat.Total),
			})
		case "degraded":
			alerts = append(alerts, poolHealthAlert{
				Group:   group,
				State:   state,
				Subject: fmt.Sprintf("[号池预警] 分组 %s 自动禁用 %d/%d", group, stat.AutoDisabled, stat.Total),
				Body: fmt.Sprintf(
					"分组 %s 的渠道已有 %d/%d 被自动禁用，只剩 %d 个可用。\n"+
						"再多挂几个就是全池不可用，建议现在补 key。",
					group, stat.AutoDisabled, stat.Total, stat.Enabled),
			})
		case "ok":
			// 只在「从坏变好」时发恢复通知，避免首轮探测就发一堆「正常」。
			if known && (before == "down" || before == "degraded") {
				alerts = append(alerts, poolHealthAlert{
					Group:   group,
					State:   state,
					Subject: fmt.Sprintf("[号池恢复] 分组 %s 可用渠道 %d 个", group, stat.Enabled),
					Body: fmt.Sprintf(
						"分组 %s 已恢复：可用 %d 个，自动禁用 %d 个，手动禁用 %d 个。",
						group, stat.Enabled, stat.AutoDisabled, stat.ManualDisabled),
				})
			}
		}
	}

	// 已被删除的分组不再保留状态，避免下次同名分组被误判为「本来就坏」。
	for group := range prev {
		if _, ok := next[group]; !ok {
			delete(prev, group)
		}
	}
	return next, alerts
}

// notifyPoolAlert 把号池告警推给 root。
// 不走 BillingAlertEnabled（那个开关是给计费告警用的），但走同一套限频，
// 防止上游抖动导致十几分钟内反复告警。
func notifyPoolAlert(alert poolHealthAlert) {
	kind := "pool:" + alert.State + ":" + alert.Group
	if !adminAlertLimiter.allow(kind, 2, time.Now()) {
		logger.LogWarn(context.Background(), common.LogText("pool alert %s suppressed by rate limit", alert.Subject))
		return
	}
	logger.LogError(context.Background(), common.LogText("pool alert: %s", alert.Subject))
	gopool.Go(func() {
		// 号池告警用自己的限流桶：熔断与恢复是一对，不能被其它告警挤掉。
		sent := notifyAdmins(dto.NotifyTypePoolHealth, func(role int, _ dto.UserSetting) bool {
			return role == common.RoleRootUser
		}, func(_ string) (string, string) {
			return alert.Subject, alert.Body
		})
		if sent == 0 {
			logger.LogWarn(context.Background(), common.LogText("pool alert reached no root recipient: %s", alert.Subject))
		}
	})
}
