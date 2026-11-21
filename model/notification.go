package model

// =============================================================================
// hc31 · B211 站内通知中心
//
// 所有经 NotifyUser 漏斗发出的用户通知都会同步落一份站内收件箱记录，
// 用户未配置任何外部通知渠道时也能在站内看到历史通知。
// =============================================================================

import (
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	// NotificationMaxPerUser 单用户收件箱上限，超出时裁剪最旧记录。
	NotificationMaxPerUser = 200
)

type Notification struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"index:idx_notification_user_read,priority:1"`
	Type      string `json:"type" gorm:"type:varchar(64);default:''"`
	Title     string `json:"title" gorm:"type:varchar(255);default:''"`
	Content   string `json:"content" gorm:"type:text"`
	IsRead    bool   `json:"is_read" gorm:"index:idx_notification_user_read,priority:2;default:false"`
	CreatedAt int64  `json:"created_at" gorm:"bigint;index"`
	ReadAt    int64  `json:"read_at" gorm:"bigint;default:0"`
}

func (n *Notification) Insert() error {
	n.CreatedAt = time.Now().Unix()
	return DB.Create(n).Error
}

// GetUserNotifications 分页返回用户收件箱，未读在前、按时间倒序。
func GetUserNotifications(userId int, offset int, limit int) ([]*Notification, int64, error) {
	var items []*Notification
	var total int64
	err := DB.Model(&Notification{}).Where("user_id = ?", userId).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 20
	}
	err = DB.Where("user_id = ?", userId).
		Order("is_read asc, created_at desc, id desc").
		Limit(limit).Offset(offset).
		Find(&items).Error
	return items, total, err
}

// CountUnreadNotifications 用户未读通知数。
func CountUnreadNotifications(userId int) (int64, error) {
	var count int64
	err := DB.Model(&Notification{}).Where("user_id = ? AND is_read = ?", userId, false).Count(&count).Error
	return count, err
}

// MarkNotificationRead 标记单条为已读，只允许操作自己的记录。
func MarkNotificationRead(userId int, id int) (int64, error) {
	result := DB.Model(&Notification{}).
		Where("user_id = ? AND id = ? AND is_read = ?", userId, id, false).
		Updates(map[string]any{"is_read": true, "read_at": time.Now().Unix()})
	return result.RowsAffected, result.Error
}

// MarkAllNotificationsRead 全部标记已读，返回影响条数。
func MarkAllNotificationsRead(userId int) (int64, error) {
	result := DB.Model(&Notification{}).
		Where("user_id = ? AND is_read = ?", userId, false).
		Updates(map[string]any{"is_read": true, "read_at": time.Now().Unix()})
	return result.RowsAffected, result.Error
}

// PruneNotifications 裁剪用户收件箱到上限以内（保留最新的）。
func PruneNotifications(userId int) {
	var count int64
	if err := DB.Model(&Notification{}).Where("user_id = ?", userId).Count(&count).Error; err != nil || count <= NotificationMaxPerUser {
		return
	}
	// 找出按时间倒序第 N 条的时间戳，删除比它更早的记录。
	var oldest Notification
	if err := DB.Where("user_id = ?", userId).
		Order("created_at desc, id desc").
		Offset(NotificationMaxPerUser - 1).
		First(&oldest).Error; err != nil {
		return
	}
	if err := DB.Where("user_id = ? AND (created_at < ? OR (created_at = ? AND id < ?))",
		userId, oldest.CreatedAt, oldest.CreatedAt, oldest.Id).Delete(&Notification{}).Error; err != nil {
		common.SysLog(common.LogText("failed to prune notifications for user %d: %s", userId, err.Error()))
	}
}
