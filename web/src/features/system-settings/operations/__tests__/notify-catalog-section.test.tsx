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
 * 通知事件覆盖表（hc32 · B213 管理端 UI）的单测。
 *
 * 只验证三件事，避免把静态目录抄一遍：
 *  1. 矩阵按后端返回的 channels 顺序渲染列，事件行给出受众与默认送达状态；
 *  2. 站内信箱永远显示「恒开启」且标注不可配置；
 *  3. 加载失败给出重试入口，重试成功后恢复矩阵。
 *
 * 测试环境的 i18n 资源为空，因此断言直接使用 key 字面量。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { NotifyCatalogSection } from '../notify-catalog-section'

function channelCover(
  channel: string,
  defaultEnabled: boolean,
  configurable: boolean
) {
  return { channel, default_enabled: defaultEnabled, configurable }
}

const catalogPayload = {
  generated_at: 1_763_000_000,
  note: 'inbox-always-on',
  channels: ['inbox', 'email', 'webhook'],
  events_inbox_covered: 2,
  events_total: 2,
  events: [
    {
      event: 'quota_exceed',
      name: 'Quota low',
      notify_type: 'quota_exceed',
      audience: 'user',
      description: 'desc-quota',
      preference_key: 'quota_warning_enabled',
      preference_type: 'tri_state_bool',
      default_enabled: true,
      channels: [
        channelCover('inbox', true, false),
        channelCover('email', true, true),
        channelCover('webhook', false, true),
      ],
    },
    {
      event: 'channel_update',
      name: 'Channel changed',
      notify_type: 'channel_update',
      audience: 'admin',
      description: 'desc-channel',
      preference_key: 'channel_notify_enabled',
      preference_type: 'tri_state_bool',
      default_enabled: false,
      channels: [
        channelCover('inbox', true, false),
        channelCover('email', true, true),
        channelCover('webhook', false, true),
      ],
    },
  ],
}

function okCatalog() {
  return { data: { success: true, message: '', data: catalogPayload } }
}

let queryClient: QueryClient

function renderSection() {
  return render(
    <QueryClientProvider client={queryClient}>
      <NotifyCatalogSection />
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
})

describe('NotifyCatalogSection', () => {
  it('renders the event × channel matrix from the catalog endpoint', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue(okCatalog())

    renderSection()

    expect(await screen.findByText('Quota low')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/api/option/notify_event_catalog')

    // 统计卡
    expect(screen.getByText('Total events')).toBeInTheDocument()
    expect(screen.getAllByText('2').length).toBeGreaterThanOrEqual(2)

    // 列头按后端 channels 顺序渲染，站内信箱走本地化 key
    expect(
      screen.getByRole('columnheader', { name: 'Inbox' })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('columnheader', { name: 'webhook' })
    ).toBeInTheDocument()

    // 受众区分：用户事件 / 管理员事件
    expect(screen.getByText('User')).toBeInTheDocument()
    expect(screen.getByText('Admin')).toBeInTheDocument()

    // 站内信箱恒开启且不可配置；外部渠道默认状态可配置
    expect(screen.getAllByText('Always on')).toHaveLength(2)
    expect(screen.getAllByText('Not configurable')).toHaveLength(2)
    expect(screen.getAllByText('On by default').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Off by default').length).toBeGreaterThan(0)

    // 偏好开关列展示字段名与三态语义
    expect(screen.getByText('quota_warning_enabled')).toBeInTheDocument()
    // 两个事件的偏好都是三态布尔；单元格为「类型 · 默认态」拼接文本
    expect(screen.getAllByText(/Tri-state toggle/)).toHaveLength(2)
  })

  it('shows a retry affordance and recovers after the failure clears', async () => {
    const get = vi.spyOn(api, 'get').mockRejectedValueOnce(new Error('network'))

    renderSection()

    expect(await screen.findByText('Failed to load')).toBeInTheDocument()

    get.mockResolvedValue(okCatalog())
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))

    await waitFor(() => {
      expect(screen.getByText('Quota low')).toBeInTheDocument()
    })
    expect(screen.queryByText('Failed to load')).not.toBeInTheDocument()
  })

  it('warns about events missing the inbox and filters the matrix by keyword', async () => {
    // 第二个事件删掉 inbox 渠道，制造一个「站内信箱未覆盖」缺口
    const payload = {
      ...catalogPayload,
      events_inbox_covered: 1,
      events: [
        catalogPayload.events[0],
        {
          ...catalogPayload.events[1],
          channels: [
            channelCover('email', true, true),
            channelCover('webhook', false, true),
          ],
        },
      ],
    }
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, message: '', data: payload },
    })

    renderSection()

    expect(await screen.findByText('Quota low')).toBeInTheDocument()

    // 缺口提示列出未覆盖事件
    expect(
      screen.getByText(/Events not delivered to the inbox/)
    ).toBeInTheDocument()

    // 关键字过滤只保留匹配行
    await userEvent.type(
      screen.getByRole('textbox', { name: 'Search events' }),
      'quota'
    )
    await waitFor(() => {
      expect(screen.queryByText('Channel changed')).not.toBeInTheDocument()
    })
    expect(screen.getByText('Quota low')).toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('exports the visible matrix as CSV', async () => {
    const createObjectURL = vi.fn((_blob: Blob) => 'blob:catalog')
    const revokeObjectURL = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: createObjectURL,
    })
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: revokeObjectURL,
    })

    try {
      vi.spyOn(api, 'get').mockResolvedValue(okCatalog())

      renderSection()

      expect(await screen.findByText('Quota low')).toBeInTheDocument()
      await userEvent.click(screen.getByRole('button', { name: 'Export CSV' }))

      expect(createObjectURL).toHaveBeenCalledTimes(1)
      const blob = createObjectURL.mock.calls[0]?.[0]
      const text = await blob.text()

      // 表头：固定列 + 每个渠道两列（单元格统一带引号）
      expect(text).toContain(
        '"event","name","notify_type","audience","preference_key","preference_type","default_enabled"'
      )
      expect(text).toContain('inbox_default_enabled')
      expect(text).toContain('webhook_configurable')
      // 数据行：事件键、受众、渠道默认态
      expect(text).toContain('quota_exceed')
      expect(text).toContain('channel_update')
      expect(text).toContain('admin')
      expect(revokeObjectURL).toHaveBeenCalledWith('blob:catalog')
    } finally {
      Reflect.deleteProperty(URL, 'createObjectURL')
      Reflect.deleteProperty(URL, 'revokeObjectURL')
    }
  })
})
