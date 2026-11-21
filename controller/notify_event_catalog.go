package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/service"
)

// GetNotifyEventCatalog 管理端「通知事件 × 渠道 × 默认开关」覆盖表（hc31 · B213）。
func GetNotifyEventCatalog(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    service.BuildNotifyEventCatalog(),
	})
}
