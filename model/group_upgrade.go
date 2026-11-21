package model

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting"
)

// 用户分组自动升级（v1.1.0 M2 · B57）。
//
// 规则由运营在后台配置：累计充值（成功订单金额合计，单位与收款币种一致）达到
// 阈值后自动把用户放进对应分组。典型用法是「累计充值满 100 进 vip」。
//
// 设计取舍：
//   - 规则用 JSON 存在一个 option 里（AutoGroupUpgradeRules），不做独立表：
//     规则数量是个位数，改规则是低频管理操作。
//   - 只升不降：累计充值只增不减，用户不会因为规则调整被自动降级；运营想
//     手动降级走既有 ManageUser。
//   - 目标分组不校验是否存在于分组倍率里：分组是否可充值/可见由其他配置管，
//     这里只负责把用户挪过去；配错了能在审计日志里立刻看到。
type AutoGroupUpgradeRule struct {
	// Group 目标分组名（即 users.group 的值）。
	Group string `json:"group"`
	// Threshold 累计充值金额阈值（含）。取规则里满足条件的最大阈值。
	Threshold float64 `json:"threshold"`
}

var autoGroupUpgradeRulesMu sync.RWMutex
var autoGroupUpgradeRulesCache []AutoGroupUpgradeRule

// SetAutoGroupUpgradeRules 解析并缓存规则 JSON；option 热更新时调用。
// 非法 JSON 返回错误且不清空旧规则（配置面板保存失败时保持现状比清空安全）。
func SetAutoGroupUpgradeRules(raw string) error {
	var rules []AutoGroupUpgradeRule
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &rules); err != nil {
			return err
		}
	}
	autoGroupUpgradeRulesMu.Lock()
	autoGroupUpgradeRulesCache = rules
	autoGroupUpgradeRulesMu.Unlock()
	return nil
}

func getAutoGroupUpgradeRules() []AutoGroupUpgradeRule {
	autoGroupUpgradeRulesMu.RLock()
	defer autoGroupUpgradeRulesMu.RUnlock()
	return autoGroupUpgradeRulesCache
}

// targetGroupForTotal 返回累计充值应命中的最高分组；未命中返回 ""。
func targetGroupForTotal(total float64, rules []AutoGroupUpgradeRule) string {
	target := ""
	best := -1.0
	for _, rule := range rules {
		if rule.Group == "" || rule.Threshold < 0 {
			continue
		}
		if total >= rule.Threshold && rule.Threshold > best {
			target = rule.Group
			best = rule.Threshold
		}
	}
	return target
}

// afterTopUpQuotaCommitted 在一笔充值成功入账并提交事务之后触发（B57）。
// 故意放在事务外：规则评估涉及 SUM 聚合，放进支付回调的事务里会拉长持锁
// 时间；晚一拍的升级由下一次充值或本函数兜住，不会漏（累计值只增不减）。
func afterTopUpQuotaCommitted(userId int) {
	if !setting.AutoGroupUpgradeEnabled || userId <= 0 {
		return
	}
	rules := getAutoGroupUpgradeRules()
	if len(rules) == 0 {
		return
	}

	var total float64
	if err := DB.Model(&TopUp{}).
		Where("user_id = ? AND status = ?", userId, common.TopUpStatusSuccess).
		Select("COALESCE(SUM(money), 0)").
		Scan(&total).Error; err != nil {
		logger.LogError(context.Background(), common.LogText("auto group upgrade: failed to sum topups for user %d: %s", userId, err.Error()))
		return
	}

	target := targetGroupForTotal(total, rules)
	if target == "" {
		return
	}
	user, err := GetUserById(userId, false)
	if err != nil || user.Id == 0 {
		return
	}
	if user.Group == target {
		return
	}
	if err := DB.Model(&User{}).Where("id = ?", userId).Update("group", target).Error; err != nil {
		logger.LogError(context.Background(), common.LogText("auto group upgrade: failed to update user %d to group %s: %s", userId, target, err.Error()))
		return
	}
	// 用户缓存里存了 group，改库后要失效，否则下次鉴权还拿旧分组。
	if err := invalidateUserCache(userId); err != nil {
		logger.LogError(context.Background(), common.LogText("auto group upgrade: failed to invalidate cache for user %d: %s", userId, err.Error()))
	}
	logger.LogInfo(context.Background(), common.LogText("auto group upgrade: user %d total=%.2f upgraded to group %s", userId, total, target))
	RecordOperationAuditLog(
		userId, 0,
		common.LogText("auto group upgrade: user %d total=%.2f upgraded to group %s", userId, total, target),
		"", "user.auto_group_upgrade",
		map[string]any{"target_user_id": userId, "total_money": total, "group": target},
		nil, nil,
	)
}
