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
/**
 * v1.1.0 M1 · B20 `/buy` 公开售卖页。后端 `GET /api/subscription/public/plans`
 * 返回的是精简后的套餐 DTO（不含内部字段），与 `features/subscriptions` 的
 * `PlanRecord` 结构不同，因此这里单独维护一份类型。
 */
export type PublicSubscriptionPlan = {
  id: number
  title: string
  subtitle: string
  price_amount: number
  currency: string
  duration_unit: string
  duration_value: number
  custom_seconds: number
  total_amount: number
  quota_reset_period: string
  allow_balance_pay: boolean
  sort_order: number
}

export type PublicSubscriptionPlansResponse = {
  success: boolean
  message: string
  data: PublicSubscriptionPlan[]
}
