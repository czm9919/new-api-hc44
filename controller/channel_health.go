package controller

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// 渠道健康总览（hc35 · B13）。
//
// 数据源是智能路由的进程内健康记忆（成功/失败衰减计数、首字延迟 EWMA、
// 失败冷却），之前只喂给路由算法，运营台看不到。本接口把它只读暴露：
//   - 渠道表：持续失败的置顶（成功率升序），无样本渠道垫底；
//   - 模型表：auto 智能选型正在使用的 (group, model) 健康信号；
//   - settings：半衰期 / 冷却 / 置信阈值，方便理解样本口径；
//   - summary：总量、有样本、冷却中、异常（样本充足且成功率 < 50%）计数。
//
// 纯读、无副作用；进程重启后记忆冷启动，属预期行为而非故障。
func GetChannelHealthOverview(c *gin.Context) {
	now := time.Now()

	rows := model.CollectChannelHealthOverview(now)
	setting := operation_setting.GetLoadBalanceSetting()

	summary := gin.H{
		"total":        len(rows),
		"with_samples": 0,
		"cooling_down": 0,
		"degraded":     0,
	}
	minSamples := float64(setting.SmartMinSamples)
	for _, row := range rows {
		if row.HasSamples {
			summary["with_samples"] = summary["with_samples"].(int) + 1
		}
		if row.CoolingDown {
			summary["cooling_down"] = summary["cooling_down"].(int) + 1
		}
		if row.HasSamples && row.Samples >= minSamples && row.SuccessRate < 0.5 {
			summary["degraded"] = summary["degraded"].(int) + 1
		}
	}

	common.ApiSuccess(c, gin.H{
		"channels": rows,
		"models":   model.CollectModelHealthOverview(now),
		"summary":  summary,
		"settings": gin.H{
			"half_life_minutes":     setting.SmartLatencyHalfLifeMinutes,
			"fail_cooldown_seconds": setting.SmartFailCooldownSeconds,
			"min_samples":           setting.SmartMinSamples,
			"load_penalty":          setting.SmartLoadPenalty,
		},
		"generated_at": now.Unix(),
	})
}
