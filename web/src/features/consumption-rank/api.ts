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
 * 用户消耗排行榜（hc34）前端类型与接口。
 *
 * 管理员专属：后端按窗口内消费日志（type=consume）按 user_id 聚合，
 * 额度倒序取前 N 名，并回填 users 表的当前用户名 / 余额 / 累计消耗。
 * quota_share 是该用户占全站窗口消耗的比例（0~1）。
 */
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

/** 榜单里的一条用户消耗记录。 */
export interface ConsumptionRankEntry {
  rank: number
  user_id: number
  /** 日志快照用户名，存在 users 表记录时会被覆盖为当前用户名。 */
  username: string
  display_name: string
  used_quota: number
  /** 占全站窗口消耗的比例（0~1）。 */
  quota_share: number
  request_count: number
  /** 窗口内最后一次消费的时间戳（秒）。 */
  last_active: number
  /** 当前余额（快照）。 */
  remaining_quota: number
  /** users.used_quota 累计消耗。 */
  lifetime_quota: number
  status: number
  /** 用户已从 users 表删除（只剩历史日志）。 */
  deleted: boolean
}

/** 全站窗口汇总。 */
export interface ConsumptionRankTotals {
  used_quota: number
  request_count: number
  user_count: number
}

/** 排行榜响应。 */
export interface ConsumptionRank {
  leaderboard: ConsumptionRankEntry[]
  totals: ConsumptionRankTotals
  window: { days: number; start_ts: number; end_ts: number }
  generated_at: number
}

/** 回看窗口选项：days=0 表示全部时段（与后端 ConsumptionRankMaxDays 对齐）。 */
export const CONSUMPTION_RANK_WINDOW_OPTIONS = [
  { value: '7', labelKey: 'Last 7 days' },
  { value: '30', labelKey: 'Last 30 days' },
  { value: '90', labelKey: 'Last 90 days' },
  { value: '0', labelKey: 'All time' },
] as const

/** 榜单条数选项（后端上限 100）。 */
export const CONSUMPTION_RANK_LIMIT_OPTIONS = ['10', '20', '50', '100'] as const

/** 读取用户消耗排行榜。 */
export async function getConsumptionRank(
  days: number,
  limit: number
): Promise<ConsumptionRank> {
  const res = await api.get('/api/user/consumption_rank', {
    params: { days, limit },
  })
  return requireServerSuccess(res.data)?.data
}
