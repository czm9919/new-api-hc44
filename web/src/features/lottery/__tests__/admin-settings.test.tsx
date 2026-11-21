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
 * 大转盘管理端设置（v1.1.0 M2）单测。
 *
 * 关注运营真正会遇到的四件事：
 *  1. 奖池草稿本地校验：权重/名称不合法时不发请求，直接在行上报错
 *  2. 加一行奖品 → 保存时 payload 带上新行
 *  3. 后端返回字段级 400 → 对应行显示后端文案（不是只弹一句「失败了」）
 *  4. 令牌轮换/撤销：轮换明文只展示一次，撤销走 DELETE
 *
 * 测试环境 i18n 资源为空，t() 返回 key + 插值结果，断言用 key 字面量。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

vi.mock('@/hooks/use-admin', () => ({ useIsAdmin: () => true }))

import { extractSettingsFieldErrors } from '../api'
import { LotteryPage } from '../index'

const lotterySettings = {
  enabled: true,
  daily_spins: 3,
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
}

const qqConfig = {
  agent_configured: true,
  agent_token_hint: '••••f0c1',
  require_for_free: true,
  bound_users: 7,
  active_bind_codes: 2,
}

const statusPayload = {
  settings: lotterySettings,
  spins_used: 0,
  spins_remaining: 3,
  qq_bound: true,
  qq: '123****89',
}

let queryClient: QueryClient

function ok(data: unknown) {
  return Promise.resolve({ data: { success: true, message: '', data } })
}

function mockGet(overrides?: {
  lotterySettings?: () => Promise<unknown>
  qqConfig?: () => Promise<unknown>
}) {
  return vi.spyOn(api, 'get').mockImplementation((url: string) => {
    if (url === '/api/user/lottery') return ok(statusPayload)
    if (url === '/api/user/lottery/records') return ok({ items: [] })
    if (url === '/api/user/qq/bind') {
      return ok({
        bound: true,
        qq: '123****89',
        bound_at: 1,
        agent_enabled: true,
      })
    }
    if (url === '/api/user/lottery/settings') {
      return overrides?.lotterySettings
        ? overrides.lotterySettings()
        : ok(lotterySettings)
    }
    if (url === '/api/user/lottery/qq/settings') {
      return overrides?.qqConfig ? overrides.qqConfig() : ok(qqConfig)
    }
    return ok({})
  })
}

function renderPage() {
  return render(
    <QueryClientProvider client={queryClient}>
      <LotteryPage />
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

describe('lottery admin settings', () => {
  it('blocks saving and flags the row when a weight is invalid', async () => {
    mockGet()
    const put = vi
      .spyOn(api, 'put')
      .mockImplementation(() => ok(lotterySettings))
    renderPage()

    const weightInput = await screen.findByLabelText('Weight 1')
    fireEvent.change(weightInput, { target: { value: '0' } })
    fireEvent.click(
      screen.getByRole('button', { name: 'Save lottery settings' })
    )

    expect(
      await screen.findByText('Weight must be between 1 and 1000')
    ).toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })

  it('appends a new prize row and sends it on save', async () => {
    mockGet()
    const put = vi.spyOn(api, 'put').mockImplementation(() =>
      ok({
        ...lotterySettings,
        prizes: [
          ...lotterySettings.prizes,
          {
            id: 'prize-new',
            name: '新奖品',
            type: 'quota',
            quota: 800,
            weight: 5,
          },
        ],
      })
    )
    renderPage()

    await screen.findByLabelText('Prize name 1')
    fireEvent.click(screen.getByRole('button', { name: 'Add prize' }))

    const newName = await screen.findByLabelText('Prize name 3')
    fireEvent.change(newName, { target: { value: '新奖品' } })
    fireEvent.click(
      screen.getByRole('button', { name: 'Save lottery settings' })
    )

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    const payload = put.mock.calls[0][1] as {
      prizes: Array<{ name: string; weight: number; quota: number }>
    }
    expect(payload.prizes).toHaveLength(3)
    expect(payload.prizes[2]).toMatchObject({
      name: '新奖品',
      type: 'quota',
      weight: 1,
    })
  })

  it('renders backend field errors next to the offending row', async () => {
    mockGet()
    vi.spyOn(api, 'put').mockImplementation(() =>
      Promise.reject({
        response: {
          data: {
            success: false,
            code: 'LOTTERY_SETTINGS_INVALID',
            message: 'lottery settings are invalid',
            fields: [{ field: 'prizes[1].quota', message: '额度太低' }],
          },
        },
      })
    )
    renderPage()

    await screen.findByLabelText('Quota 2')
    fireEvent.click(
      screen.getByRole('button', { name: 'Save lottery settings' })
    )

    expect(await screen.findByText('额度太低')).toBeInTheDocument()
  })

  it('rotates the agent token and shows the plaintext exactly once', async () => {
    mockGet()
    const post = vi.spyOn(api, 'post').mockImplementation((url: string) => {
      if (url === '/api/user/lottery/qq/settings/rotate') {
        return ok({
          agent_token: 'deadbeefdeadbeefdeadbeefdeadbeef',
          config: { ...qqConfig, agent_token_hint: '••••beef' },
        })
      }
      return ok({})
    })
    renderPage()

    const rotate = await screen.findByRole('button', {
      name: 'Generate random token',
    })
    fireEvent.click(rotate)

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/user/lottery/qq/settings/rotate')
    )
    expect(
      await screen.findByText('deadbeefdeadbeefdeadbeefdeadbeef')
    ).toBeInTheDocument()
    expect(screen.getByText('New token (shown only once)')).toBeInTheDocument()
  })

  it('revokes the agent token through an explicit confirm step', async () => {
    mockGet()
    const del = vi
      .spyOn(api, 'delete')
      .mockImplementation(() => ok({ ...qqConfig, agent_configured: false }))
    renderPage()

    const revoke = await screen.findByRole('button', { name: 'Revoke token' })
    // 机器人配置还没查回来之前按钮是禁用的，等它可用再点
    await waitFor(() => expect(revoke).not.toBeDisabled())
    fireEvent.click(revoke)
    // 第一次点击只是展开确认，不该发请求
    expect(del).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Confirm revoke' }))
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith('/api/user/lottery/qq/settings')
    )
  })

  it('restores defaults through a dedicated endpoint', async () => {
    mockGet()
    const post = vi
      .spyOn(api, 'post')
      .mockImplementation(() => ok(lotterySettings))
    renderPage()

    fireEvent.click(
      await screen.findByRole('button', { name: 'Restore defaults' })
    )
    expect(post).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Confirm restore' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/user/lottery/settings/reset')
    )
  })
})

describe('extractSettingsFieldErrors', () => {
  it('reads fields from an axios-shaped error', () => {
    expect(
      extractSettingsFieldErrors({
        response: { data: { fields: [{ field: 'a', message: 'b' }] } },
      })
    ).toEqual([{ field: 'a', message: 'b' }])
  })

  it('returns an empty list for unrelated errors', () => {
    expect(extractSettingsFieldErrors(new Error('boom'))).toEqual([])
    expect(
      extractSettingsFieldErrors({ response: { data: { fields: 'nope' } } })
    ).toEqual([])
  })
})
