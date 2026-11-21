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
import { useMemo } from 'react'

import type { NavGroup, NavItem } from '@/components/layout/types'
import { useStatus } from '@/hooks/use-status'

/**
 * 导航栏自定义配置（管理员全局生效）。
 *
 * 持久化在 option `SidebarNavCustom` 里，结构刻意用「数组即顺序」：
 *
 * ```json
 * {
 *   "groups": [
 *     { "id": "general", "label": "工作台",
 *       "items": [{ "id": "general.overview" }, { "id": "general.keys", "hidden": true }] },
 *     { "id": "admin", "hidden": true }
 *   ]
 * }
 * ```
 *
 * 设计要点：
 *  - 只记录「与默认值的差异」：没出现在数组里的分组/菜单项按默认顺序排在后面，
 *    仍然可见。这样升级后新增的菜单不会因为老配置而被悄悄藏掉；
 *  - 数组顺序 = 展示顺序，不需要额外的 order 字段，也不会出现 order 撞车；
 *  - label 为空表示沿用默认（已翻译的）标题，避免把某一种语言的文案写死进配置。
 */
export type NavCustomItemConfig = {
  id: string
  hidden?: boolean
  /** 自定义显示名；空/缺省表示沿用默认标题 */
  label?: string
}

export type NavCustomGroupConfig = {
  id: string
  hidden?: boolean
  label?: string
  items?: NavCustomItemConfig[]
}

export type SidebarNavCustomConfig = {
  groups: NavCustomGroupConfig[]
}

const EMPTY_CONFIG: SidebarNavCustomConfig = { groups: [] }

function normalizeItem(raw: unknown): NavCustomItemConfig | null {
  if (!raw || typeof raw !== 'object') return null
  const record = raw as Record<string, unknown>
  const id = typeof record.id === 'string' ? record.id.trim() : ''
  if (!id) return null
  const item: NavCustomItemConfig = { id }
  if (record.hidden === true) item.hidden = true
  if (typeof record.label === 'string' && record.label.trim() !== '') {
    item.label = record.label.trim()
  }
  return item
}

function normalizeGroup(raw: unknown): NavCustomGroupConfig | null {
  if (!raw || typeof raw !== 'object') return null
  const record = raw as Record<string, unknown>
  const id = typeof record.id === 'string' ? record.id.trim() : ''
  if (!id) return null
  const group: NavCustomGroupConfig = { id }
  if (record.hidden === true) group.hidden = true
  if (typeof record.label === 'string' && record.label.trim() !== '') {
    group.label = record.label.trim()
  }
  if (Array.isArray(record.items)) {
    const items: NavCustomItemConfig[] = []
    const seen = new Set<string>()
    for (const rawItem of record.items) {
      const item = normalizeItem(rawItem)
      if (!item || seen.has(item.id)) continue
      seen.add(item.id)
      items.push(item)
    }
    group.items = items
  }
  return group
}

/**
 * 解析后端返回的配置字符串。空值、非法 JSON、结构不符一律回退成「空配置」
 * （= 全部菜单按默认顺序显示），保证导航栏永远不会因为配置损坏而整块消失。
 */
export function parseSidebarNavCustom(value: unknown): SidebarNavCustomConfig {
  if (typeof value !== 'string' || value.trim() === '') return EMPTY_CONFIG
  try {
    const parsed: unknown = JSON.parse(value)
    if (!parsed || typeof parsed !== 'object') return EMPTY_CONFIG
    const groupsRaw = (parsed as Record<string, unknown>).groups
    if (!Array.isArray(groupsRaw)) return EMPTY_CONFIG
    const groups: NavCustomGroupConfig[] = []
    const seen = new Set<string>()
    for (const rawGroup of groupsRaw) {
      const group = normalizeGroup(rawGroup)
      if (!group || seen.has(group.id)) continue
      seen.add(group.id)
      groups.push(group)
    }
    return { groups }
  } catch {
    return EMPTY_CONFIG
  }
}

/** 序列化配置（保存到 option 前调用）。 */
export function serializeSidebarNavCustom(
  config: SidebarNavCustomConfig
): string {
  return JSON.stringify(config)
}

function applyItemLabel(item: NavItem, label?: string): NavItem {
  const trimmed = label?.trim()
  if (!trimmed || trimmed === item.title) return item
  return { ...item, title: trimmed }
}

function applyGroupConfig(
  group: NavGroup,
  config: NavCustomGroupConfig
): NavGroup {
  const configured = config.items ?? []
  const configuredIds = new Set(configured.map((item) => item.id))
  const items: NavItem[] = []

  // 1) 先按配置顺序输出该分组内已配置的菜单项（隐藏的直接跳过）
  for (const itemConfig of configured) {
    const item = group.items.find((candidate) => candidate.id === itemConfig.id)
    if (!item || itemConfig.hidden) continue
    items.push(applyItemLabel(item, itemConfig.label))
  }

  // 2) 配置里没提到的菜单项（通常是新版本新增的）追加在后面，保持默认顺序
  for (const item of group.items) {
    if (item.id && configuredIds.has(item.id)) continue
    items.push(item)
  }

  const title = config.label?.trim()
  return { ...group, title: title || group.title, items }
}

/**
 * 把管理员配置叠加到默认导航上。
 *
 * - 分组/菜单项 `hidden: true` → 全局隐藏；
 * - 配置数组的顺序 → 展示顺序；
 * - 未出现在配置里的分组/菜单项 → 追加在末尾且保持可见（新功能不会被老配置吃掉）。
 */
export function applySidebarNavCustom(
  navGroups: NavGroup[],
  config: SidebarNavCustomConfig
): NavGroup[] {
  const configuredGroups = config.groups ?? []
  if (configuredGroups.length === 0) return navGroups

  const configuredIds = new Set(configuredGroups.map((group) => group.id))
  const ordered: NavGroup[] = []

  for (const groupConfig of configuredGroups) {
    // 没有 id 的分组（理论上是嵌套视图的动态项）不参与自定义
    const group = navGroups.find((candidate) => candidate.id === groupConfig.id)
    if (!group || groupConfig.hidden) continue
    ordered.push(applyGroupConfig(group, groupConfig))
  }

  for (const group of navGroups) {
    if (group.id && configuredIds.has(group.id)) continue
    ordered.push(group)
  }

  return ordered
}

/**
 * 读取管理员全局导航栏配置，并返回一个把配置叠加到任意导航组上的函数。
 */
export function useSidebarNavCustom(): {
  config: SidebarNavCustomConfig
  apply: (navGroups: NavGroup[]) => NavGroup[]
} {
  const { status } = useStatus()
  const raw = status?.SidebarNavCustom
  const config = useMemo(() => parseSidebarNavCustom(raw), [raw])
  const apply = useMemo(
    () => (navGroups: NavGroup[]) => applySidebarNavCustom(navGroups, config),
    [config]
  )
  return { config, apply }
}
