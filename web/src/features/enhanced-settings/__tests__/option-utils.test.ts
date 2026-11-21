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

import { optionValueOf, toOptionMap } from '../option-utils'

describe('toOptionMap', () => {
  it('把 GET /api/option/ 的数组形态归一成 map', () => {
    // 这是后端 controller.GetOptions 的真实返回：data 是 [{key,value}]
    const payload = {
      success: true,
      data: [
        { key: 'CustomHeadHTML', value: '<meta name="a" content="1" />' },
        { key: 'BalanceDisplaySetting', value: '{"split_enabled":false}' },
      ],
    }
    expect(toOptionMap(payload)).toEqual({
      CustomHeadHTML: '<meta name="a" content="1" />',
      BalanceDisplaySetting: '{"split_enabled":false}',
    })
  })

  it('兼容历史 map 形态与 axios response 包装', () => {
    expect(toOptionMap({ data: { CustomHeadHTML: 'x' } })).toEqual({
      CustomHeadHTML: 'x',
    })
    expect(
      toOptionMap({ data: { success: true, data: [{ key: 'K', value: 'v' }] } })
    ).toEqual({ K: 'v' })
  })

  it('空值与异常结构返回空 map', () => {
    expect(toOptionMap(undefined)).toEqual({})
    expect(toOptionMap(null)).toEqual({})
    expect(toOptionMap('oops')).toEqual({})
    expect(toOptionMap({ success: true, data: null })).toEqual({})
  })

  it('数字/布尔值转成字符串，缺失的 value 当空串', () => {
    expect(
      toOptionMap({ success: true, data: [{ key: 'A', value: 3 }] })
    ).toEqual({ A: '3' })
    expect(toOptionMap({ success: true, data: [{ key: 'B' }] })).toEqual({
      B: '',
    })
  })
})

describe('optionValueOf', () => {
  it('命中时返回原始字符串', () => {
    const payload = { success: true, data: [{ key: 'X', value: 'abc' }] }
    expect(optionValueOf(payload, 'X')).toBe('abc')
  })

  it('未配置时返回空串，而不是 undefined', () => {
    // 旧实现用数组下标取 key，永远拿到 undefined；这里锁死行为
    const payload = { success: true, data: [{ key: 'X', value: 'abc' }] }
    expect(optionValueOf(payload, 'CustomHeadHTML')).toBe('')
  })
})
