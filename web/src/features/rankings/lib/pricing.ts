/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { PricingLeaderboardRow } from '../types'

/** 金额都很小，固定两位小数会把 $0.075 显示成 $0.08，因此按量级自适应小数位。 */
export function formatUnitPrice(value: number | undefined | null): string {
  if (value === undefined || value === null || !Number.isFinite(value)) {
    return '—'
  }
  if (value <= 0) {
    return '$0'
  }
  let digits = 2
  if (value < 0.01) {
    digits = 4
  } else if (value < 1) {
    digits = 3
  }
  return `$${value.toFixed(digits).replace(/\.?0+$/, '')}`
}

/**
 * 相对官方价的偏差：负数 = 比官方便宜。
 * 返回带符号的百分比文本，0 附近归一化成「0%」。
 */
export function formatDiscount(pct: number | undefined | null): string {
  if (pct === undefined || pct === null || !Number.isFinite(pct)) {
    return '—'
  }
  if (Math.abs(pct) < 0.05) {
    return '0%'
  }
  const sign = pct > 0 ? '+' : '−'
  const abs = Math.abs(pct)
  const digits = abs < 100 ? 1 : 0
  return `${sign}${abs.toFixed(digits).replace(/\.0$/, '')}%`
}

/** 折扣语义：便宜 / 贵 / 持平，用于决定配色。 */
export function discountTone(
  pct: number | undefined | null
): 'cheaper' | 'pricier' | 'flat' {
  if (pct === undefined || pct === null || !Number.isFinite(pct)) {
    return 'flat'
  }
  if (Math.abs(pct) < 0.05) {
    return 'flat'
  }
  return pct < 0 ? 'cheaper' : 'pricier'
}

/** 主价格列：按 token 计费看输入价，按次计费看每次价格。 */
export function primaryPrice(row: PricingLeaderboardRow): number {
  if (row.unit === 'request') {
    return row.site_request_price ?? 0
  }
  return row.site_input_price ?? 0
}
