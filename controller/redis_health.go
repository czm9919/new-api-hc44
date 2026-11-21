package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// GetRedisHealth 返回 Redis 健康状态快照（v1.1.0 M2 · B12，仅 root）。
//
// 半夜排障时先看 degraded 与 consecutive_failures：为 true 说明限流正在走
// 内存降级路径（缓存类调用点本来就回源数据库），last_error 给出最近一次
// 失败的操作与原因，degrade_count 统计本轮发布以来累计降级次数。
func GetRedisHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    common.RedisHealthSnapshot(),
	})
}
