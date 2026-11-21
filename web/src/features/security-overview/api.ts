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
/*
 * 安全巡检（hc36 · B93 管理端）前端类型与接口。
 *
 * 两个数据源都是「后端早就有、前端一直没消费」的运维接口：
 *  - GET /api/token/risk/overview：跨用户汇总窗口内的高风险密钥；
 *  - GET /api/user/2fa/stats：全站两步验证覆盖率。
 *
 * 风险报告结构与用户侧自查页（features/token-risk）完全一致，直接复用类型，
 * 避免两处口径漂移。
 */
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { TokenRiskReport } from '../token-risk/api'

export type {
  TokenRiskIP,
  TokenRiskItem,
  TokenRiskReport,
} from '../token-risk/api'

/** 全站两步验证覆盖率（/api/user/2fa/stats）。 */
export interface TwoFAStats {
  total_users: number
  enabled_users: number
  /** 后端已格式化的百分比字符串，例如 "42.5%"。 */
  enabled_rate: string
}

/** 管理端巡检一次最多取多少条风险密钥（与后端 TokenRiskOverviewLimit 一致）。 */
export const SECURITY_OVERVIEW_LIMIT = 100

/** 回看窗口：与后端 TokenRiskMaxDays（90）保持一致。 */
export const SECURITY_OVERVIEW_DAY_OPTIONS = [
  { value: '7', labelKey: 'Last 7 days' },
  { value: '30', labelKey: 'Last 30 days' },
  { value: '90', labelKey: 'Last 90 days' },
] as const

/** 跨用户密钥风险巡检（管理员）。 */
export async function getSecurityOverview(
  days: number,
  limit: number = SECURITY_OVERVIEW_LIMIT
): Promise<TokenRiskReport> {
  const res = await api.get('/api/token/risk/overview', {
    params: { days, limit },
  })
  return requireServerSuccess(res.data)?.data
}

/** 全站两步验证覆盖率（管理员）。 */
export async function getTwoFAStats(): Promise<TwoFAStats> {
  const res = await api.get('/api/user/2fa/stats')
  return requireServerSuccess(res.data)?.data
}
