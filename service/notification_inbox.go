package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// =============================================================================
// hc31 · B211 站内通知中心（服务层）
//
// NotifyUser 是所有用户通知的唯一出口，站内收件箱在这里同步落库，
// 因此「邮件/webhook/bark 发送失败」「用户没配任何渠道」都不会丢失通知。
// =============================================================================

const (
	NotificationDefaultPageSize = 20
	NotificationMaxPageSize     = 100
)

// NotificationPage 收件箱分页响应。
type NotificationPage struct {
	Items       []*model.Notification `json:"items"`
	Total       int64                 `json:"total"`
	Page        int                   `json:"page"`
	PageSize    int                   `json:"page_size"`
	UnreadCount int64                 `json:"unread_count"`
}

// InboxNotificationView 站内通知视图（含渲染后的正文）。
type InboxNotificationView struct {
	Id        int    `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	IsRead    bool   `json:"is_read"`
	CreatedAt int64  `json:"created_at"`
	ReadAt    int64  `json:"read_at"`
}

// RenderNotifyContent 把 dto.Notify 的 {{value}} 占位符替换为实际值，
// 与各渠道发送时使用的规则保持一致，保证站内正文可读。
func RenderNotifyContent(data dto.Notify) string {
	content := data.Content
	for _, value := range data.Values {
		content = strings.Replace(content, dto.ContentValueParam, fmt.Sprintf("%v", value), 1)
	}
	return content
}

// PushInboxNotification 写入一条站内通知（失败只记日志，不影响外部渠道发送）。
func PushInboxNotification(userId int, notifyType string, title string, content string) {
	if userId <= 0 {
		return
	}
	item := &model.Notification{
		UserId:  userId,
		Type:    notifyType,
		Title:   title,
		Content: content,
	}
	if err := item.Insert(); err != nil {
		common.SysLog(common.LogText("failed to persist inbox notification for user %d: %s", userId, err.Error()))
		return
	}
	model.PruneNotifications(userId)
}

// PushInboxNotificationFromDTO 便于调用方直接传 dto.Notify。
func PushInboxNotificationFromDTO(userId int, data dto.Notify) {
	PushInboxNotification(userId, data.Type, data.Title, RenderNotifyContent(data))
}

// NormalizeNotificationPage 校正分页参数。
func NormalizeNotificationPage(page int, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = NotificationDefaultPageSize
	}
	if pageSize > NotificationMaxPageSize {
		pageSize = NotificationMaxPageSize
	}
	return page, pageSize
}

// ListUserNotifications 返回分页收件箱（含未读数）。
func ListUserNotifications(userId int, page int, pageSize int) (*NotificationPage, error) {
	page, pageSize = NormalizeNotificationPage(page, pageSize)
	items, total, err := model.GetUserNotifications(userId, (page-1)*pageSize, pageSize)
	if err != nil {
		return nil, err
	}
	unread, err := model.CountUnreadNotifications(userId)
	if err != nil {
		return nil, err
	}
	return &NotificationPage{
		Items:       items,
		Total:       total,
		Page:        page,
		PageSize:    pageSize,
		UnreadCount: unread,
	}, nil
}

// GetUnreadNotificationCount 未读数（前端角标轮询用）。
func GetUnreadNotificationCount(userId int) (int64, error) {
	return model.CountUnreadNotifications(userId)
}

// MarkUserNotificationRead 标记单条已读。
func MarkUserNotificationRead(userId int, id int) (bool, error) {
	affected, err := model.MarkNotificationRead(userId, id)
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// MarkAllUserNotificationsRead 全部标记已读。
func MarkAllUserNotificationsRead(userId int) (int64, error) {
	return model.MarkAllNotificationsRead(userId)
}
