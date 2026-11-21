package service

import (
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

// =============================================================================
// hc31 · B213 通知事件覆盖表
//
// 把散落在各处的通知触发点收敛成一份可读的「事件 × 渠道 × 默认开关」目录，
// 运营据此判断某类事件会通过哪些渠道送达、受哪个用户偏好开关控制。
// 目录是静态声明的（事件源在代码里，不会因运行期配置增减），
// 用户偏好字段则直接复用 dto.UserSetting 的现有结构。
// =============================================================================

// 受众
const (
	NotifyAudienceUser  = "user"
	NotifyAudienceAdmin = "admin"
)

// 偏好类型
const (
	// NotifyPrefTriStateBool 三态布尔：nil 表示「按默认策略」，false 显式关闭。
	NotifyPrefTriStateBool = "tri_state_bool"
	// NotifyPrefBoolDefaultOff 普通布尔，默认关闭。
	NotifyPrefBoolDefaultOff = "bool_default_off"
	// NotifyPrefChannelType 渠道单选（email/webhook/bark/gotify）。
	NotifyPrefChannelType = "channel_type"
)

// NotifyEventChannel 单个渠道在该事件下的覆盖情况。
type NotifyEventChannel struct {
	Channel string `json:"channel"`
	// DefaultEnabled 默认是否送达。站内收件箱（hc31 · B211）恒为 true。
	DefaultEnabled bool `json:"default_enabled"`
	// Configurable 是否可由用户偏好改变。
	Configurable bool   `json:"configurable"`
	Note         string `json:"note,omitempty"`
}

// NotifyEventCovers 单个通知事件的覆盖条目。
type NotifyEventCovers struct {
	Event string `json:"event"`
	Name  string `json:"name"`
	// NotifyType 实际写入 dto.Notify.Type 的类型码（限流键也用它）。
	NotifyType string `json:"notify_type"`
	// Audience 谁收这条通知：user（全体用户）/ admin（管理员）。
	Audience    string `json:"audience"`
	Description string `json:"description"`
	// PreferenceKey 控制该事件是否发送的 dto.UserSetting 字段名，空表示不受用户偏好控制。
	PreferenceKey string `json:"preference_key"`
	// PreferenceType 偏好字段语义，见 NotifyPref* 常量。
	PreferenceType string `json:"preference_type"`
	// DefaultEnabled 偏好未设置时的默认行为。
	DefaultEnabled bool                 `json:"default_enabled"`
	Channels       []NotifyEventChannel `json:"channels"`
}

// NotifyEventCatalog 覆盖表整体响应。
type NotifyEventCatalog struct {
	GeneratedAt int64  `json:"generated_at"`
	Note        string `json:"note"`
	// Channels 全量渠道顺序，便于前端渲染矩阵列。
	Channels []string `json:"channels"`
	// EventsInboxCovered 恒等于事件总数：站内收件箱对所有事件生效。
	EventsInboxCovered int                 `json:"events_inbox_covered"`
	EventsTotal        int                 `json:"events_total"`
	Events             []NotifyEventCovers `json:"events"`
}

// notifyChannelCatalog 渠道全集（顺序即前端矩阵列序）。
var notifyChannelCatalog = []string{"inbox", dto.NotifyTypeEmail, dto.NotifyTypeWebhook, dto.NotifyTypeBark, dto.NotifyTypeGotify}

// externalChannelNote 外部渠道由用户的 notify_type 单选决定。
const externalChannelNote = "由用户 notify_type 单选决定，未配置该渠道时跳过"

// inboxChannel 站内收件箱：始终写入、不可关闭。
func inboxChannel() NotifyEventChannel {
	return NotifyEventChannel{
		Channel:        "inbox",
		DefaultEnabled: true,
		Configurable:   false,
		Note:           "始终写入站内收件箱（hc31 · B211），不受外部渠道配置影响",
	}
}

func externalChannels() []NotifyEventChannel {
	channels := make([]NotifyEventChannel, 0, len(notifyChannelCatalog)-1)
	for _, ch := range notifyChannelCatalog[1:] {
		channels = append(channels, NotifyEventChannel{
			Channel:        ch,
			DefaultEnabled: ch == dto.NotifyTypeEmail,
			Configurable:   true,
			Note:           externalChannelNote,
		})
	}
	return channels
}

func userEvent(event string, notifyType string, name string, description string, prefKey string, prefType string, defaultEnabled bool) NotifyEventCovers {
	channels := append([]NotifyEventChannel{inboxChannel()}, externalChannels()...)
	return NotifyEventCovers{
		Event:          event,
		Name:           name,
		NotifyType:     notifyType,
		Audience:       NotifyAudienceUser,
		Description:    description,
		PreferenceKey:  prefKey,
		PreferenceType: prefType,
		DefaultEnabled: defaultEnabled,
		Channels:       channels,
	}
}

func adminEvent(event string, notifyType string, name string, description string, prefKey string, prefType string, defaultEnabled bool) NotifyEventCovers {
	item := userEvent(event, notifyType, name, description, prefKey, prefType, defaultEnabled)
	item.Audience = NotifyAudienceAdmin
	return item
}

// notifyEventDefinitions 通知事件目录，新增触发点时必须同步登记这里。
func notifyEventDefinitions() []NotifyEventCovers {
	return []NotifyEventCovers{
		userEvent(
			dto.NotifyTypeQuotaExceed,
			dto.NotifyTypeQuotaExceed,
			"额度不足提醒",
			"用户额度低于预警阈值或调用时余额不足（service/quota.go）",
			"quota_warning_enabled",
			NotifyPrefTriStateBool,
			true,
		),
		userEvent(
			dto.NotifyTypeUsageAnomaly,
			dto.NotifyTypeUsageAnomaly,
			"用量异常提醒",
			"当日消耗远超历史日均（service/usage_alert_task.go，B105），复用余额提醒开关",
			"quota_warning_enabled",
			NotifyPrefTriStateBool,
			true,
		),
		adminEvent(
			dto.NotifyTypeChannelUpdate,
			dto.NotifyTypeChannelUpdate,
			"渠道状态变更",
			"渠道被自动禁用/恢复启用（service/channel.go、provider_plugin_scheduled.go）",
			"channel_notify_enabled",
			NotifyPrefTriStateBool,
			false, // 三态未设置时仅 root 接收
		),
		adminEvent(
			dto.NotifyTypeChannelTest,
			dto.NotifyTypeChannelTest,
			"渠道批量测速结果",
			"全量渠道测试完成后的汇总通知（controller/channel-test.go）",
			"channel_notify_enabled",
			NotifyPrefTriStateBool,
			false,
		),
		adminEvent(
			"upstream_model_update",
			dto.NotifyTypeChannelUpdate, // 复用 channel_update 类型码发送与限流
			"上游模型更新",
			"定时检测到上游模型清单变化（controller/channel_upstream_update.go）",
			"upstream_model_update_notify_enabled",
			NotifyPrefBoolDefaultOff,
			false,
		),
	}
}

// BuildNotifyEventCatalog 生成「事件 × 渠道 × 默认开关」覆盖表。
func BuildNotifyEventCatalog() *NotifyEventCatalog {
	events := notifyEventDefinitions()
	catalog := &NotifyEventCatalog{
		GeneratedAt:        time.Now().Unix(),
		Note:               "站内收件箱（inbox）对所有事件恒开启；email/webhook/bark/gotify 由用户 notify_type 单选决定，且仅在该渠道已配置时送达。",
		Channels:           append([]string{}, notifyChannelCatalog...),
		EventsInboxCovered: len(events),
		EventsTotal:        len(events),
		Events:             events,
	}
	return catalog
}
