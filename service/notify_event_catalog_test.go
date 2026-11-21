package service

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v1.1.0 · hc31 B213 通知事件覆盖表。
func TestBuildNotifyEventCatalog(t *testing.T) {
	catalog := BuildNotifyEventCatalog()
	require.NotNil(t, catalog)

	assert.Equal(t, []string{"inbox", dto.NotifyTypeEmail, dto.NotifyTypeWebhook, dto.NotifyTypeBark, dto.NotifyTypeGotify}, catalog.Channels)
	assert.Equal(t, len(catalog.Events), catalog.EventsTotal)
	assert.Equal(t, catalog.EventsTotal, catalog.EventsInboxCovered)
	assert.NotZero(t, catalog.GeneratedAt)
	assert.NotEmpty(t, catalog.Note)

	seen := make(map[string]struct{}, len(catalog.Events))
	for _, ev := range catalog.Events {
		// 事件码唯一，避免前端矩阵出现重复行。
		_, dup := seen[ev.Event]
		assert.False(t, dup, "duplicate event %s", ev.Event)
		seen[ev.Event] = struct{}{}

		assert.NotEmpty(t, ev.Name)
		assert.NotEmpty(t, ev.NotifyType)
		assert.NotEmpty(t, ev.PreferenceKey)
		assert.NotEmpty(t, ev.Description)
		assert.Contains(t, []string{NotifyAudienceUser, NotifyAudienceAdmin}, ev.Audience)

		// 每个事件都必须覆盖全部渠道（前端按列渲染）。
		require.Len(t, ev.Channels, len(catalog.Channels))
		for i, ch := range ev.Channels {
			assert.Equal(t, catalog.Channels[i], ch.Channel)
			if ch.Channel == "inbox" {
				// B211：站内收件箱恒开启且不可关闭。
				assert.True(t, ch.DefaultEnabled)
				assert.False(t, ch.Configurable)
				continue
			}
			// 外部渠道均可由 notify_type 改变，默认走 email。
			assert.True(t, ch.Configurable)
			assert.Equal(t, ch.Channel == dto.NotifyTypeEmail, ch.DefaultEnabled,
				"非 inbox 渠道只有 email 默认开启，实际 %s=%v", ch.Channel, ch.DefaultEnabled)
			assert.NotEmpty(t, ch.Note)
		}
	}

	// 明确登记的关键事件。
	for _, code := range []string{
		dto.NotifyTypeQuotaExceed,
		dto.NotifyTypeUsageAnomaly,
		dto.NotifyTypeChannelUpdate,
		dto.NotifyTypeChannelTest,
		"upstream_model_update",
	} {
		_, ok := seen[code]
		assert.True(t, ok, "missing event %s", code)
	}
}

// 用户侧事件默认开启、管理员侧事件默认需要显式开启。
func TestNotifyEventCatalogAudienceDefaults(t *testing.T) {
	catalog := BuildNotifyEventCatalog()
	byEvent := make(map[string]NotifyEventCovers, len(catalog.Events))
	for _, ev := range catalog.Events {
		byEvent[ev.Event] = ev
	}

	quota := byEvent[dto.NotifyTypeQuotaExceed]
	assert.Equal(t, NotifyAudienceUser, quota.Audience)
	assert.True(t, quota.DefaultEnabled)
	assert.Equal(t, "quota_warning_enabled", quota.PreferenceKey)
	assert.Equal(t, NotifyPrefTriStateBool, quota.PreferenceType)

	// 用量异常复用余额提醒开关（与 usage_alert_task 的实现一致）。
	anomaly := byEvent[dto.NotifyTypeUsageAnomaly]
	assert.Equal(t, "quota_warning_enabled", anomaly.PreferenceKey)
	assert.Equal(t, NotifyPrefTriStateBool, anomaly.PreferenceType)

	channelUpdate := byEvent[dto.NotifyTypeChannelUpdate]
	assert.Equal(t, NotifyAudienceAdmin, channelUpdate.Audience)
	assert.Equal(t, "channel_notify_enabled", channelUpdate.PreferenceKey)
	assert.False(t, channelUpdate.DefaultEnabled) // 三态未设置时仅 root 接收

	upstream := byEvent["upstream_model_update"]
	assert.Equal(t, NotifyAudienceAdmin, upstream.Audience)
	assert.Equal(t, "upstream_model_update_notify_enabled", upstream.PreferenceKey)
	assert.Equal(t, NotifyPrefBoolDefaultOff, upstream.PreferenceType)
	assert.False(t, upstream.DefaultEnabled)
	// 实际发送时复用的类型码。
	assert.Equal(t, dto.NotifyTypeChannelUpdate, upstream.NotifyType)
}
