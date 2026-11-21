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
import { expect, test } from 'vitest'

import { resolveIconAssetPath } from '../icon-asset'

test.each([
  ['SiAlipay', '/icons/si/SiAlipay.svg'],
  ['SiWechat', '/icons/si/SiWechat.svg'],
  ['LuCreditCard', '/icons/lu/LuCreditCard.svg'],
  ['  SiStripe  ', '/icons/si/SiStripe.svg'],
])('resolves supported icon names: %s', (name, expected) => {
  expect(resolveIconAssetPath(name)).toBe(expected)
})

test.each([
  // 未物化的图标包
  ['MdPayment'],
  ['FaCreditCard'],
  ['TbCreditCard'],
  // 非法名称
  ['siAlipay'],
  ['Si支付宝'],
  [''],
  ['   '],
  [null],
  [undefined],
])('returns null for unsupported names: %s', (name) => {
  expect(resolveIconAssetPath(name)).toBeNull()
})
