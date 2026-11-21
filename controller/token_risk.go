package controller

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

// GetUserTokenRisk 用户侧密钥泄露风险自查（v1.1.0 M2 · B93）：
// 基于近 N 天日志的 token × IP 聚合，识别多 IP 使用与 AllowIps 冲突。
// 查询参数 days：回看天数，缺省 30，上限 90。
func GetUserTokenRisk(c *gin.Context) {
	userId := c.GetInt("id")
	days, _ := strconv.Atoi(c.Query("days"))
	report, err := service.BuildTokenRiskReport(userId, days)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    report,
	})
}

// GetTokenRiskOverview 管理端密钥泄露风险巡检：跨用户汇总高风险密钥。
// 查询参数：days（回看天数）、limit（最多返回条目数，默认 100）。
func GetTokenRiskOverview(c *gin.Context) {
	days, _ := strconv.Atoi(c.Query("days"))
	limit, _ := strconv.Atoi(c.Query("limit"))
	report, err := service.BuildTokenRiskOverview(days, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    report,
	})
}
