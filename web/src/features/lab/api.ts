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

import type { LabConfigResponse, LabStatusResponse } from './types'

/** 公开接口：自研实验室展示配置（匿名可读）。 */
export async function getLabConfig() {
  const res = await api.get<LabConfigResponse>('/api/lab/public/config')
  return res.data
}

/** 公开接口：推理服务器状态代理（后端 5s 缓存，前端只需低频轮询）。 */
export async function getLabStatus() {
  const res = await api.get<LabStatusResponse>('/api/lab/public/status')
  return res.data
}
