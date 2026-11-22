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
 * 加速专线（hc48 / hc49）前端接口。
 *
 * 后端口径：
 *  - 套餐解锁的是「分组」，用户拿到 group_name 后要把它填到令牌的分组上；
 *  - 月费以**美元**报价（price_usd），只从付费余额扣，免费额度用不了；
 *  - 自动续费关闭后，到期前仍可用，到期自然失效，不做按天退款；
 *  - 用量（usage）按分组从消费日志聚合，是「这条线到底用了多少」的唯一来源。
 */
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

/** 一段窗口内的加速专线用量。 */
export interface AccelUsage {
  requests: number
  /** 内部额度，仅用于对账，页面不要直接展示。 */
  quota: number
  /** 美元金额，页面展示用这个。 */
  quota_usd: number
  /** 最后一次消费时间，0 表示窗口内没有使用。 */
  last_used_at: number
}

/** 服务端下发的输入约束，避免前后端各写一份阈值。 */
export interface AccelLimits {
  max_price_usd: number
  price_step_usd: number
  max_concurrency: number
  max_extend_days: number
}

export interface AccelPlan {
  id: number
  name: string
  group_name: string
  /** 月费，额度单位（内部计费口径，不要直接展示）。 */
  price_per_month: number
  /** 月费的美元报价，页面展示与输入都用这个。 */
  price_usd: number
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
  /** 月费快照的美元报价。 */
  price_usd: number
  total_charged: number
  /** 累计扣费的美元金额。 */
  total_charged_usd: number
  /** 最近一次续费失败原因（如余额不足），正常为空串。 */
  last_charge_error: string
}

/** 入站加速接入地址（hc50）。host 为空表示管理员还没配，整块隐藏。 */
export interface AccelIngress {
  host: string
  /** hc52：个人水印前缀（8 位 hex），一人一份、泄露可溯源。订阅生效后才有。 */
  prefix?: string
  base_url: string
  /** 可直接粘贴的 curl 示例。 */
  example: string
  /** hc51：已配置加速域名但当前用户没有生效订阅——地址不下发，前端展示订阅引导。 */
  locked?: boolean
}

export interface AccelOverview {
  plans: AccelPlan[]
  subscription: AccelSubscription | null
  /** 用户默认分组，用于提示「token 不设分组时走的就是它」。 */
  user_group: string
  /** 付费余额（quota - free_quota），内部额度。 */
  paid_balance: number
  /** 付费余额的美元报价。 */
  paid_balance_usd: number
  /** 本期用量（当前订阅周期内）；未订阅为 null。 */
  usage: AccelUsage | null
  /** 今日用量；未订阅为 null。 */
  today_usage: AccelUsage | null
  limits: AccelLimits
  ingress: AccelIngress
  /** 并发计数后端：redis 为多实例共享上限，memory 仅单实例准确。 */
  slot_backend: 'redis' | 'memory' | string
  server_time: number
}

/** 一条加速线路（就是一个绑了专线出口的渠道，已脱敏）。 */
export interface AccelLine {
  channel_id: number
  name: string
  status: number
  /** enabled / disabled / auto_disabled / unknown */
  status_text: string
  priority: number
  weight: number
  /** 该线路覆盖的模型数，0 表示不限（all）。 */
  models: number
  has_samples: boolean
  success_rate: number
  samples: number
  ttft_ms: number
  latency_ms: number
  cooling_down: boolean
  active: number
  /** 样本充足且成功率低于阈值。 */
  degraded: boolean
}

export interface AccelLineSummary {
  total: number
  available: number
  degraded: number
  cooling_down: number
  /** 分组下所有线路都不可用（不等于没有线路）。 */
  all_down: boolean
}

export interface AccelLinesResponse {
  group: string
  lines: AccelLine[]
  summary: AccelLineSummary
  /** 用户端是否允许展示（管理员可整体关闭，或进程刚重启无样本）。 */
  visible: boolean
  slot_backend: string
  degraded_percent: number
}

export interface AccelPlanInput {
  name: string
  group_name: string
  /** 月费，美元。服务端负责换算成额度。 */
  price_usd: number
  concurrency: number
  description: string
  enabled: boolean
  sort_order: number
}

export interface AccelSubRow {
  user_id: number
  username: string
  plan_name: string
  group_name: string
  start_at: number
  expire_at: number
  auto_renew: boolean
  status: string
  days_left: number
  price_usd: number
  total_charged_usd: number
  last_charge_error: string
}

export interface AccelSubPage {
  page: number
  page_size: number
  total: number
  items: AccelSubRow[]
}

export async function getAccelOverview(): Promise<AccelOverview> {
  const res = await api.get('/api/user/accel')
  return requireServerSuccess(res.data)?.data
}

/**
 * 开通 / 换套餐。
 * switchPlan=true 表示「生效中换套餐」：会按新价重新扣一个月并**作废剩余时长**，
 * 必须由用户二次确认后再传，否则服务端返回 409。
 */
export async function subscribeAccel(
  planId: number,
  switchPlan = false
): Promise<AccelSubscription> {
  const res = await api.post('/api/user/accel/subscribe', {
    plan_id: planId,
    switch: switchPlan,
  })
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

/** 管理端：订阅列表（含已到期）。 */
export async function listAccelSubs(
  page = 1,
  pageSize = 20
): Promise<AccelSubPage> {
  const res = await api.get('/api/user/accel/subscriptions', {
    params: { p: page, page_size: pageSize },
  })
  return requireServerSuccess(res.data)?.data
}

/** 管理端：补时长（不扣费）。 */
export async function extendAccelSub(
  userId: number,
  days: number
): Promise<AccelSubscription> {
  const res = await api.post('/api/user/accel/subscriptions/extend', {
    user_id: userId,
    days,
  })
  return requireServerSuccess(res.data)?.data
}

/** 管理端：强制停用（不退款）。 */
export async function revokeAccelSub(
  userId: number,
  reason: string
): Promise<void> {
  const res = await api.post('/api/user/accel/subscriptions/revoke', {
    user_id: userId,
    reason,
  })
  requireServerSuccess(res.data)
}

/** 我的线路状态（脱敏：无出口地址、无密钥）。未订阅时返回空列表。 */
export async function getAccelLines(): Promise<AccelLinesResponse> {
  const res = await api.get('/api/user/accel/lines')
  return requireServerSuccess(res.data)?.data
}

/** 管理端：按分组看全部线路（含已禁用）。 */
export async function listAccelLines(group: string): Promise<AccelLinesResponse> {
  const res = await api.get('/api/user/accel/lines', { params: { group } })
  return requireServerSuccess(res.data)?.data
}

/** 管理端：加速域名守卫计数（hc52）。拦截数突增说明域名被传播。 */
export interface AccelIngressStats {
  allowed: number
  denied: number
  ingress_host: string
  global_limit: number
  slot_backend: string
}

export async function getAccelIngressStats(): Promise<AccelIngressStats> {
  const res = await api.get('/api/user/accel/ingress/stats')
  return requireServerSuccess(res.data)?.data
}
