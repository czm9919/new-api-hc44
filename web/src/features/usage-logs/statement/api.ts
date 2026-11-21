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
import { api } from '@/lib/api'

/**
 * v1.1.0 M1 · B26 对账月结单（`GET /api/log/statement`，管理员接口）。
 * 字段与 controller.MonthlyStatement 的 JSON tag 一一对应。
 */
export type StatementDay = {
  date: string
  recharge_quota: number
  consume_quota: number
  refund_quota: number
}

export type MonthlyStatementData = {
  month: string
  recharge_quota: number
  consume_quota: number
  refund_quota: number
  recharge_orders: number
  recharge_money: number
  recharge_amount: number
  pending_orders: number
  pending_money: number
  pending_recharge_quota: number
  days: StatementDay[]
}

export type MonthlyStatementResponse = {
  success: boolean
  message: string
  data: MonthlyStatementData
}

export async function getMonthlyStatement(
  month: string
): Promise<MonthlyStatementData> {
  const res = await api.get<MonthlyStatementResponse>('/api/log/statement', {
    params: { month },
  })
  if (!res.data.success || !res.data.data) {
    throw new Error(res.data.message || 'Failed to load statement')
  }
  return res.data.data
}
