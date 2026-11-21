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
import { api } from '@/lib/api'

import type {
  PricingLeaderboard,
  RankingPeriod,
  RankingsSnapshot,
  UserRankingSnapshot,
} from './types'

type RankingsResponse = {
  success: boolean
  message?: string
  data: RankingsSnapshot
}

export async function getRankings(
  period: RankingPeriod
): Promise<RankingsResponse> {
  const res = await api.get('/api/rankings', { params: { period } })
  return res.data
}

type PricingLeaderboardResponse = {
  success: boolean
  message?: string
  data: PricingLeaderboard
}

/** 价格排行榜（hc41）：公开只读，与 /api/pricing 共用同一个导航开关。 */
export async function getPricingLeaderboard(): Promise<PricingLeaderboardResponse> {
  const res = await api.get('/api/pricing/leaderboard')
  return res.data
}

type UserRankingResponse = {
  success: boolean
  message?: string
  data: UserRankingSnapshot
}

/**
 * 用户消耗榜（hc47）：匿名可看榜，登录后后端额外返回 `self`（我的名次）。
 * 与 /api/rankings 共用 rankings 导航开关，因此不会绕过模块禁用。
 */
export async function getUserRanking(
  period: RankingPeriod,
  limit = 20
): Promise<UserRankingResponse> {
  const res = await api.get('/api/rankings/users', {
    params: { period, limit },
  })
  return res.data
}
