package common

import "math"

// 额度与美元的换算（hc49）
//
// 为什么集中在这里：hc47/hc48 新增的付费功能（付费抽奖、加速套餐）在库里
// 仍然用 quota 计量（这样能和既有的原子扣减、退款、日志链路保持一致），
// 但对外一律用美元报价。换算口径只能有一处，否则前端、管理端、对账三边
// 迟早出现分位差异。
//
// QuotaPerUnit 是变量（默认 500000，1 美元 = 50 万额度），所以不能写成常量。

// QuotaToUSD 把内部额度换算成美元；QuotaPerUnit 非法时返回 0。
func QuotaToUSD(quota int64) float64 {
	return QuotaToUSDValue(float64(quota))
}

// QuotaToUSDValue 同 QuotaToUSD，但接受浮点额度（统计聚合结果用）。
func QuotaToUSDValue(quota float64) float64 {
	if QuotaPerUnit <= 0 {
		return 0
	}
	return quota / QuotaPerUnit
}

// USDToQuota 把美元价格换算成内部额度，四舍五入到整数额度。
// 负数与非数字一律返回 0：价格校验在调用方做，这里只负责换算。
func USDToQuota(usd float64) int64 {
	if usd <= 0 || QuotaPerUnit <= 0 || math.IsNaN(usd) || math.IsInf(usd, 0) {
		return 0
	}
	return int64(math.Round(usd * QuotaPerUnit))
}

// RoundUSD 把美元金额收敛到 4 位小数，避免 1/3 这类换算结果在 JSON 里
// 出现 3.3333333333333335 这种噪声。
func RoundUSD(usd float64) float64 {
	if math.IsNaN(usd) || math.IsInf(usd, 0) {
		return 0
	}
	return math.Round(usd*10000) / 10000
}
