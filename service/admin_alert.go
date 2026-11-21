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

// 运维告警（v1.1.0 M1 · B71/B72/B73）的公共收口：
//   - 指标计数永远发生（Prometheus 面板不依赖通知开关）；
//   - 通知统一发给 root 用户，按 kind 做滑动窗口限频，避免一次事故刷爆邮箱；
//   - 通知内容用英文固定文案（与现有告警类通知一致），语言包不做覆盖。
//
// 通知 kind 是限频键，采用 "域:对象" 形式，例如 billing:zero_charge、
// monitor:channel_error_rate:3、system_task:log_cleanup。

// alertNotifyThrottleWindow 是一条告警通知的限频窗口。
const alertNotifyThrottleWindow = time.Hour

// alertThrottle 按 kind 维护滑动窗口内的已发送时间，超过配额即静默。
// 静默只影响通知：指标与日志仍然逐条记录，事后排查不受限频影响。
type alertThrottle struct {
	mu      sync.Mutex
	windows map[string][]time.Time
}

var adminAlertLimiter = &alertThrottle{windows: make(map[string][]time.Time)}

// allow 报告 kind 在 now 之前的窗口内是否还有通知配额。
func (t *alertThrottle) allow(kind string, limit int, now time.Time) bool {
	if limit <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.windows == nil {
		t.windows = make(map[string][]time.Time)
	}
	window := t.windows[kind]
	cutoff := now.Add(-alertNotifyThrottleWindow)
	keep := window[:0]
	for _, at := range window {
		if at.After(cutoff) {
			keep = append(keep, at)
		}
	}
	if len(keep) >= limit {
		t.windows[kind] = keep
		return false
	}
	t.windows[kind] = append(keep, now)

	// kind 集合虽然有限（渠道告警按 id 展开为有限个），仍做一次廉价回收，
	// 防止长期运行下残留空窗口。
	if len(t.windows) > 512 {
		for key, w := range t.windows {
			if len(w) == 0 {
				delete(t.windows, key)
			}
		}
	}
	return true
}

// resetAlertLimiterForTest 仅供测试使用。
func resetAlertLimiterForTest() {
	adminAlertLimiter.mu.Lock()
	defer adminAlertLimiter.mu.Unlock()
	adminAlertLimiter.windows = make(map[string][]time.Time)
}

// logAlert 把告警写入错误日志。ctx 可以是 *gin.Context 或 context.Background()，
// 为 nil 时退化为 SysError（后台巡检场景不传 ctx）。
func logAlert(ctx context.Context, msg string) {
	if ctx == nil {
		common.SysError(msg)
		return
	}
	logger.LogError(ctx, msg)
}

// notifyAdminAlert 把一条运维告警异步推给 root 用户，命中 kind 限频则跳过。
// ctx 同 logAlert；subject/content 为英文固定文案。
func notifyAdminAlert(ctx context.Context, kind string, subject string, body string) {
	if !common.BillingAlertEnabled {
		return
	}
	if !adminAlertLimiter.allow(kind, common.AlertNotifyMaxPerHour, time.Now()) {
		logger.LogWarn(ctx, common.LogText("admin alert %s suppressed by rate limit: %s", kind, subject))
		return
	}
	gopool.Go(func() {
		sent := notifyAdmins(dto.NotifyTypeAdminAlert, func(role int, _ dto.UserSetting) bool {
			return role == common.RoleRootUser
		}, func(_ string) (string, string) {
			return subject, body
		})
		if sent == 0 {
			logger.LogWarn(ctx, common.LogText("admin alert %s reached no root recipient", subject))
		}
	})
}

// alertSystemTaskFailure 是定时任务失败告警（v1.1.0 M1 · B72）的入口：
// 失败除了落库，还要让 root 当场知道，指标面板同步可见。
func alertSystemTaskFailure(task *model.SystemTask, taskErr error) {
	if task == nil {
		return
	}
	kind := fmt.Sprintf("system_task:%s", task.Type)
	subject := "System task failed: " + task.Type
	body := fmt.Sprintf(
		"Scheduled system task %s (type %s) failed at %s.\n\nError:\n%s\n\nThe task record in the console keeps the full history.",
		task.TaskID, task.Type, time.Now().Format(time.RFC3339), taskErr.Error())
	logAlert(nil, common.LogText("admin alert system task failure: type %s, task %s, err %s", task.Type, task.TaskID, taskErr.Error()))
	notifyAdminAlert(nil, kind, subject, body)
}
