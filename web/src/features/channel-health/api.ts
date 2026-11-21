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
 * 渠道健康总览（hc35 · B13–B15）前端类型与接口。
 *
 * 数据来自智能路由的进程内健康记忆——成功/失败按半衰期衰减计数、
 * 首字延迟 EWMA、失败冷却。之前只喂给路由算法，运营台看不到；
 * 这个接口把它只读暴露出来。进程重启后记忆冷启动（全空）属预期行为。
 */
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

/** 渠道健康单行。success_rate / samples 均为衰减后的值。 */
export interface ChannelHealthRow {
  channel_id: number
  name: string
  type: number
  status: number
  tag: string
  group: string
  priority: number
  weight: number
  has_samples: boolean
  /** 0~1；样本为 0 时无意义。 */
  success_rate: number
  samples: number
  /** 首字延迟 EWMA（毫秒），未知为 0。 */
  ttft_ms: number
  /** 非流式总耗时 EWMA（毫秒），未知为 0。 */
  latency_ms: number
  cooling_down: boolean
  active_count: number
}

/** 模型级健康（key: group|model），auto 智能选型的信号源。 */
export interface ModelHealthRow {
  group: string
  model: string
  success_rate: number
  samples: number
  ttft_ms: number
}

export interface ChannelHealthSummary {
  total: number
  with_samples: number
  cooling_down: number
  /** 样本充足（>= min_samples）且成功率 < 50% 的渠道数。 */
  degraded: number
}

export interface ChannelHealthSettings {
  half_life_minutes: number
  fail_cooldown_seconds: number
  min_samples: number
  load_penalty: number
}

export interface ChannelHealthOverview {
  channels: ChannelHealthRow[]
  models: ModelHealthRow[]
  summary: ChannelHealthSummary
  settings: ChannelHealthSettings
  generated_at: number
}

/** 渠道状态：1 启用 / 2 手动禁用 / 3 自动禁用（与后端 common 常量一致）。 */
export const CHANNEL_HEALTH_STATUS = {
  ENABLED: 1,
  MANUALLY_DISABLED: 2,
  AUTO_DISABLED: 3,
} as const

/** 成功率低于该值视为需要注意（与后端 degraded 口径一致）。 */
export const CHANNEL_HEALTH_DEGRADED_RATE = 0.5

/** 读取渠道健康总览（管理员 / 渠道读权限）。 */
export async function getChannelHealthOverview(): Promise<ChannelHealthOverview> {
  const res = await api.get('/api/channel/health/overview')
  return requireServerSuccess(res.data)?.data
}
