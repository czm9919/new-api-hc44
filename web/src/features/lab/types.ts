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
 * 自研实验室（/lab）类型定义。全部字段来自管理员在扩展设置里填写的
 * LabSetting JSON（经 /api/lab/public/config 匿名下发），所有字段可选，
 * 页面对缺失字段逐项降级展示。
 */

export interface LabTimelineEntry {
  date?: string
  title: string
  detail?: string
}

export interface LabSponsorMethod {
  name: string
  url?: string
  qrcode_url?: string
}

export interface LabSponsor {
  target?: number
  raised?: number
  currency?: string
  note?: string
  methods?: LabSponsorMethod[]
}

export interface LabConfig {
  enabled?: boolean
  model_name?: string
  tagline?: string
  intro?: string
  params?: string
  hardware?: string
  context_tokens?: number
  speed_tps?: number
  status_url?: string
  demo_url?: string
  demo_embed?: boolean
  timeline?: LabTimelineEntry[]
  sponsor?: LabSponsor
  acknowledgements?: string[]
}

/** /api/lab/public/status 的响应：status 为推理服务器状态端点原文。 */
export interface LabStatusSnapshot {
  online: boolean
  configured: boolean
  error?: string
  status?: Record<string, unknown>
}

export interface LabConfigResponse {
  success: boolean
  message: string
  data: LabConfig
}

export interface LabStatusResponse {
  success: boolean
  message: string
  data: LabStatusSnapshot
}
