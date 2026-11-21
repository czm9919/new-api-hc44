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
 * 用户消耗榜（hc47）单测。
 *
 * 只覆盖用户能看到的四种状态，不测样式：
 *  1. 登录且上榜：显示名次、我的消耗、距上一名；
 *  2. 登录但本期没消耗：提示「暂未上榜」，不显示假名次；
 *  3. 未登录：榜单照常可见，但显示登录引导而不是名次卡；
 *  4. 空榜 / 接口失败：给出可读文案。
 *
 * 测试环境 i18n 资源为空，t() 直接返回 key 字面量，断言用 key。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { UserLeaderboardSection } from '../components/user-leaderboard-section'
import type { UserRankingSnapshot } from '../types'

const leaderboard: UserRankingSnapshot['leaderboard'] = [
  {
    rank: 1,
    user_id: 7,
    username: 'bo****b',
    used_quota: 3_000_000,
    request_count: 120,
    quota_share: 0.75,
    is_self: false,
  },
  {
    rank: 2,
    user_id: 1,
    username: 'al***e',
    used_quota: 1_000_000,
    request_count: 40,
    quota_share: 0.25,
    is_self: false,
  },
]

function snapshot(
  overrides: Partial<UserRankingSnapshot> = {}
): UserRankingSnapshot {
  return {
    period: 'week',
    start_ts: 1_772_000_000,
    end_ts: 0,
    leaderboard,
    totals: { used_quota: 4_000_000, request_count: 160, user_count: 2 },
    generated_at: 1_772_100_000,
    ...overrides,
  }
}

const rankedSelf: UserRankingSnapshot['self'] = {
  ranked: true,
  rank: 2,
  used_quota: 1_000_000,
  request_count: 40,
  ahead_quota: 3_000_000,
  gap_to_ahead: 2_000_000,
  total_users: 2,
  percentile: 0,
}

const unrankedSelf: UserRankingSnapshot['self'] = {
  ranked: false,
  rank: 0,
  used_quota: 0,
  request_count: 0,
  ahead_quota: 0,
  gap_to_ahead: 0,
  total_users: 2,
  percentile: 0,
}

let queryClient: QueryClient

function renderSection() {
  return render(
    <QueryClientProvider client={queryClient}>
      <UserLeaderboardSection period='week' />
    </QueryClientProvider>
  )
}

function mockPayload(payload: UserRankingSnapshot) {
  return vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, message: '', data: payload },
  })
}

beforeEach(() => {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
})

afterEach(() => {
  queryClient.clear()
  vi.restoreAllMocks()
})

describe('UserLeaderboardSection', () => {
  it('shows the rank card and highlights my row when signed in', async () => {
    mockPayload(
      snapshot({
        self: rankedSelf,
        leaderboard: [leaderboard[0], { ...leaderboard[1], is_self: true }],
      })
    )
    renderSection()

    expect(await screen.findByText('My rank')).toBeInTheDocument()
    // `#2` 与「共 N 人」在同一个 <p> 里，按前缀匹配。
    expect(screen.getByText(/^#2/)).toBeInTheDocument()
    expect(screen.getByText('My spend')).toBeInTheDocument()
    expect(screen.getByText('Gap to the next rank')).toBeInTheDocument()
    // 只有自己那一行带「我」徽标。
    expect(screen.getAllByText('Me')).toHaveLength(1)
    // 榜单本身始终可见。
    expect(screen.getByText('bo****b')).toBeInTheDocument()
    expect(screen.getByText('al***e')).toBeInTheDocument()
    expect(screen.getByText('participants')).toBeInTheDocument()
  })

  it('tells a signed-in user with no spend to send a request instead of faking a rank', async () => {
    mockPayload(snapshot({ self: unrankedSelf }))
    renderSection()

    expect(await screen.findByText('Not on the board yet')).toBeInTheDocument()
    expect(
      screen.getByText(
        'No consumption in this period yet — send one request to get on the board.'
      )
    ).toBeInTheDocument()
    expect(screen.queryByText('My rank')).not.toBeInTheDocument()
  })

  it('invites anonymous visitors to sign in while still showing the board', async () => {
    mockPayload(snapshot())
    renderSection()

    expect(
      await screen.findByText('Sign in to see your rank')
    ).toBeInTheDocument()
    expect(screen.queryByText('My rank')).not.toBeInTheDocument()
    expect(screen.queryByText('Me')).not.toBeInTheDocument()
    expect(screen.getByText('bo****b')).toBeInTheDocument()
  })

  it('renders an empty state when nobody consumed in the period', async () => {
    mockPayload(
      snapshot({
        leaderboard: [],
        totals: { used_quota: 0, request_count: 0, user_count: 0 },
      })
    )
    renderSection()

    expect(
      await screen.findByText('No consumption in this period yet')
    ).toBeInTheDocument()
  })

  it('surfaces the server error message when the request fails', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('rankings unavailable'))
    renderSection()

    expect(await screen.findByText('rankings unavailable')).toBeInTheDocument()
  })
})
