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
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'

import { PAYMENT_ICON_COLORS, PAYMENT_TYPES } from '../../constants'
import { getPaymentIcon } from '../ui'

test.each([
  [PAYMENT_TYPES.ALIPAY, 'Alipay'],
  [PAYMENT_TYPES.WECHAT, 'WeChat'],
  [PAYMENT_TYPES.STRIPE, 'Stripe'],
])(
  'the built-in %s icon preserves the requested size and payment color',
  (type, label) => {
    render(getPaymentIcon(type, 'h-6 w-6'))
    const icon = screen.getByRole('img', { name: label })
    expect(icon).toHaveClass('h-6', 'w-6')
    expect(icon).toHaveStyle({ color: PAYMENT_ICON_COLORS[type] })
  }
)

test('an admin-configured Simple Icon resolves through the static asset mask', async () => {
  render(
    getPaymentIcon(
      PAYMENT_TYPES.ALIPAY,
      'h-4 w-4',
      'SiAlipay',
      'Configured Alipay'
    )
  )
  const title = await screen.findByTitle('Configured Alipay')
  // Icons are materialized to /icons/<pack>/<Name>.svg at build time and
  // painted through a CSS mask, so the request is one small file instead of a
  // multi-megabyte icon pack chunk.
  expect(title).toHaveClass('h-4', 'w-4')
  expect(title).toHaveStyle({ maskImage: 'url("/icons/si/SiAlipay.svg")' })
})
