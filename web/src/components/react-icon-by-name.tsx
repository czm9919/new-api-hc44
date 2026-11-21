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
 * 按图标名渲染图标（支付方式、导航项等由管理员填写的组件名）。
 *
 * 图标在构建期就被渲染成静态 SVG（见 scripts/generate-icon-assets.mjs），
 * 运行时只用 CSS mask 引用对应文件：
 * - 命中一个图标只下载约 1~4KB，而不是整包几百 KB ~ 数 MB；
 * - mask 用 currentColor 上色，跟随文字色/暗色主题；
 * - 名称不存在或前缀不支持时什么都不渲染（与「无效名称不显示图标」语义一致）。
 */
import type { CSSProperties } from 'react'

import { resolveIconAssetPath } from '@/lib/icon-asset'

type ReactIconByNameProps = {
  name?: string | null
  className?: string
  /** 边长；数字按 px 处理，缺省 1em（跟随字号）。 */
  size?: number | string
  title?: string
  style?: CSSProperties
}

export function ReactIconByName({
  name,
  className,
  size,
  title,
  style,
}: ReactIconByNameProps) {
  const assetPath = resolveIconAssetPath(name)
  if (!assetPath) return null

  const dimension = typeof size === 'number' ? `${size}px` : (size ?? '1em')
  const maskStyle: CSSProperties = {
    display: 'inline-block',
    flexShrink: 0,
    width: dimension,
    height: dimension,
    backgroundColor: 'currentColor',
    maskImage: `url("${assetPath}")`,
    maskRepeat: 'no-repeat',
    maskPosition: 'center',
    maskSize: '100% 100%',
    WebkitMaskImage: `url("${assetPath}")`,
    WebkitMaskRepeat: 'no-repeat',
    WebkitMaskPosition: 'center',
    WebkitMaskSize: '100% 100%',
    ...style,
  }

  return (
    <span
      role={title ? 'img' : undefined}
      aria-label={title}
      title={title}
      className={className}
      style={maskStyle}
    />
  )
}
