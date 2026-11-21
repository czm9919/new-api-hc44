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

import { getPricingLeaderboard } from '../api'

/** 页面停留时的自动轮询间隔：官方价是小时级的，5 分钟已经足够「看起来实时」。 */
const PRICING_LEADERBOARD_REFETCH_MS = 5 * 60 * 1000

/**
 * 价格榜数据。
 *
 * hc42 起后端会自动同步官方价，所以这里不再用 5 分钟的 staleTime 把旧数据
 * 锁住：切回标签页就重取，停留期间按 5 分钟轮询。后端在快照过期时还会顺手
 * 异步刷新一次，因此用户看到的永远是最近一次能拿到的官方价。
 */
export function usePricingLeaderboard() {
  return useQuery({
    queryKey: ['pricing-leaderboard'],
    queryFn: async () => requireServerSuccess(await getPricingLeaderboard()),
    staleTime: 30 * 1000,
    refetchInterval: PRICING_LEADERBOARD_REFETCH_MS,
    refetchOnWindowFocus: true,
  })
}
