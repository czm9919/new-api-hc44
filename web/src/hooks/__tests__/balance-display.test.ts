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

import {
  DEFAULT_BALANCE_DISPLAY,
  parseBalanceDisplay,
  serializeBalanceDisplay,
} from '../use-balance-display'

describe('parseBalanceDisplay', () => {
  it('空值与非法 JSON 一律回退默认配置（拆分开启、显示在用户列表）', () => {
    expect(parseBalanceDisplay(undefined)).toEqual(DEFAULT_BALANCE_DISPLAY)
    expect(parseBalanceDisplay('')).toEqual(DEFAULT_BALANCE_DISPLAY)
    expect(parseBalanceDisplay('{not json')).toEqual(DEFAULT_BALANCE_DISPLAY)
    expect(parseBalanceDisplay('[]')).toEqual(DEFAULT_BALANCE_DISPLAY)
    expect(parseBalanceDisplay(42)).toEqual(DEFAULT_BALANCE_DISPLAY)
  })

  it('缺字段时逐项回落到默认值，不会被配置缺失拖崩', () => {
    expect(parseBalanceDisplay('{"hide_free":true}')).toEqual({
      ...DEFAULT_BALANCE_DISPLAY,
      hideFree: true,
    })
  })

  it('接受字符串形式的布尔值（老配置/手写配置更宽容）', () => {
    const parsed = parseBalanceDisplay(
      '{"split_enabled":"false","hide_free":"true","show_in_user_list":"false"}'
    )
    expect(parsed.splitEnabled).toBe(false)
    expect(parsed.hideFree).toBe(true)
    expect(parsed.showInUserList).toBe(false)
  })

  it('自定义显示名去空白，空串表示沿用默认文案', () => {
    const parsed = parseBalanceDisplay(
      '{"paid_label":"  充值余额 ","free_label":"   "}'
    )
    expect(parsed.paidLabel).toBe('充值余额')
    expect(parsed.freeLabel).toBe('')
  })

  it('已解析好的对象也能直接吃下（后端字段直传场景）', () => {
    expect(
      parseBalanceDisplay({ split_enabled: false, paid_label: '余额' })
    ).toEqual({
      ...DEFAULT_BALANCE_DISPLAY,
      splitEnabled: false,
      paidLabel: '余额',
    })
  })
})

describe('serializeBalanceDisplay', () => {
  it('落库用 snake_case，且不写入多余空白', () => {
    const json = serializeBalanceDisplay({
      splitEnabled: false,
      paidLabel: '  充值余额  ',
      freeLabel: '',
      hideFree: true,
      showInUserList: false,
    })
    expect(JSON.parse(json)).toEqual({
      split_enabled: false,
      paid_label: '充值余额',
      free_label: '',
      hide_free: true,
      show_in_user_list: false,
    })
  })

  it('序列化后再解析得到同一份配置', () => {
    const config = {
      splitEnabled: true,
      paidLabel: '充值余额',
      freeLabel: '赠送额度',
      hideFree: true,
      showInUserList: false,
    }
    expect(parseBalanceDisplay(serializeBalanceDisplay(config))).toEqual(config)
  })
})
