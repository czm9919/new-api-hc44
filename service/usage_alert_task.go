package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

// ---------------------------------------------------------------------------
// B105 用量异常提醒定时任务
// ---------------------------------------------------------------------------

const (
	usageAlertTickInterval = 1 * time.Hour
	// usageAlertMaxNotifiedPerRun 单轮最多提醒的用户数，防止异常日把邮件通道打爆。
	usageAlertMaxNotifiedPerRun = 50
)

var (
	usageAlertOnce    sync.Once
	usageAlertRunning atomic.Bool
	// usageAlertNotifiedToday 已提醒去重表：key 为 "yyyy-mm-dd|userId"。
	// 仅内存生效（主节点重启后可能对个别用户再提醒一次，可接受），配合
	// CheckNotificationLimit 的速率闸门双重防骚扰。
	usageAlertNotifiedToday = sync.Map{}
)

// StartUsageAlertTask 启动用量异常提醒任务（B105）。
// 仅主节点执行，每小时检查一次：把「当日消耗」与「近 N 天有消耗天的日均」比较，
// 超过配置倍率即向用户发送提醒。用户级开关复用 quota_warning_enabled。
func StartUsageAlertTask() {
	usageAlertOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), common.LogText("usage alert task started: tick=%s", usageAlertTickInterval))
			ticker := time.NewTicker(usageAlertTickInterval)
			defer ticker.Stop()
			for range ticker.C {
				runUsageAlertOnce()
			}
		})
	})
}

// usageAlertLocation 统一使用北京时区（UTC+8）计算自然日边界。
func usageAlertLocation() *time.Location {
	loc, err := time.LoadLocation(beijingLocation)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return loc
}

// usageAlertDayStart 返回以北京时间为准的某天 0 点的 unix 时间戳。
func usageAlertDayStart(now time.Time, daysAgo int) (int64, int64) {
	loc := usageAlertLocation()
	todayStart := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	dayStart := todayStart.AddDate(0, 0, -daysAgo)
	return dayStart.Unix(), dayStart.AddDate(0, 0, 1).Unix()
}

func runUsageAlertOnce() {
	if !usageAlertRunning.CompareAndSwap(false, true) {
		return
	}
	defer usageAlertRunning.Store(false)

	cfg := operation_setting.GetUsageAlertSetting()
	if !cfg.Enabled {
		return
	}

	ctx := context.Background()
	now := time.Now()
	// 去重表按天累积，跨日清理旧 key，避免长期运行内存无界增长。
	dateKey := now.In(usageAlertLocation()).Format("2006-01-02")
	usageAlertNotifiedToday.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && !strings.HasPrefix(k, dateKey+"|") {
			usageAlertNotifiedToday.Delete(key)
		}
		return true
	})

	// 采集基线：近 BaselineDays 个完整自然日（不含今天）的用户日消耗。
	baselineDays := cfg.BaselineDays
	if baselineDays <= 0 {
		baselineDays = 7
	}
	baseline := make([]map[int]int64, baselineDays)
	for i := 0; i < baselineDays; i++ {
		start, end := usageAlertDayStart(now, i+1) // i+1: 昨天往前数
		m, err := model.SumQuotaByUserInWindow(start, end)
		if err != nil {
			logger.LogWarn(ctx, common.LogText("usage alert: fetch baseline day %d failed: %v", i+1, err))
			return
		}
		baseline[i] = m
	}

	// 当日消耗（今天 0 点至今）。
	todayStart, _ := usageAlertDayStart(now, 0)
	today, err := model.SumQuotaByUserInWindow(todayStart, now.Unix()+1)
	if err != nil {
		logger.LogWarn(ctx, common.LogText("usage alert: fetch today usage failed: %v", err))
		return
	}
	if len(today) == 0 {
		return
	}

	notified := 0
	for userId, todayQuota := range today {
		if notified >= usageAlertMaxNotifiedPerRun {
			break
		}
		dedupeKey := dateKey + "|" + strconv.Itoa(userId)
		if _, sent := usageAlertNotifiedToday.Load(dedupeKey); sent {
			continue
		}
		baselineDaily := make([]int64, len(baseline))
		for i, m := range baseline {
			baselineDaily[i] = m[userId]
		}
		decision := operation_setting.EvaluateUsageAnomaly(
			todayQuota, baselineDaily, cfg.Multiplier, cfg.MinDailyQuota)
		if !decision.Triggered {
			continue
		}
		if err := sendUsageAlertNotify(userId, todayQuota, decision); err != nil {
			logger.LogWarn(ctx, common.LogText("usage alert: notify user %d failed: %v", userId, err))
			continue
		}
		usageAlertNotifiedToday.Store(dedupeKey, struct{}{})
		notified++
	}

	if notified > 0 {
		logger.LogInfo(ctx, common.LogText("usage alert: sent %d notifications on %s", notified, dateKey))
	}
}

// sendUsageAlertNotify 按用户的语言渲染并发送异常提醒；
// 用户级开关复用 quota_warning_enabled（显式关闭余额提醒的用户同样不收异常提醒）。
func sendUsageAlertNotify(userId int, todayQuota int64, decision operation_setting.UsageAlertDecision) error {
	user, err := model.GetUserById(userId, true)
	if err != nil {
		return err
	}
	userSetting := user.GetSetting()
	if userSetting.QuotaWarningEnabled != nil && !*userSetting.QuotaWarningEnabled {
		return nil
	}
	lang := userSetting.Language
	title := i18n.Translate(lang, i18n.MsgNotifyUsageAlertTitle)
	content := i18n.Translate(lang, i18n.MsgNotifyUsageAlertContent, map[string]any{
		"Today":    logger.FormatQuota(int(todayQuota)),
		"Baseline": logger.FormatQuota(int(decision.BaselineAverage)),
		"Ratio":    fmt.Sprintf("%.1f", decision.Ratio),
	})
	if err := NotifyUser(userId, user.Email, userSetting,
		dto.NewNotify(dto.NotifyTypeUsageAnomaly, title, content, nil)); err != nil {
		return err
	}
	if cfg := operation_setting.GetUsageAlertSetting(); cfg.NotifyAdmins {
		NotifyChannelWatchers(dto.NotifyTypeUsageAnomaly, func(l string) (string, string) {
			t := i18n.Translate(l, i18n.MsgNotifyUsageAlertTitle)
			c := i18n.Translate(l, i18n.MsgNotifyUsageAlertContent, map[string]any{
				"Today":    logger.FormatQuota(int(todayQuota)),
				"Baseline": logger.FormatQuota(int(decision.BaselineAverage)),
				"Ratio":    fmt.Sprintf("%.1f", decision.Ratio),
			})
			return t, c
		})
	}
	return nil
}
