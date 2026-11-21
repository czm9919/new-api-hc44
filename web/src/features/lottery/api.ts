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
 * 大转盘抽奖 + QQ 绑定（v1.1.0 M2）前端类型与接口。
 *
 * 后端口径：
 *  - 奖池、每日次数、是否要求 QQ 全部来自 option，前端只渲染
 *  - 绑定动作不在面板完成：面板只拿一次性码，用户把码发给配套 AstrBot
 *    机器人，机器人用服务令牌调 /api/qq/agent/verify 落库
 */
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

/** 一个转盘扇区。type: quota=送额度 / none=谢谢参与。 */
export interface LotteryPrize {
  id: string
  name: string
  type: 'quota' | 'none'
  quota: number
  weight: number
}

export interface LotterySettings {
  enabled: boolean
  daily_spins: number
  require_qq: boolean
  prizes: LotteryPrize[]
  /** 付费抽奖开关：关闭时用户只能用每日免费次数。 */
  paid_enabled: boolean
  /** 单次付费抽奖价格（额度，只扣付费余额）。 */
  paid_price: number
  /** 每日付费抽奖次数上限（与免费次数分开计）。 */
  paid_daily_limit: number
}

export interface LotteryStatus {
  settings: LotterySettings
  spins_used: number
  spins_remaining: number
  /** 今日已付费抽奖次数。 */
  paid_spins_used: number
  /** 今日剩余付费抽奖次数。 */
  paid_spins_remaining: number
  /** 工作余额（含免费部分）。 */
  quota: number
  /** 付费余额（quota - free_quota），付费抽奖只认这个数。 */
  paid_quota: number
  /** 现在能不能付费抽一次，后端算好，前端直接用。 */
  paid_affordable: boolean
  qq_bound: boolean
  /** 已脱敏（例如 123****89）；未绑定为空串。 */
  qq: string
}

export interface LotterySpin {
  id: number
  user_id: number
  day_key: string
  spin_index: number
  prize_id: string
  prize_name: string
  prize_type: string
  quota_awarded: number
  /** 本次是否为付费抽奖。 */
  paid: boolean
  /** 本次花费的额度（免费抽奖为 0）。 */
  quota_cost: number
  created_at: number
}

export interface LotterySpinResult {
  prize: LotteryPrize
  /** 命中的扇区下标，前端据此把指针停在该扇区。 */
  prize_index: number
  quota_awarded: number
  spins_used: number
  spins_remaining: number
  paid_spins_used: number
  paid_spins_remaining: number
  paid: boolean
  quota_cost: number
  spin: LotterySpin
}

export interface QQBindStatus {
  bound: boolean
  qq: string
  bound_at: number
  agent_enabled: boolean
}

export interface QQBindCode {
  code: string
  expires_at: number
  ttl_seconds: number
}

export async function getLottery(): Promise<LotteryStatus> {
  const res = await api.get('/api/user/lottery')
  return requireServerSuccess(res.data)?.data
}

/** 抽一次奖。paid=true 走付费通道（扣付费余额，免费次数不消耗）。 */
export async function spinLottery(paid = false): Promise<LotterySpinResult> {
  const res = await api.post('/api/user/lottery/spin', { paid })
  return requireServerSuccess(res.data)?.data
}

export async function getLotteryRecords(
  limit = 20
): Promise<{ items: LotterySpin[] }> {
  const res = await api.get('/api/user/lottery/records', { params: { limit } })
  return requireServerSuccess(res.data)?.data
}

export async function getQQBindStatus(): Promise<QQBindStatus> {
  const res = await api.get('/api/user/qq/bind')
  return requireServerSuccess(res.data)?.data
}

export async function createQQBindCode(): Promise<QQBindCode> {
  const res = await api.post('/api/user/qq/bind/code')
  return requireServerSuccess(res.data)?.data
}

export async function unbindQQ(): Promise<void> {
  const res = await api.delete('/api/user/qq/bind')
  requireServerSuccess(res.data)
}

/** 管理端校验失败时后端返回的字段级错误。 */
export interface LotterySettingsFieldError {
  field: string
  message: string
}

/** 管理端看到的机器人接入配置（令牌只给尾号，不给明文）。 */
export interface QQAgentConfig {
  agent_configured: boolean
  /** 形如 ••••f0c1；未配置为空串。 */
  agent_token_hint: string
  require_for_free: boolean
  bound_users: number
  active_bind_codes: number
}

/** 轮换令牌的响应：明文只在这一次出现。 */
export interface QQAgentRotateResult {
  agent_token: string
  config: QQAgentConfig
}

export interface SaveQQAgentSettingsPayload {
  /** 留空表示不修改现有令牌。 */
  agent_token?: string
  /** 显式撤销令牌（与 agent_token 互斥）。 */
  clear_token?: boolean
  require_for_free: boolean
}

export async function getAdminQQSettings(): Promise<QQAgentConfig> {
  const res = await api.get('/api/user/lottery/qq/settings')
  return requireServerSuccess(res.data)?.data
}

export async function saveAdminQQSettings(
  payload: SaveQQAgentSettingsPayload
): Promise<QQAgentConfig> {
  const res = await api.put('/api/user/lottery/qq/settings', payload)
  return requireServerSuccess(res.data)?.data
}

/** 一键生成并启用新令牌；返回的明文请立刻复制到 AstrBot 配置里。 */
export async function rotateAdminQQToken(): Promise<QQAgentRotateResult> {
  const res = await api.post('/api/user/lottery/qq/settings/rotate')
  return requireServerSuccess(res.data)?.data
}

/** 撤销令牌：绑定码接口立刻回到 503。 */
export async function clearAdminQQToken(): Promise<QQAgentConfig> {
  const res = await api.delete('/api/user/lottery/qq/settings')
  return requireServerSuccess(res.data)?.data
}

export async function getAdminLotterySettings(): Promise<LotterySettings> {
  const res = await api.get('/api/user/lottery/settings')
  return requireServerSuccess(res.data)?.data
}

/** 保存奖池：后端逐字段校验，成功时返回**回读后的**配置。 */
export async function saveAdminLotterySettings(
  settings: LotterySettings
): Promise<LotterySettings> {
  const res = await api.put('/api/user/lottery/settings', settings)
  return requireServerSuccess(res.data)?.data
}

/** 恢复默认奖池（开关/次数/是否要求 QQ 一起回默认）。 */
export async function resetAdminLotterySettings(): Promise<LotterySettings> {
  const res = await api.post('/api/user/lottery/settings/reset')
  return requireServerSuccess(res.data)?.data
}

/**
 * 从任意异常里捞出后端字段级错误。
 *
 * 后端 400 的响应体是 `{success:false, code, message, fields:[{field,message}]}`，
 * axios 抛错时挂在 `error.response.data`；这里做防御式解析，拿不到就返回空数组，
 * 调用方回落到普通错误提示。
 */
export function extractSettingsFieldErrors(
  error: unknown
): LotterySettingsFieldError[] {
  const responseData = (
    error as { response?: { data?: unknown } } | null | undefined
  )?.response?.data
  const fields = (responseData as { fields?: unknown } | null | undefined)
    ?.fields
  if (!Array.isArray(fields)) return []
  return fields
    .filter(
      (item): item is LotterySettingsFieldError =>
        Boolean(item) &&
        typeof (item as LotterySettingsFieldError).field === 'string' &&
        typeof (item as LotterySettingsFieldError).message === 'string'
    )
    .map((item) => ({ field: item.field, message: item.message }))
}
