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
import { useQuery } from '@tanstack/react-query'

import { requireServerSuccess } from '@/lib/server-error-message'

import { getUserRanking } from '../api'
import type { RankingPeriod } from '../types'

/**
 * 用户消耗榜（hc47）。
 *
 * 后端只缓存 60 秒，前端 staleTime 与它对齐即可：切周期时会立刻命中新 key，
 * 同一个周期来回切换不会额外打请求。
 */
export function useUserRanking(period: RankingPeriod, limit = 20) {
  return useQuery({
    queryKey: ['rankings', 'users', period, limit],
    queryFn: async () => requireServerSuccess(await getUserRanking(period, limit)),
    staleTime: 60 * 1000,
  })
}
