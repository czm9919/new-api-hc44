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
 * 密钥泄露风险自查（hc31 · B93）前端类型与接口。
 *
 * 后端把窗口内「token × IP」的消费日志聚合后套两条规则：
 *  - ip_outside_allowlist：设置了 AllowIps 白名单但出现了白名单外的 IP；
 *  - multi_ip：同一把密钥被 ≥3 个不同 IP 使用（≥5 判 high）。
 * 前端只负责把报告读出来按风险等级排序展示。
 */
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

/** 窗口内该密钥单个 IP 的使用情况。 */
export interface TokenRiskIP {
  ip: string
  request_count: number
  last_seen_at: number
  /** 是否在该密钥 AllowIps 白名单内（未设置白名单时恒为 true）。 */
  allowed: boolean
}

/** 单把密钥的风险明细。 */
export interface TokenRiskItem {
  token_id: number
  token_name: string
  user_id: number
  username: string
  status: number
  /** high / medium / low / '' */
  risk_level: string
  /** ip_outside_allowlist / multi_ip */
  reasons: string[]
  distinct_ips: number
  request_count: number
  allow_ips: string[]
  ips: TokenRiskIP[]
  last_used_at: number
  recommendation: string
}

/** 密钥泄露风险报告。 */
export interface TokenRiskReport {
  window_days: number
  generated_at: number
  /** 窗口内有使用记录的密钥数。 */
  total_tokens: number
  /** 命中风险规则的密钥数。 */
  risky_tokens: number
  items: TokenRiskItem[]
}

/** 回看窗口选项：与后端 TokenRiskMaxDays（90）保持一致。 */
export const TOKEN_RISK_DAY_OPTIONS = [
  { value: '7', labelKey: 'Last 7 days' },
  { value: '30', labelKey: 'Last 30 days' },
  { value: '90', labelKey: 'Last 90 days' },
] as const

/** 读取当前用户的密钥风险报告。 */
export async function getTokenRisk(days: number): Promise<TokenRiskReport> {
  const res = await api.get('/api/user/token-risk', { params: { days } })
  return requireServerSuccess(res.data)?.data
}
