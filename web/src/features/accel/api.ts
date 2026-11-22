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
/*
 * 加速专线（hc48）前端接口。
 *
 * 后端口径：
 *  - 套餐解锁的是「分组」，用户拿到 group_name 后要把它填到令牌的分组上；
 *  - 月费只从付费余额扣（quota - free_quota），手机额度（免费部分）用不了；
 *  - 自动续费关闭后，到期前仍可用，到期自然失效，不做按天退款。
 */
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export interface AccelPlan {
  id: number
  name: string
  group_name: string
  /** 月费，额度单位。 */
  price_per_month: number
  /** 单用户并发上限，0 为不限。 */
  concurrency: number
  description: string
  enabled: boolean
  sort_order: number
}

export interface AccelSubscription {
  id: number
  plan_id: number
  plan_name: string
  group_name: string
  start_at: number
  expire_at: number
  auto_renew: boolean
  status: string
  active: boolean
  /** 剩余天数，向上取整。 */
  days_left: number
  price_per_month: number
  total_charged: number
  /** 最近一次续费失败原因（如余额不足），正常为空串。 */
  last_charge_error: string
}

export interface AccelOverview {
  plans: AccelPlan[]
  subscription: AccelSubscription | null
  /** 用户默认分组，用于提示「token 不设分组时走的就是它」。 */
  user_group: string
  /** 付费余额（quota - free_quota）。 */
  paid_balance: number
  server_time: number
}

export interface AccelPlanInput {
  name: string
  group_name: string
  price_per_month: number
  concurrency: number
  description: string
  enabled: boolean
  sort_order: number
}

export interface AccelSubRow extends AccelSubscription {
  user_id: number
  username: string
  created_at: number
  updated_at: number
}

export async function getAccelOverview(): Promise<AccelOverview> {
  const res = await api.get('/api/user/accel')
  return requireServerSuccess(res.data)?.data
}

export async function subscribeAccel(planId: number): Promise<AccelSubscription> {
  const res = await api.post('/api/user/accel/subscribe', { plan_id: planId })
  return requireServerSuccess(res.data)?.data
}

export async function setAccelAutoRenew(
  autoRenew: boolean
): Promise<{ auto_renew: boolean }> {
  const res = await api.post('/api/user/accel/auto_renew', {
    auto_renew: autoRenew,
  })
  return requireServerSuccess(res.data)?.data
}

export async function listAccelPlans(): Promise<AccelPlan[]> {
  const res = await api.get('/api/user/accel/plans')
  return requireServerSuccess(res.data)?.data ?? []
}

export async function createAccelPlan(
  input: AccelPlanInput
): Promise<AccelPlan> {
  const res = await api.post('/api/user/accel/plans', input)
  return requireServerSuccess(res.data)?.data
}

export async function updateAccelPlan(
  id: number,
  input: AccelPlanInput
): Promise<AccelPlan> {
  const res = await api.put(`/api/user/accel/plans/${id}`, input)
  return requireServerSuccess(res.data)?.data
}

export async function deleteAccelPlan(id: number): Promise<void> {
  const res = await api.delete(`/api/user/accel/plans/${id}`)
  requireServerSuccess(res.data)
}
