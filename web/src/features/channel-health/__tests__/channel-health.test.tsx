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
 * 渠道健康页（hc35 · B13–B15）页面单测。
 *
 * 只验证运营真正会用的四件事：
 *  1. 渠道表按后端顺序渲染（坏渠道置顶），成功率 / 样本 / 延迟可见；
 *  2. 冷却中与自动禁用有明确标记，无样本渠道显示占位；
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

import { ChannelHealthPage } from '../index'

const overviewPayload = {
  channels: [
    {
      channel_id: 7,
      name: 'bad-channel',
      type: 1,
      status: 1,
      tag: '',
      group: 'default',
      priority: 0,
      weight: 0,
      has_samples: true,
      success_rate: 0.12,
      samples: 42.5,
      ttft_ms: 1300,
      latency_ms: 0,
      cooling_down: true,
      active_count: 2,
    },
    {
      channel_id: 3,
      name: 'healthy',
      type: 14,
      status: 1,
      tag: 'tier-a',
      group: 'vip',
      priority: 10,
      weight: 5,
      has_samples: true,
      success_rate: 0.99,
      samples: 120,
      ttft_ms: 300,
      latency_ms: 1500,
      cooling_down: false,
      active_count: 0,
    },
    {
      channel_id: 9,
      name: 'unused',
      type: 1,
      status: 3,
      tag: '',
      group: 'default',
      priority: 0,
      weight: 0,
      has_samples: false,
      success_rate: 0,
      samples: 0,
      ttft_ms: 0,
      latency_ms: 0,
      cooling_down: false,
      active_count: 0,
    },
  ],
  models: [
    {
      group: 'default',
      model: 'gpt-4o',
      success_rate: 0.95,
      samples: 300,
      ttft_ms: 450,
    },
  ],
  summary: {
    total: 3,
    with_samples: 2,
    cooling_down: 1,
    degraded: 1,
  },
  settings: {
    half_life_minutes: 10,
    fail_cooldown_seconds: 45,
    min_samples: 10,
    load_penalty: 1,
  },
  generated_at: 1_763_000_100,
}

function okOverview() {
  return { data: { success: true, message: '', data: overviewPayload } }
}

let queryClient: QueryClient

function renderPage() {
  return render(
    <QueryClientProvider client={queryClient}>
      <ChannelHealthPage />
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

describe('ChannelHealthPage', () => {
  it('renders channel rows in backend order with rate and latency', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue(okOverview())

    renderPage()

    // 后端已按成功率升序排好，bad-channel 应在 healthy 之前
    expect(await screen.findByText('bad-channel')).toBeInTheDocument()
    expect(screen.getByText('healthy')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/api/channel/health/overview')

    // 延迟回填与统计卡
    expect(screen.getByText('1300 ms')).toBeInTheDocument()
    expect(
      screen.getByText('Enough samples but success rate below 50%')
    ).toBeInTheDocument()
    // 列头按表作用域断言：模型级健康表也有同名 Success rate 列
    const channelTable = screen.getByRole('table', {
      name: 'Channel reliability',
    })
    expect(
      within(channelTable).getByRole('columnheader', { name: 'Success rate' })
    ).toBeInTheDocument()
    expect(
      within(channelTable).getByRole('columnheader', { name: 'TTFT (avg)' })
    ).toBeInTheDocument()
  })

  it('marks cooling-down, auto-disabled and no-sample channels', async () => {
    vi.spyOn(api, 'get').mockResolvedValue(okOverview())

    renderPage()

    const channelTable = await screen.findByRole('table', {
      name: 'Channel reliability',
    })
    expect(within(channelTable).getByText('Cooling down')).toBeInTheDocument()
    expect(screen.getByText('Auto disabled')).toBeInTheDocument()
    // 无样本渠道显示占位而非 0%
    expect(screen.getAllByText('No samples yet').length).toBeGreaterThan(0)
  })

  it('filters rows locally without refetching', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue(okOverview())
    const user = userEvent.setup()

    renderPage()

    await screen.findByText('bad-channel')
    expect(get).toHaveBeenCalledTimes(1)

    await user.type(screen.getByLabelText('Search channels'), 'healthy')

    expect(screen.getByText('healthy')).toBeInTheDocument()
    expect(screen.queryByText('bad-channel')).not.toBeInTheDocument()
    // 过滤只作用于本地结果集
    expect(get).toHaveBeenCalledTimes(1)

    await user.type(screen.getByLabelText('Search channels'), 'zzz')
    expect(
      screen.getByText('No channels match your search')
    ).toBeInTheDocument()
  })

  it('exports the filtered rows as CSV', async () => {
    vi.spyOn(api, 'get').mockResolvedValue(okOverview())
    const createObjectURL = vi.fn((_blob: Blob) => 'blob:health')
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

    await screen.findByText('bad-channel')
    await user.click(screen.getByRole('button', { name: /Export CSV/ }))

    expect(createObjectURL).toHaveBeenCalledTimes(1)
    const blob = createObjectURL.mock.calls[0]?.[0]
    expect(blob).toBeInstanceOf(Blob)
    const text = await (blob as Blob).text()
    expect(text).toContain(
      '"channel_id","name","type","status","group","tag","priority"'
    )
    expect(text).toContain('"7","bad-channel","1","1","default","","0"')
    expect(text).toContain('"3","healthy"')
    expect(text).toContain('"tier-a"')
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:health')
  })
})
