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
 * 大转盘抽奖（v1.1.0 M2）页面单测。
 *
 * 只验证用户真正会踩的四条路径：
 *  1. 未绑定 QQ：抽奖按钮禁用 + 明确提示「先绑定」
 *  2. 已绑定：点抽奖会请求后端，转盘停稳后展示中奖横幅
 *  3. 未绑定时生成一次性绑定码，把码展示给用户
 *  4. 抽奖记录按后端顺序渲染
 *
 * 测试环境 i18n 资源为空，t() 直接返回 key 字面量，断言用 key。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { LotteryPage } from '../index'

const statusPayload = {
  settings: {
    enabled: true,
    daily_spins: 1,
    require_qq: true,
    prizes: [
      { id: 'thanks', name: '谢谢参与', type: 'none', quota: 0, weight: 40 },
      {
        id: 'quota-500',
        name: '500 额度',
        type: 'quota',
        quota: 500,
        weight: 60,
      },
    ],
  },
  spins_used: 0,
  spins_remaining: 1,
  qq_bound: false,
  qq: '',
}

const recordsPayload = {
  items: [
    {
      id: 2,
      user_id: 10,
      day_key: '2026-11-18',
      spin_index: 1,
      prize_id: 'quota-500',
      prize_name: '500 额度',
      prize_type: 'quota',
      quota_awarded: 500,
      created_at: 1_763_000_000,
    },
  ],
}

const bindStatusUnbound = {
  bound: false,
  qq: '',
  bound_at: 0,
  agent_enabled: true,
}

const spinResult = {
  prize: {
    id: 'quota-500',
    name: '500 额度',
    type: 'quota',
    quota: 500,
    weight: 60,
  },
  prize_index: 1,
  quota_awarded: 500,
  spins_used: 1,
  spins_remaining: 0,
  spin: {
    id: 3,
    user_id: 10,
    day_key: '2026-11-18',
    spin_index: 0,
    prize_id: 'quota-500',
    prize_name: '500 额度',
    prize_type: 'quota',
    quota_awarded: 500,
    created_at: 1_763_000_100,
  },
}

let queryClient: QueryClient

function renderPage() {
  return render(
    <QueryClientProvider client={queryClient}>
      <LotteryPage />
    </QueryClientProvider>
  )
}

/** 按 URL 分流 mock：三个 GET + 一个 POST。 */
function mockApi(options?: {
  qqBound?: boolean
  spin?: () => Promise<unknown>
  bindCode?: () => Promise<unknown>
}) {
  const qqBound = options?.qqBound ?? false
  return vi.spyOn(api, 'get').mockImplementation((url: string) => {
    if (url === '/api/user/lottery') {
      return Promise.resolve({
        data: {
          success: true,
          message: '',
          data: {
            ...statusPayload,
            qq_bound: qqBound,
            qq: qqBound ? '123****89' : '',
          },
        },
      })
    }
    if (url === '/api/user/lottery/records') {
      return Promise.resolve({
        data: { success: true, message: '', data: recordsPayload },
      })
    }
    if (url === '/api/user/qq/bind') {
      return Promise.resolve({
        data: {
          success: true,
          message: '',
          data: qqBound
            ? {
                bound: true,
                qq: '123****89',
                bound_at: 1_763_000_000,
                agent_enabled: true,
              }
            : bindStatusUnbound,
        },
      })
    }
    return Promise.resolve({ data: { success: true, message: '', data: {} } })
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

describe('LotteryPage', () => {
  it('disables the spin button until a QQ account is bound', async () => {
    mockApi({ qqBound: false })
    renderPage()

    expect(
      await screen.findByText('Bind a QQ account first to spin.')
    ).toBeInTheDocument()
    const spinButton = screen.getByRole('button', { name: /Spin/ })
    expect(spinButton).toBeDisabled()
  })

  it('spins the wheel and shows the prize banner', async () => {
    mockApi({
      qqBound: true,
      spin: () =>
        Promise.resolve({
          data: { success: true, message: '', data: spinResult },
        }),
    })
    const post = vi
      .spyOn(api, 'post')
      .mockImplementation((url: string) =>
        url === '/api/user/lottery/spin'
          ? Promise.resolve({
              data: { success: true, message: '', data: spinResult },
            })
          : Promise.resolve({ data: { success: true, message: '', data: {} } })
      )

    renderPage()
    const spinButton = await screen.findByRole('button', { name: /Spin/ })
    await waitFor(() => expect(spinButton).not.toBeDisabled())

    fireEvent.click(spinButton)

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/user/lottery/spin')
    )
    // 转盘动画 3.2s 后才展示结果横幅
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 3400))
    })
    // 测试环境 resources 为空，t('You won {{prize}}') 会带着插值原样返回，
    // 所以用正则匹配前缀，避免依赖具体翻译文案。
    expect(screen.getByText(/You won/)).toBeInTheDocument()
    expect(screen.getAllByText(/\+500/).length).toBeGreaterThan(0)
  })

  it('issues a one-time bind code when the user is not bound', async () => {
    mockApi({ qqBound: false })
    const post = vi.spyOn(api, 'post').mockImplementation((url: string) => {
      if (url === '/api/user/qq/bind/code') {
        return Promise.resolve({
          data: {
            success: true,
            message: '',
            data: {
              code: 'ABCD2345',
              expires_at: 1_763_000_600,
              ttl_seconds: 600,
            },
          },
        })
      }
      return Promise.resolve({ data: { success: true, message: '', data: {} } })
    })

    const user = userEvent.setup()
    renderPage()

    const button = await screen.findByRole('button', {
      name: /Generate bind code/,
    })
    await user.click(button)

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/user/qq/bind/code')
    )
    expect(await screen.findByText('ABCD2345')).toBeInTheDocument()
  })

  it('renders the recent spin records', async () => {
    mockApi({ qqBound: true })
    renderPage()

    expect(await screen.findByText('Recent spins')).toBeInTheDocument()
    // 记录行里的额度增量（奖品名在转盘/奖池/记录三处出现，用增量文案断言）
    expect(screen.getAllByText(/\+500/).length).toBeGreaterThan(0)
  })
})
