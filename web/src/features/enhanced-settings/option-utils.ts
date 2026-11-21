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
 * GET /api/option/ 的读取辅助（hc25）。
 *
 * 后端 controller.GetOptions 返回的是**数组**：
 *   { success: true, data: [{ key: 'CustomHeadHTML', value: '...' }, ...] }
 *
 * 但扩展设置里的几个卡片一直按 map 取值（res.data.data['CustomHeadHTML']）。
 * 数组用字符串下标取永远是 undefined，于是：
 *  - 打开页面读不到已保存的值，界面永远停在默认值/空白，看起来「没保存上」；
 *  - 其实保存是成功的，一刷新又回到默认值 → 管理员只会看到「保存失败」。
 *
 * 这里把数组形态与历史 map 形态统一归一成 map，所有卡片只从这里读。
 */

export type OptionMap = Record<string, string>

function asText(value: unknown): string {
  if (typeof value === 'string') return value
  if (value == null) return ''
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

/** 剥掉 { success, data } / { data } 这层包装，最多剥两层（兼顾 axios response）。 */
function unwrap(payload: unknown): unknown {
  let current = payload
  for (let depth = 0; depth < 2; depth++) {
    if (!current || typeof current !== 'object' || Array.isArray(current)) break
    const record = current as Record<string, unknown>
    const isEnvelope =
      'data' in record && ('success' in record || 'message' in record)
    const isLoneData = 'data' in record && Object.keys(record).length === 1
    if (!isEnvelope && !isLoneData) break
    current = record.data
  }
  return current
}

/** 把 /api/option/ 的响应归一成 key -> value。 */
export function toOptionMap(payload: unknown): OptionMap {
  const data = unwrap(payload)
  const map: OptionMap = {}
  if (Array.isArray(data)) {
    for (const item of data) {
      if (!item || typeof item !== 'object') continue
      const entry = item as { key?: unknown; value?: unknown }
      if (typeof entry.key !== 'string' || !entry.key) continue
      map[entry.key] = asText(entry.value)
    }
    return map
  }
  if (data && typeof data === 'object') {
    for (const [key, value] of Object.entries(data as Record<string, unknown>)) {
      map[key] = asText(value)
    }
  }
  return map
}

/** 取单个配置项，缺失时返回空串（调用方按「未配置」处理）。 */
export function optionValueOf(payload: unknown, key: string): string {
  return toOptionMap(payload)[key] ?? ''
}
