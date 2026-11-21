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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { getCheckinStatus, getUserProfile, performCheckin } from '../api'
import { CheckinCalendarCard } from '../components/checkin-calendar-card'
import { useProfile } from '../hooks/use-profile'

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    getCheckinStatus: vi.fn(),
    performCheckin: vi.fn(),
    getUserProfile: vi.fn(),
  }
})

afterEach(() => vi.clearAllMocks())

const checkinStatus = {
  success: true,
  data: {
    enabled: true,
    stats: {
      checked_in_today: false,
      total_checkins: 3,
      total_quota: 3000,
      checkin_count: 1,
      records: [],
    },
  },
}

function renderCard(onCheckedIn: () => void) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <CheckinCalendarCard
        checkinEnabled
        turnstileEnabled={false}
        turnstileSiteKey=''
        onCheckedIn={onCheckedIn}
      />
    </QueryClientProvider>
  )
}

describe('签到后的余额刷新', () => {
  it('签到成功后触发外层刷新用户资料（否则页面余额停在旧值）', async () => {
    vi.mocked(getCheckinStatus).mockResolvedValue(checkinStatus as never)
    vi.mocked(performCheckin).mockResolvedValue({
      success: true,
      data: { quota_awarded: 1000 },
    } as never)

    const onCheckedIn = vi.fn()
    renderCard(onCheckedIn)

    const button = await screen.findByRole('button', { name: /Check in now/i })
    fireEvent.click(button)

    await waitFor(() => expect(performCheckin).toHaveBeenCalled())
    await waitFor(() => expect(onCheckedIn).toHaveBeenCalledTimes(1))
  })

  it('签到失败时不触发刷新', async () => {
    vi.mocked(getCheckinStatus).mockResolvedValue(checkinStatus as never)
    vi.mocked(performCheckin).mockResolvedValue({
      success: false,
      message: 'Already checked in today',
    } as never)

    const onCheckedIn = vi.fn()
    renderCard(onCheckedIn)

    const button = await screen.findByRole('button', { name: /Check in now/i })
    fireEvent.click(button)

    await waitFor(() => expect(performCheckin).toHaveBeenCalled())
    expect(onCheckedIn).not.toHaveBeenCalled()
  })

  it('拉取资料后把最新余额同步到全局登录态', async () => {
    useAuthStore.setState((state) => ({
      auth: {
        ...state.auth,
        user: { id: 1, username: 'alice', role: 1, quota: 1000 },
      },
    }))
    vi.mocked(getUserProfile).mockResolvedValue({
      success: true,
      data: { id: 1, username: 'alice', role: 1, quota: 3000, free_quota: 1000 },
    } as never)

    const { result } = renderHook(() => useProfile())
    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(result.current.profile?.quota).toBe(3000)
    expect(useAuthStore.getState().auth.user?.quota).toBe(3000)
  })
})
