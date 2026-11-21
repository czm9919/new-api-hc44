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
 * 安全巡检页（hc36）单测。
 *
 * 只验证管理员真正会用的四件事：
 *  1. 两个数据源都打到正确接口，跨用户风险行与 2FA 覆盖率都渲染出来；
 *  2. 无风险密钥被过滤掉，只展示命中规则的密钥；
 *  3. 关键字过滤只在本地生效（不额外请求后端）；
 *  4. CSV 导出带上占位表头与防公式注入的引号约定。
 *
 * 测试环境的 i18n 资源为空，断言直接用 key 字面量。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { SecurityOverviewPage } from '../index'

const riskPayload = {
  window_days: 30,
  generated_at: 1_763_000_100,
  total_tokens: 3,
  risky_tokens: 2,
  items: [
    {
      token_id: 11,
      token_name: 'ci-bot-key',
      user_id: 5,
      username: 'alice',
      status: 1,
      risk_level: 'high',
      reasons: ['multi_ip', 'ip_outside_allowlist'],
      distinct_ips: 6,
      request_count: 240,
      allow_ips: ['10.0.0.1'],
      ips: [
        {
          ip: '203.0.113.7',
          request_count: 120,
          last_seen_at: 1_763_000_000,
          allowed: false,
        },
      ],
      last_used_at: 1_763_000_000,
      recommendation: 'Rotate this key.',
    },
    {
      token_id: 12,
      token_name: 'mobile-app',
      user_id: 6,
      username: 'bob',
      status: 1,
      risk_level: 'medium',
      reasons: ['multi_ip'],
      distinct_ips: 3,
      request_count: 40,
      allow_ips: [],
      ips: [],
      last_used_at: 1_762_999_000,
      recommendation: 'Add an IP allowlist.',
    },
    {
      // 无风险：没有命中任何规则，不应该出现在巡检表里
      token_id: 13,
      token_name: 'quiet-key',
      user_id: 7,
      username: 'carol',
      status: 1,
      risk_level: '',
      reasons: [],
      distinct_ips: 1,
      request_count: 8,
      allow_ips: [],
      ips: [],
      last_used_at: 1_762_998_000,
      recommendation: '',
    },
  ],
}

const twofaPayload = {
  total_users: 40,
  enabled_users: 17,
  enabled_rate: '42.5%',
}

function okRisk() {
  return { data: { success: true, message: '', data: riskPayload } }
}

function okTwoFA() {
  return { data: { success: true, message: '', data: twofaPayload } }
}

let queryClient: QueryClient

function renderPage() {
  return render(
    <QueryClientProvider client={queryClient}>
      <SecurityOverviewPage />
    </QueryClientProvider>
  )
}

function mockApi() {
  return vi.spyOn(api, 'get').mockImplementation((url: string) => {
    if (url === '/api/user/2fa/stats') {
      return Promise.resolve(okTwoFA())
    }
    return Promise.resolve(okRisk())
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
  Reflect.deleteProperty(URL, 'createObjectURL')
  Reflect.deleteProperty(URL, 'revokeObjectURL')
})

describe('SecurityOverviewPage', () => {
  it('loads both admin sources and renders flagged keys only', async () => {
    const get = mockApi()

    renderPage()

    expect(await screen.findByText('ci-bot-key')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/api/token/risk/overview', {
      params: { days: 30, limit: 100 },
    })
    expect(get).toHaveBeenCalledWith('/api/user/2fa/stats')

    const table = screen.getByRole('table', { name: 'Flagged keys' })
    expect(within(table).getByText('ci-bot-key')).toBeInTheDocument()
    expect(within(table).getByText('mobile-app')).toBeInTheDocument()
    // 无风险密钥不进入巡检表
    expect(within(table).queryByText('quiet-key')).not.toBeInTheDocument()

    // 跨用户证据：用户名、命中原因、风险等级
    expect(within(table).getByText('alice')).toBeInTheDocument()
    expect(within(table).getByText('High risk')).toBeInTheDocument()
    // 两行都命中 multi_ip 规则，同一文案出现多次
    expect(within(table).getAllByText('Used from many IPs').length).toBe(2)
    expect(
      within(table).getByRole('columnheader', { name: 'Distinct IPs' })
    ).toBeInTheDocument()
  })

  it('shows two-factor coverage from the stats endpoint', async () => {
    mockApi()

    renderPage()

    expect(await screen.findByText('42.5%')).toBeInTheDocument()
    expect(screen.getByText('Two-factor coverage')).toBeInTheDocument()
  })

  it('filters rows locally without refetching', async () => {
    const get = mockApi()
    const user = userEvent.setup()

    renderPage()

    await screen.findByText('ci-bot-key')
    const callsAfterLoad = get.mock.calls.length

    await user.type(screen.getByLabelText('Search keys'), 'mobile')

    expect(screen.getByText('mobile-app')).toBeInTheDocument()
    expect(screen.queryByText('ci-bot-key')).not.toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(callsAfterLoad)

    await user.type(screen.getByLabelText('Search keys'), 'zzz')
    expect(screen.getByText('No keys match your search')).toBeInTheDocument()
  })

  it('exports the filtered rows as CSV', async () => {
    mockApi()
    const createObjectURL = vi.fn((_blob: Blob) => 'blob:security')
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

    await screen.findByText('ci-bot-key')
    await user.click(screen.getByRole('button', { name: /Export CSV/ }))

    expect(createObjectURL).toHaveBeenCalledTimes(1)
    const blob = createObjectURL.mock.calls[0]?.[0]
    expect(blob).toBeInstanceOf(Blob)
    const text = await (blob as Blob).text()
    expect(text).toContain(
      '"token_id","token_name","user_id","username","risk_level","reasons"'
    )
    expect(text).toContain(
      '"11","ci-bot-key","5","alice","high","multi_ip|ip_outside_allowlist"'
    )
    expect(text).not.toContain('quiet-key')
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:security')
  })
})
