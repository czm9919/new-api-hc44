package dto

type Notify struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Values  []any  `json:"values"`
}

const ContentValueParam = "{{value}}"

const (
	NotifyTypeQuotaExceed   = "quota_exceed"
	NotifyTypeChannelUpdate = "channel_update"
	NotifyTypeChannelTest   = "channel_test"
	// NotifyTypeUsageAnomaly 用量异常提醒（B105）：当日消耗远超历史日均。
	NotifyTypeUsageAnomaly = "usage_anomaly"

	// 以下三个类型专供系统级运维通知。它们同时是 CheckNotificationLimit 的
	// 限流键（默认每 10 分钟 2 条），必须彼此独立：否则一天一次的战报会把
	// 事故告警的配额吃掉，故障恢复通知就会被静默丢弃（hc46 真机验证发现）。
	NotifyTypeAdminAlert  = "admin_alert"  // 事故类告警：内存、任务失败、计费异常
	NotifyTypeDailyReport = "daily_report" // 每日战报
	NotifyTypePoolHealth  = "pool_health"  // 号池熔断/恢复
)

func NewNotify(t string, title string, content string, values []any) Notify {
	return Notify{
		Type:    t,
		Title:   title,
		Content: content,
		Values:  values,
	}
}
