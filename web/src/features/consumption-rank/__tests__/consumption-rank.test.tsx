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
 * 用户消耗排行榜（hc34）页面单测。
 *
 * 只验证运营真正会用的四件事：
 *  1. 榜单按后端顺序渲染，名次 / 用户名 / 消耗 / 占比都能看到；
 *  2. 已删除用户与禁用用户有明确标记；
 *  3. 关键字过滤只在本地生效（不额外请求后端）；
 *  4. CSV 导出带上占位表头与防公式注入的引号约定。
 *
 * 测试环境的 i18n 资源为空，断言直接用 key 字面量。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ConsumptionRankPage } from '../index'

const rankPayload = {
  leaderboard: [
    {
      rank: 1,
      user_id: 2,
      username: 'bob',
      display_name: 'Bob Chen',
      used_quota: 3_000_000,
      quota_share: 0.6,
      request_count: 120,
      last_active: 1_763_000_000,
      remaining_quota: 500_000,
      lifetime_quota: 9_000_000,
      status: 1,
      deleted: false,
    },
    {
      rank: 2,
      user_id: 1,
      username: 'alice',
      display_name: '',
      used_quota: 1_500_000,
      quota_share: 0.3,
      request_count: 88,
      last_active: 1_763_000_000,
      remaining_quota: 0,
      lifetime_quota: 4_000_000,
      status: 2,
      deleted: false,
    },
    {
      rank: 3,
      user_id: 7,
      username: 'ghost',
      display_name: 'Ghost',
      used_quota: 500_000,
      quota_share: 0.1,
      request_count: 10,
      last_active: 1_763_000_000,
      remaining_quota: 0,
      lifetime_quota: 500_000,
      status: 0,
      deleted: true,
    },
  ],
  totals: {
    used_quota: 5_000_000,
    request_count: 218,
    user_count: 3,
  },
  window: { days: 30, start_ts: 1_700_000_000, end_ts: 0 },
  generated_at: 1_763_000_100,
}

function okRank() {
  return { data: { success: true, message: '', data: rankPayload } }
}

let queryClient: QueryClient

function renderPage() {
  return render(
    <QueryClientProvider client={queryClient}>
      <ConsumptionRankPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
})

afterEach(() => {
  queryClient.clear()
  vi.restoreAllMocks()
  Reflect.deleteProperty(URL, 'createObjectURL')
  Reflect.deleteProperty(URL, 'revokeObjectURL')
})

describe('ConsumptionRankPage', () => {
  it('renders the leaderboard rows with rank, user and spend', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue(okRank())

    renderPage()

    expect(await screen.findByText('Bob Chen')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/api/user/consumption_rank', {
      params: { days: 30, limit: 20 },
    })

    // 展示名缺失时回退到用户名
    expect(screen.getByText('alice')).toBeInTheDocument()
    // 统计卡：窗口内有消耗的用户数
    expect(
      screen.getByText('Users with consumption in this window')
    ).toBeInTheDocument()
    // 表格列头
    expect(
      screen.getByRole('columnheader', { name: 'Rank' })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('columnheader', { name: 'Lifetime spent' })
    ).toBeInTheDocument()
  })

  it('marks deleted and disabled users', async () => {
    vi.spyOn(api, 'get').mockResolvedValue(okRank())

    renderPage()

    expect(await screen.findByText('Deleted user')).toBeInTheDocument()
    expect(screen.getByText('disabled')).toBeInTheDocument()
  })

  it('filters rows locally without refetching', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue(okRank())
    const user = userEvent.setup()

    renderPage()

    await screen.findByText('Bob Chen')
    expect(get).toHaveBeenCalledTimes(1)

    await user.type(screen.getByLabelText('Search users'), 'alice')

    expect(screen.getByText('alice')).toBeInTheDocument()
    expect(screen.queryByText('Bob Chen')).not.toBeInTheDocument()
    // 过滤只作用于本地结果集
    expect(get).toHaveBeenCalledTimes(1)

    await user.type(screen.getByLabelText('Search users'), 'zzz')
    expect(screen.getByText('No users match your search')).toBeInTheDocument()
  })

  it('exports the filtered rows as CSV', async () => {
    vi.spyOn(api, 'get').mockResolvedValue(okRank())
    const createObjectURL = vi.fn((_blob: Blob) => 'blob:rank')
    const revokeObjectURL = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: createObjectURL,
    })
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: revokeObjectURL,
    })
    const user = userEvent.setup()

    renderPage()

    await screen.findByText('Bob Chen')
    await user.click(screen.getByRole('button', { name: /Export CSV/ }))

    expect(createObjectURL).toHaveBeenCalledTimes(1)
    const blob = createObjectURL.mock.calls[0]?.[0]
    expect(blob).toBeInstanceOf(Blob)
    const text = await (blob as Blob).text()
    expect(text).toContain(
      '"rank","user_id","username","display_name","used_quota"'
    )
    expect(text).toContain('"1","2","bob","Bob Chen"')
    expect(text).toContain('"ghost"')
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:rank')
  })
})
