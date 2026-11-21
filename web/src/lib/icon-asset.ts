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
 * 图标组件名 → 静态 SVG 资源地址的映射规则。
 *
 * 管理员用 react-icons 组件名（如 SiAlipay）配置支付方式/导航图标，
 * 图标在构建期由 scripts/generate-icon-assets.mjs 物化成 /icons/<包>/<名>.svg。
 */

/** 构建期已物化的图标包；新增包需同步 scripts/generate-icon-assets.mjs。 */
const ICON_ASSET_PACKS = [
  // Simple Icons：品牌标识（SiAlipay / SiWechat / SiStripe …）
  { id: 'si', pattern: /^Si[A-Z0-9]/ },
  // Lucide：通用线性图标（LuCreditCard / LuWallet …）
  { id: 'lu', pattern: /^Lu[A-Z0-9]/ },
] as const

function normalizeIconName(name: string | null | undefined): string | null {
  const trimmed = name?.trim()
  if (!trimmed || !/^[A-Z][A-Za-z0-9]*$/.test(trimmed)) return null
  return trimmed
}

/** 把「组件名」解析成静态图标资源地址；不支持的名字返回 null。 */
export function resolveIconAssetPath(
  name: string | null | undefined
): string | null {
  const iconName = normalizeIconName(name)
  if (!iconName) return null
  const pack = ICON_ASSET_PACKS.find((entry) => entry.pattern.test(iconName))
  return pack ? `/icons/${pack.id}/${iconName}.svg` : null
}
