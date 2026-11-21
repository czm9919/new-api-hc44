package service

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v1.1.0 · hc31 B211 站内通知中心。
func setupNotificationTables(t *testing.T) {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.Notification{}))
	t.Cleanup(func() {
		model.DB.Where("1 = 1").Delete(&model.Notification{})
	})
}

// 占位符 {{value}} 按顺序替换，与外部渠道的渲染规则一致。
func TestRenderNotifyContent(t *testing.T) {
	data := dto.NewNotify(dto.NotifyTypeQuotaExceed, "额度不足", "当前额度 {{value}}，低于阈值 {{value}}", []any{100, 50})
	assert.Equal(t, "当前额度 100，低于阈值 50", RenderNotifyContent(data))
}

// PushInboxNotificationFromDTO 落库 + 未读计数。
func TestPushInboxNotificationFromDTO(t *testing.T) {
	setupNotificationTables(t)

	PushInboxNotificationFromDTO(123001, dto.NewNotify(dto.NotifyTypeQuotaExceed, "标题", "内容 {{value}}", []any{"x"}))
	PushInboxNotificationFromDTO(123001, dto.NewNotify(dto.NotifyTypeUsageAnomaly, "标题2", "内容2", nil))

	items, total, err := model.GetUserNotifications(123001, 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 2)
	// 未读在前、时间倒序：最新的先返回。
	assert.Equal(t, "标题2", items[0].Title)
	assert.False(t, items[0].IsRead)
	assert.Equal(t, "内容 x", items[1].Content)

	unread, err := model.CountUnreadNotifications(123001)
	require.NoError(t, err)
	assert.Equal(t, int64(2), unread)

	// 非法 userId 直接忽略。
	PushInboxNotificationFromDTO(0, dto.NewNotify("t", "t", "t", nil))
	_, total, _ = model.GetUserNotifications(123001, 0, 20)
	assert.Equal(t, int64(2), total)
}

// 单条已读 / 全部已读 / 只能操作自己的记录。
func TestMarkNotificationsRead(t *testing.T) {
	setupNotificationTables(t)

	PushInboxNotification(123002, dto.NotifyTypeQuotaExceed, "a", "a")
	PushInboxNotification(123002, dto.NotifyTypeQuotaExceed, "b", "b")
	PushInboxNotification(123003, dto.NotifyTypeQuotaExceed, "other", "other")

	items, _, err := model.GetUserNotifications(123002, 0, 20)
	require.NoError(t, err)
	require.Len(t, items, 2)

	// 越权标记他人通知：无效。
	updated, err := MarkUserNotificationRead(123003, items[0].Id)
	require.NoError(t, err)
	assert.False(t, updated)

	updated, err = MarkUserNotificationRead(123002, items[0].Id)
	require.NoError(t, err)
	assert.True(t, updated)
	// 重复标记返回 false。
	updated, err = MarkUserNotificationRead(123002, items[0].Id)
	require.NoError(t, err)
	assert.False(t, updated)

	affected, err := MarkAllUserNotificationsRead(123002)
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	unread, err := model.CountUnreadNotifications(123002)
	require.NoError(t, err)
	assert.Equal(t, int64(0), unread)
	// 其他用户的通知不受影响。
	unread, err = model.CountUnreadNotifications(123003)
	require.NoError(t, err)
	assert.Equal(t, int64(1), unread)
}

// 分页：未读优先 + 页码/页大小校正。
func TestListUserNotificationsPaging(t *testing.T) {
	setupNotificationTables(t)
	for i := 0; i < 5; i++ {
		PushInboxNotification(123004, dto.NotifyTypeQuotaExceed, "n", "n")
	}
	items, _, err := model.GetUserNotifications(123004, 0, 20)
	require.NoError(t, err)
	// 标记最新两条为已读。
	require.NoError(t, model.DB.Model(&model.Notification{}).
		Where("user_id = ? AND id IN (?, ?)", 123004, items[0].Id, items[1].Id).
		Update("is_read", true).Error)

	page, err := ListUserNotifications(123004, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, page.Page)
	assert.Equal(t, 20, page.PageSize)
	assert.Equal(t, int64(5), page.Total)
	assert.Equal(t, int64(3), page.UnreadCount)
	require.Len(t, page.Items, 5)
	// 未读在前。
	assert.False(t, page.Items[0].IsRead)
	assert.True(t, page.Items[4].IsRead)

	// 超大页大小被截断。
	page, err = ListUserNotifications(123004, 2, 500)
	require.NoError(t, err)
	assert.Equal(t, 2, page.Page)
	assert.Equal(t, 100, page.PageSize)
}

// 收件箱上限裁剪：超出 NotificationMaxPerUser 时删除最旧的记录。
func TestPruneNotifications(t *testing.T) {
	setupNotificationTables(t)
	for i := 0; i < model.NotificationMaxPerUser+10; i++ {
		PushInboxNotification(123005, dto.NotifyTypeQuotaExceed, "n", "n")
	}
	_, total, err := model.GetUserNotifications(123005, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(model.NotificationMaxPerUser), total)
}

// NotifyUser 漏斗：即使未配置外部渠道（无邮箱），站内通知也已落库。
func TestNotifyUserPersistsInbox(t *testing.T) {
	setupNotificationTables(t)

	// InitEnv 只在 main 里调用，单测进程里 NotifyLimitCount 为 0，需显式放开。
	constant.NotifyLimitCount = 10
	t.Cleanup(func() { constant.NotifyLimitCount = 0 })

	// NotifyType 为空 → 默认 email，邮箱为空 → 跳过外部发送但不应报错。
	err := NotifyUser(123006, "", dto.UserSetting{},
		dto.NewNotify(dto.NotifyTypeQuotaExceed, "漏斗", "漏斗内容", nil))
	require.NoError(t, err)

	page, err := ListUserNotifications(123006, 1, 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "漏斗", page.Items[0].Title)
	assert.Equal(t, dto.NotifyTypeQuotaExceed, page.Items[0].Type)
	assert.Equal(t, int64(1), page.UnreadCount)
}
