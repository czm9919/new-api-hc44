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

import { createServerError } from '@/lib/server-error-message'

import { describeSaveFailure } from '../save-error'

const FALLBACK = '扩展设置保存失败'

describe('describeSaveFailure', () => {
  it('业务失败（success:false）时透出服务端文案', () => {
    // 后端 ApiError 是 HTTP 200 + success:false，requireServerSuccess 会包成 Error
    const error = createServerError({
      success: false,
      message: '每日额度不能为负数',
    })
    expect(describeSaveFailure(error, FALLBACK)).toBe('每日额度不能为负数')
  })

  it('axios 错误时取 response.data.message', () => {
    const error = {
      isAxiosError: true,
      message: 'Request failed with status code 400',
      response: {
        status: 400,
        data: { success: false, message: 'Invalid parameters' },
      },
    }
    expect(describeSaveFailure(error, FALLBACK)).toBe('Invalid parameters')
  })

  it('普通 Error 时用 message', () => {
    expect(describeSaveFailure(new Error('网络异常'), FALLBACK)).toBe('网络异常')
  })

  it('拿不到任何文案时回落到调用方给的兜底文案', () => {
    expect(describeSaveFailure({}, FALLBACK)).toBe(FALLBACK)
    expect(describeSaveFailure(undefined, FALLBACK)).toBe(FALLBACK)
    expect(
      describeSaveFailure({ success: false, message: '' }, FALLBACK)
    ).toBe(FALLBACK)
  })
})
