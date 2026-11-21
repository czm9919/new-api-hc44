package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

// GetCostInsight 用户侧成本优化建议（v1.1.0 M2 · B90）：
// 基于近 N 天逐模型消耗 + 当前倍率，识别高占比模型与更便宜替代。
// 查询参数 days：回看天数，缺省 30，上限 90。
func GetCostInsight(c *gin.Context) {
	userId := c.GetInt("id")
	days, _ := strconv.Atoi(c.Query("days"))
	insight, err := service.BuildCostInsight(userId, days)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    insight,
	})
}
