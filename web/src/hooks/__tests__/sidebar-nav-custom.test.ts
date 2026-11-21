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
import { describe, expect, it } from 'vitest'

import type { NavGroup } from '@/components/layout/types'

import {
  applySidebarNavCustom,
  parseSidebarNavCustom,
  serializeSidebarNavCustom,
} from '../use-sidebar-nav-custom'

function groups(): NavGroup[] {
  return [
    {
      id: 'general',
      title: 'General',
      items: [
        { id: 'general.overview', title: 'Overview', url: '/dashboard/overview' },
        { id: 'general.keys', title: 'API Keys', url: '/keys' },
      ],
    },
    {
      id: 'personal',
      title: 'Personal',
      items: [
        { id: 'personal.wallet', title: 'Wallet', url: '/wallet' },
      ],
    },
  ]
}

describe('parseSidebarNavCustom', () => {
  it('空值与非法 JSON 一律回退为空配置（导航栏保持默认）', () => {
    expect(parseSidebarNavCustom(undefined)).toEqual({ groups: [] })
    expect(parseSidebarNavCustom('')).toEqual({ groups: [] })
    expect(parseSidebarNavCustom('not-json')).toEqual({ groups: [] })
    expect(parseSidebarNavCustom('{"groups":"oops"}')).toEqual({ groups: [] })
  })

  it('解析隐藏、改名与顺序，并丢弃无 id 的脏数据', () => {
    const config = parseSidebarNavCustom(
      JSON.stringify({
        groups: [
          {
            id: 'personal',
            label: '我的',
            items: [{ id: 'personal.wallet', hidden: true }],
          },
          { id: 'general', items: [{ id: 'general.keys' }, { nope: 1 }] },
          { id: '' },
        ],
      })
    )
    expect(config.groups.map((g) => g.id)).toEqual(['personal', 'general'])
    expect(config.groups[0].label).toBe('我的')
    expect(config.groups[0].items).toEqual([
      { id: 'personal.wallet', hidden: true },
    ])
    expect(config.groups[1].items).toEqual([{ id: 'general.keys' }])
  })

  it('序列化后可以被重新解析（往返一致）', () => {
    const raw = JSON.stringify({
      groups: [{ id: 'general', items: [{ id: 'general.keys', hidden: true }] }],
    })
    const config = parseSidebarNavCustom(raw)
    expect(parseSidebarNavCustom(serializeSidebarNavCustom(config))).toEqual(
      config
    )
  })
})

describe('applySidebarNavCustom', () => {
  it('没有配置时原样返回', () => {
    const source = groups()
    expect(applySidebarNavCustom(source, { groups: [] })).toBe(source)
  })

  it('隐藏分组与菜单项、改分组名', () => {
    const result = applySidebarNavCustom(groups(), {
      groups: [
        { id: 'general', label: '工作台', items: [{ id: 'general.keys', hidden: true }] },
      ],
    })
    expect(result.map((g) => g.id)).toEqual(['general', 'personal'])
    expect(result[0].title).toBe('工作台')
    expect(result[0].items.map((i) => i.id)).toEqual(['general.overview'])

    const hidden = applySidebarNavCustom(groups(), {
      groups: [{ id: 'personal', hidden: true }],
    })
    expect(hidden.map((g) => g.id)).toEqual(['general'])
  })

  it('按配置顺序重排分组与菜单项，未配置的追加在后', () => {
    const result = applySidebarNavCustom(groups(), {
      groups: [
        { id: 'personal', items: [{ id: 'personal.wallet' }] },
        {
          id: 'general',
          items: [{ id: 'general.keys' }, { id: 'general.overview' }],
        },
      ],
    })
    expect(result.map((g) => g.id)).toEqual(['personal', 'general'])
    expect(result[1].items.map((i) => i.id)).toEqual([
      'general.keys',
      'general.overview',
    ])
  })

  it('配置里没提到的新菜单项仍然可见（升级不会被老配置吃掉）', () => {
    const source: NavGroup[] = [
      {
        id: 'general',
        title: 'General',
        items: [
          { id: 'general.overview', title: 'Overview', url: '/dashboard/overview' },
          { id: 'general.brandNew', title: 'New', url: '/new' },
        ],
      },
    ]
    // 老配置只认识 overview
    const result = applySidebarNavCustom(source, {
      groups: [{ id: 'general', items: [{ id: 'general.overview' }] }],
    })
    expect(result[0].items.map((i) => i.id)).toEqual([
      'general.overview',
      'general.brandNew',
    ])
  })

  it('支持菜单项改名', () => {
    const result = applySidebarNavCustom(groups(), {
      groups: [
        {
          id: 'general',
          items: [
            { id: 'general.overview', label: '总览' },
            { id: 'general.keys', label: '密钥' },
          ],
        },
      ],
    })
    expect(result[0].items.map((i) => i.title)).toEqual(['总览', '密钥'])
  })
})
