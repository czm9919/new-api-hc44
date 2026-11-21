package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

// =============================================================================
// hc31 · B211 站内通知中心（用户侧端点）
// =============================================================================

// GetNotifications 用户收件箱：分页列表 + 未读数。
// 查询参数：page（从 1 开始）、page_size（默认 20，上限 100）。
func GetNotifications(c *gin.Context) {
	userId := c.GetInt("id")
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	data, err := service.ListUserNotifications(userId, page, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
}

// GetNotificationUnreadCount 未读通知数（顶栏角标轮询）。
func GetNotificationUnreadCount(c *gin.Context) {
	userId := c.GetInt("id")
	count, err := service.GetUnreadNotificationCount(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"unread_count": count},
	})
}

// MarkNotificationRead 标记单条通知已读（只能操作自己的记录）。
func MarkNotificationRead(c *gin.Context) {
	userId := c.GetInt("id")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "invalid notification id",
		})
		return
	}
	updated, err := service.MarkUserNotificationRead(userId, id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !updated {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "notification not found or already read",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

// MarkAllNotificationsRead 全部标记已读。
func MarkAllNotificationsRead(c *gin.Context) {
	userId := c.GetInt("id")
	affected, err := service.MarkAllUserNotificationsRead(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"marked": affected},
	})
}
