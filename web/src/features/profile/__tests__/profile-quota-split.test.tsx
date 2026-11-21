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
import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { formatQuota } from '@/lib/format'

import { ProfileHeader } from '../components/profile-header'
import { splitProfileQuota } from '../lib'
import type { UserProfile } from '../types'

function makeProfile(overrides: Partial<UserProfile> = {}): UserProfile {
  return {
    id: 1,
    username: 'alice',
    display_name: 'Alice',
    role: 1,
    group: 'default',
    quota: 0,
    free_quota: 0,
    used_quota: 0,
    request_count: 0,
    status: 1,
    aff_count: 0,
    aff_quota: 0,
    aff_history_quota: 0,
    created_time: 0,
    ...overrides,
  }
}

describe('splitProfileQuota', () => {
  it('付费余额 = 总额 - 免费额度', () => {
    expect(splitProfileQuota(3000, 1000)).toEqual({
      total: 3000,
      free: 1000,
      paid: 2000,
    })
  })

  it('没有免费额度时付费余额等于总额', () => {
    expect(splitProfileQuota(2500, 0)).toEqual({
      total: 2500,
      free: 0,
      paid: 2500,
    })
    expect(splitProfileQuota(2500)).toEqual({
      total: 2500,
      free: 0,
      paid: 2500,
    })
  })

  it('免费额度超过总额（脏数据）时付费余额钳到 0', () => {
    expect(splitProfileQuota(100, 500).paid).toBe(0)
  })

  it('透支（总额为负）时保留负号', () => {
    expect(splitProfileQuota(-100, 0).paid).toBe(-100)
  })
})

describe('个人页余额卡：签到只让免费额度变，付费余额不动', () => {
  it('按付费余额 / 免费额度 / 总可用三个口径展示', () => {
    const { container } = render(
      <ProfileHeader
        profile={makeProfile({ quota: 3000, free_quota: 1000 })}
        loading={false}
      />
    )
    const text = container.textContent ?? ''
    expect(text).toContain('Paid balance')
    expect(text).toContain('Free quota')
    expect(text).toContain(formatQuota(2000)) // 付费余额
    expect(text).toContain(formatQuota(1000)) // 免费额度
    expect(text).toContain(formatQuota(3000)) // 总可用
    // 旧的「当前余额」口径会把含免费的总额当付费余额直出，必须消失
    expect(text).not.toContain('Current Balance')
  })

  it('签到 +4000 后付费余额卡数值保持不变', () => {
    const before = render(
      <ProfileHeader
        profile={makeProfile({ quota: 3000, free_quota: 1000 })}
        loading={false}
      />
    )
    const beforeText = before.container.textContent ?? ''
    before.unmount()

    const after = render(
      <ProfileHeader
        profile={makeProfile({ quota: 7000, free_quota: 5000 })}
        loading={false}
      />
    )
    const afterText = after.container.textContent ?? ''

    // 签到前 3000-1000=2000，签到 +4000 后 7000-5000=2000：付费余额不能涨
    expect(beforeText).toContain(formatQuota(2000))
    expect(afterText).toContain(formatQuota(2000))
    // 涨的是免费额度与总可用
    expect(afterText).toContain(formatQuota(5000))
    expect(afterText).toContain(formatQuota(7000))
  })
})
