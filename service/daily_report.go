package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

// ---------------------------------------------------------------------------
// 每日战报
//
// 目标只有一个：早上打开手机就知道「昨天有没有出事、有没有人在用」，
// 不需要翻日志、不需要开面板。
//
// 实现刻意做得很薄：
//   - 30 秒一次 tick，命中配置的时刻且当天还没发过才发；
//   - 错过窗口（比如 9 点的战报，进程 15 点才起来）不再补发，避免中午收到
//     「昨日战报」造成误解；
//   - 发送失败不写「已发」标记，下一个 tick 自然重试。
// ---------------------------------------------------------------------------

const (
	dailyReportTickInterval = 30 * time.Second
	// dailyReportSendGrace 命中时刻后多久内仍允许发送。超过就当作错过。
	dailyReportSendGrace = 3 * time.Hour
)

var (
	dailyReportOnce     sync.Once
	dailyReportRunning  atomic.Bool
	dailyReportLastDate atomic.Value // string: yyyy-mm-dd（北京时间）
)

// StartDailyReportTask 启动每日战报任务，仅主节点执行。
func StartDailyReportTask() {
	dailyReportOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), common.LogText("daily report task started: tick=%s", dailyReportTickInterval))
			ticker := time.NewTicker(dailyReportTickInterval)
			defer ticker.Stop()
			for range ticker.C {
				runDailyReportOnce(time.Now())
			}
		})
	})
}

func dailyReportLocation() *time.Location {
	loc, err := time.LoadLocation(beijingLocation)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return loc
}

// shouldSendDailyReport 判断此刻是否该发今天的战报。
// 抽成纯函数是为了能直接测时间边界（跨天、错过窗口、已发过）。
func shouldSendDailyReport(now time.Time, hour, minute int, lastSentDate string) bool {
	loc := now.Location()
	scheduled := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	if now.Before(scheduled) {
		return false
	}
	if now.Sub(scheduled) > dailyReportSendGrace {
		return false
	}
	return lastSentDate != now.Format("2006-01-02")
}

func runDailyReportOnce(now time.Time) {
	setting := operation_setting.GetDailyReportSetting()
	if !setting.Enabled {
		return
	}
	now = now.In(dailyReportLocation())
	lastDate, _ := dailyReportLastDate.Load().(string)
	if !shouldSendDailyReport(now, setting.Hour, setting.Minute, lastDate) {
		return
	}
	// 同一时刻只允许一个发送流程（tick 很密，构建战报要查库）。
	if !dailyReportRunning.CompareAndSwap(false, true) {
		return
	}
	defer dailyReportRunning.Store(false)

	subject, body, err := buildDailyReport(now, setting.TopUsers)
	if err != nil {
		logger.LogError(context.Background(), common.LogText("daily report build failed: %s", err.Error()))
		return
	}
	// 用独立的通知类型：战报每天一条，不能占用事故告警的限流配额。
	sent := notifyAdmins(dto.NotifyTypeDailyReport, func(role int, _ dto.UserSetting) bool {
		return role == common.RoleRootUser
	}, func(_ string) (string, string) {
		return subject, body
	})
	if sent == 0 {
		// 没有配置任何推送方式时不要卡住，记一条日志即可，明天照常尝试。
		logger.LogWarn(context.Background(), common.LogText("daily report reached no root recipient, body: %s", body))
	} else {
		logger.LogInfo(context.Background(), common.LogText("daily report sent to %d root user(s)", sent))
	}
	dailyReportLastDate.Store(now.Format("2006-01-02"))
}

// buildDailyReport 生成「昨日」的运营汇总。
func buildDailyReport(now time.Time, topN int) (string, string, error) {
	loc := now.Location()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	yesterdayStart := todayStart.AddDate(0, 0, -1)

	summary, err := model.GetDailySummary(yesterdayStart.Unix(), todayStart.Unix(), topN)
	if err != nil {
		return "", "", err
	}
	channels, err := model.CountChannelsByStatus()
	if err != nil {
		// 渠道统计失败不该让整份战报发不出去，用零值继续。
		logger.LogWarn(context.Background(), common.LogText("daily report channel stats failed: %s", err.Error()))
	}

	dateLabel := yesterdayStart.Format("2006-01-02")
	subject := fmt.Sprintf("昨日战报 %s：%d 次请求 · %s", dateLabel, summary.Requests, formatQuotaUSD(summary.Quota))

	var b strings.Builder
	fmt.Fprintf(&b, "昨日战报 · %s\n\n", dateLabel)
	if summary.Requests == 0 {
		b.WriteString("昨天没有任何计费调用。\n")
	} else {
		fmt.Fprintf(&b, "请求 %s 次 · 消耗 %s\n", formatQuotaCount(summary.Requests), formatQuotaUSD(summary.Quota))
		fmt.Fprintf(&b, "活跃用户 %d 人 · 单次均耗 %s\n",
			summary.ActiveUsers, formatQuotaUSD(avgQuota(summary.Quota, summary.Requests)))
	}
	fmt.Fprintf(&b, "新增用户 %d 人 · 新增 QQ 绑定 %d 人\n", summary.NewUsers, summary.NewBinds)

	if len(summary.TopUsers) > 0 {
		b.WriteString("\n消耗前三：\n")
		for i, row := range summary.TopUsers {
			name := strings.TrimSpace(row.Username)
			if name == "" {
				name = fmt.Sprintf("用户 %d", row.UserId)
			}
			fmt.Fprintf(&b, "%d. %s　%s（%d 次）\n",
				i+1, name, formatQuotaUSD(int64(row.Quota)), row.Requests)
		}
	}

	fmt.Fprintf(&b, "\n渠道：启用 %d · 自动禁用 %d · 手动禁用 %d\n",
		channels.Enabled, channels.Auto, channels.Manual)
	if channels.Enabled == 0 && channels.Total > 0 {
		b.WriteString("⚠️ 当前没有任何可用渠道，新请求会直接失败。\n")
	}

	return subject, b.String(), nil
}

func avgQuota(total int64, requests int64) int64 {
	if requests <= 0 {
		return 0
	}
	return total / requests
}

// formatQuotaUSD 把内部额度换算成美元展示。
func formatQuotaUSD(quota int64) string {
	perUnit := common.QuotaPerUnit
	if perUnit <= 0 {
		return "—"
	}
	return fmt.Sprintf("$%.4f", float64(quota)/perUnit)
}

// formatQuotaCount 给大数字加千分位，手机上一眼能读。
func formatQuotaCount(n int64) string {
	raw := fmt.Sprintf("%d", n)
	if len(raw) <= 3 {
		return raw
	}
	var parts []string
	for len(raw) > 3 {
		parts = append([]string{raw[len(raw)-3:]}, parts...)
		raw = raw[:len(raw)-3]
	}
	parts = append([]string{raw}, parts...)
	return strings.Join(parts, ",")
}
