package model

import "github.com/QuantumNous/new-api/common"

// 加速专线用量统计（hc49）
//
// 订阅卖的是通道，用户最想知道的其实是「我这条线到底用了多少」。
// 这里直接从消费日志按分组聚合，不另建用量表：
//   - logs 已有 (user_id, group, created_at) 维度的常用索引，
//     单个用户 + 单个分组 + 时间窗口的数据量很小；
//   - 另建用量表就要处理补扣、退款、日志清理后的对账，收益不抵成本。
//
// 口径与扣费完全一致：type = LogTypeConsume，quota 即本次消耗的内部额度。
type AccelUsage struct {
	// Requests 窗口内的请求条数。
	Requests int64 `json:"requests"`
	// Quota 窗口内消耗的内部额度。
	Quota int64 `json:"quota"`
	// QuotaUSD 同一金额的美元报价（对外的唯一口径）。
	QuotaUSD float64 `json:"quota_usd"`
	// LastUsedAt 窗口内最后一次消费时间，0 表示从未使用。
	LastUsedAt int64 `json:"last_used_at"`
}

// GetAccelUsage 统计某用户在指定分组、since（unix 秒）之后的消耗。
// LOG_DB 未初始化（如单元测试）时返回零值而不是报错，页面显示 0 即可。
func GetAccelUsage(userId int, group string, since int64) (AccelUsage, error) {
	var usage AccelUsage
	if LOG_DB == nil || userId <= 0 || group == "" {
		return usage, nil
	}
	groupCol := logGroupColumn()
	tx := LOG_DB.Table("logs").
		Select("COUNT(*) AS requests, COALESCE(SUM(quota), 0) AS quota, "+
			"COALESCE(MAX(created_at), 0) AS last_used_at").
		Where("type = ? AND user_id = ? AND "+groupCol+" = ?", LogTypeConsume, userId, group)
	if since > 0 {
		tx = tx.Where("created_at >= ?", since)
	}
	if err := tx.Scan(&usage).Error; err != nil {
		return AccelUsage{}, err
	}
	usage.QuotaUSD = common.RoundUSD(common.QuotaToUSD(usage.Quota))
	return usage, nil
}
