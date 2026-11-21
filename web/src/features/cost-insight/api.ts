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
 * 成本优化建议（hc31 · B89）前端类型与接口。
 *
 * 后端只读聚合「本人窗口内的消费日志 × 模型计价」，返回两个维度：
 *  - models：每个模型的用量、占比与折算倍率（用于展示钱花在哪）；
 *  - suggestions：可执行的省钱动作（更便宜的替代模型 / 单一模型占比过高），
 *    其中 estimated_saving_percent 是 0~1 的小数，展示时再乘 100。
 */
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

/** 单个模型在窗口内的用量与计价信息。 */
export interface CostInsightModel {
  model_name: string
  used_quota: number
  /** 该模型占窗口内总消耗的比例（0~1）。 */
  quota_share: number
  request_count: number
  prompt_tokens: number
  completion_tokens: number
  model_ratio: number
  completion_ratio: number
  /** 按该模型自身 token 构成折算出的综合倍率（按次计费时退化为 model_ratio）。 */
  effective_ratio: number
}

/** 单个更便宜替代模型的预估收益。 */
export interface CostInsightAlternative {
  model_name: string
  model_ratio: number
  effective_ratio: number
  /** 假设把该模型全部流量迁到替代模型，窗口内可省额度。 */
  estimated_saving_quota: number
  /** 0~1 的节省比例。 */
  estimated_saving_percent: number
}

/** 一条成本优化建议。 */
export interface CostInsightSuggestion {
  model_name: string
  /** cheaper_alternative / dominant_model */
  reason_code: string
  /** 后端生成的中文说明，可直接展示。 */
  reason: string
  model: CostInsightModel
  alternatives: CostInsightAlternative[]
}

/** 成本优化建议总览。 */
export interface CostInsight {
  window_days: number
  generated_at: number
  total_used_quota: number
  total_requests: number
  models: CostInsightModel[]
  suggestions: CostInsightSuggestion[]
}

/** 回看窗口选项：与后端 CostInsightMaxDays（90）保持一致。 */
export const COST_INSIGHT_DAY_OPTIONS = [
  { value: '7', labelKey: 'Last 7 days' },
  { value: '30', labelKey: 'Last 30 days' },
  { value: '90', labelKey: 'Last 90 days' },
] as const

/** 读取当前用户的成本优化建议。 */
export async function getCostInsight(days: number): Promise<CostInsight> {
  const res = await api.get('/api/user/cost-insight', { params: { days } })
  return requireServerSuccess(res.data)?.data
}
