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
import assert from 'node:assert/strict'

import { describe, test } from 'vitest'

import {
  discountTone,
  formatDiscount,
  formatUnitPrice,
  primaryPrice,
} from '../lib/pricing'
import type { PricingLeaderboardRow } from '../types'

function row(overrides: Partial<PricingLeaderboardRow>): PricingLeaderboardRow {
  return {
    rank: 1,
    model_name: 'gpt-4o',
    unit: 'token',
    site_input_price: 2.5,
    site_output_price: 10,
    has_official: false,
    ...overrides,
  }
}

describe('formatUnitPrice', () => {
  test('keeps small prices readable instead of rounding them to zero', () => {
    assert.equal(formatUnitPrice(0.075), '$0.075')
    assert.equal(formatUnitPrice(0.0035), '$0.0035')
    assert.equal(formatUnitPrice(2.5), '$2.5')
    assert.equal(formatUnitPrice(30), '$30')
  })

  test('handles missing and zero values', () => {
    assert.equal(formatUnitPrice(undefined), '—')
    assert.equal(formatUnitPrice(null), '—')
    assert.equal(formatUnitPrice(Number.NaN), '—')
    assert.equal(formatUnitPrice(0), '$0')
  })
})

describe('formatDiscount', () => {
  test('marks cheaper than official with a minus sign', () => {
    assert.equal(formatDiscount(-50), '−50%')
    assert.equal(formatDiscount(-12.34), '−12.3%')
  })

  test('marks pricier than official with a plus sign', () => {
    assert.equal(formatDiscount(25), '+25%')
  })

  test('treats noise around zero as flat', () => {
    assert.equal(formatDiscount(0.01), '0%')
    assert.equal(formatDiscount(0), '0%')
    assert.equal(formatDiscount(undefined), '—')
  })
})

describe('discountTone', () => {
  test('maps the sign to a colour tone', () => {
    assert.equal(discountTone(-20), 'cheaper')
    assert.equal(discountTone(20), 'pricier')
    assert.equal(discountTone(0), 'flat')
    assert.equal(discountTone(undefined), 'flat')
  })
})

describe('primaryPrice', () => {
  test('uses the input price for token-billed models', () => {
    assert.equal(primaryPrice(row({ site_input_price: 2.5 })), 2.5)
  })

  test('uses the per-call price for request-billed models', () => {
    assert.equal(
      primaryPrice(
        row({ unit: 'request', site_input_price: 0, site_request_price: 0.5 })
      ),
      0.5
    )
  })
})
