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
 * 导航栏配置卡片（管理员全局生效）。
 *
 * 能力：显示 / 隐藏分组与菜单项、给菜单项或分组改名、调整顺序。
 * 配置持久化在 option `SidebarNavCustom`，由 `useSidebarNavCustom` 在渲染侧边栏时叠加。
 *
 * 之所以做成「全局」而不是个人偏好：本站的侧边栏条目对应的是产品对外的功能面，
 * 由管理员统一决定哪些入口暴露给用户，比让每个用户自己关菜单更符合运营诉求
 * （个人偏好仍可在『个人资料 → 侧边栏个人设置』里单独收窄）。
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import {
  ArrowDown,
  ArrowUp,
  Eye,
  EyeOff,
  LayoutList,
  RotateCcw,
} from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  parseSidebarNavCustom,
  serializeSidebarNavCustom,
  type SidebarNavCustomConfig,
} from '@/hooks/use-sidebar-nav-custom'
import { api } from '@/lib/api'
import { NAV_CATALOG } from '@/lib/nav-catalog'
import { requireServerSuccess } from '@/lib/server-error-message'

import { optionValueOf } from './option-utils'
import { describeSaveFailure } from './save-error'

/** 导航栏全局配置在 option 表里的 key。 */
export const SIDEBAR_NAV_CUSTOM_OPTION_KEY = 'SidebarNavCustom'

type ItemDraft = {
  id: string
  title: string
  hidden: boolean
  label: string
}

type GroupDraft = {
  id: string
  title: string
  hidden: boolean
  label: string
  items: ItemDraft[]
}

/** 把「目录默认值 + 已保存配置」合并成可编辑的草稿（配置里的顺序优先）。 */
function buildDrafts(config: SidebarNavCustomConfig): GroupDraft[] {
  const configuredGroups = config.groups ?? []
  const configuredIds = new Set(configuredGroups.map((g) => g.id))
  const drafts: GroupDraft[] = []

  const pushGroup = (
    catalogGroup: (typeof NAV_CATALOG)[number],
    groupConfig?: SidebarNavCustomConfig['groups'][number]
  ) => {
    const configuredItems = groupConfig?.items ?? []
    const configuredItemIds = new Set(configuredItems.map((i) => i.id))
    const items: ItemDraft[] = []

    for (const itemConfig of configuredItems) {
      const catalogItem = catalogGroup.items.find(
        (candidate) => candidate.id === itemConfig.id
      )
      if (!catalogItem) continue
      items.push({
        id: catalogItem.id,
        title: catalogItem.titleKey,
        hidden: itemConfig.hidden === true,
        label: itemConfig.label ?? '',
      })
    }
    for (const catalogItem of catalogGroup.items) {
      if (configuredItemIds.has(catalogItem.id)) continue
      items.push({
        id: catalogItem.id,
        title: catalogItem.titleKey,
        hidden: false,
        label: '',
      })
    }

    drafts.push({
      id: catalogGroup.id,
      title: catalogGroup.titleKey,
      hidden: groupConfig?.hidden === true,
      label: groupConfig?.label ?? '',
      items,
    })
  }

  // 配置里出现过的分组先按配置顺序排，其余按目录顺序追加
  for (const groupConfig of configuredGroups) {
    const catalogGroup = NAV_CATALOG.find((g) => g.id === groupConfig.id)
    if (!catalogGroup) continue
    pushGroup(catalogGroup, groupConfig)
  }
  for (const catalogGroup of NAV_CATALOG) {
    if (configuredIds.has(catalogGroup.id)) continue
    pushGroup(catalogGroup)
  }

  return drafts
}

/** 草稿 → 持久化结构：数组顺序即展示顺序，空的 label 不落库。 */
function toConfig(drafts: GroupDraft[]): SidebarNavCustomConfig {
  return {
    groups: drafts.map((group) => {
      const groupConfig: SidebarNavCustomConfig['groups'][number] = {
        id: group.id,
        items: group.items.map((item) => {
          const itemConfig: { id: string; hidden?: boolean; label?: string } = {
            id: item.id,
          }
          if (item.hidden) itemConfig.hidden = true
          const label = item.label.trim()
          if (label) itemConfig.label = label
          return itemConfig
        }),
      }
      if (group.hidden) groupConfig.hidden = true
      const groupLabel = group.label.trim()
      if (groupLabel) groupConfig.label = groupLabel
      return groupConfig
    }),
  }
}

function move<T>(list: T[], index: number, delta: number): T[] {
  const target = index + delta
  if (target < 0 || target >= list.length) return list
  const next = [...list]
  const [moved] = next.splice(index, 1)
  next.splice(target, 0, moved)
  return next
}

export function NavCustomCard() {
  const queryClient = useQueryClient()
  const [drafts, setDrafts] = useState<GroupDraft[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const res = await api.get('/api/option/')
      // /api/option/ 返回 [{key,value}] 数组，直接按 key 下标取会是 undefined
      setDrafts(
        buildDrafts(
          parseSidebarNavCustom(
            optionValueOf(res.data, SIDEBAR_NAV_CUSTOM_OPTION_KEY)
          )
        )
      )
    } catch {
      setDrafts(buildDrafts(parseSidebarNavCustom('')))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const hiddenCount = useMemo(
    () =>
      drafts.reduce(
        (sum, group) =>
          sum + (group.hidden ? group.items.length : group.items.filter((i) => i.hidden).length),
        0
      ),
    [drafts]
  )

  const patchGroup = (groupIndex: number, next: Partial<GroupDraft>) => {
    setDrafts((prev) =>
      prev.map((group, index) =>
        index === groupIndex ? { ...group, ...next } : group
      )
    )
  }

  const patchItem = (
    groupIndex: number,
    itemIndex: number,
    next: Partial<ItemDraft>
  ) => {
    setDrafts((prev) =>
      prev.map((group, index) =>
        index === groupIndex
          ? {
              ...group,
              items: group.items.map((item, i) =>
                i === itemIndex ? { ...item, ...next } : item
              ),
            }
          : group
      )
    )
  }

  const save = async () => {
    setSaving(true)
    try {
      const res = await api.put('/api/option/', {
        key: SIDEBAR_NAV_CUSTOM_OPTION_KEY,
        value: serializeSidebarNavCustom(toConfig(drafts)),
      })
      requireServerSuccess(res.data)
      // 侧边栏读的是 /api/status 里的配置快照，必须让 status 失效后重新拉取才会生效
      queryClient.invalidateQueries({ queryKey: ['status'] })
      try {
        window.localStorage.removeItem('status')
      } catch {
        /* localStorage 可能被禁用 */
      }
      toast.success('导航栏配置已保存，刷新后对所有人生效')
    } catch (error) {
      toast.error(describeSaveFailure(error, '导航栏配置保存失败'))
    } finally {
      setSaving(false)
    }
  }

  const reset = () => {
    setDrafts(buildDrafts(parseSidebarNavCustom('')))
    toast.success('已恢复默认顺序与名称（记得点保存）')
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <LayoutList className='h-4 w-4' /> 导航栏配置
        </CardTitle>
        <CardDescription>
          全局生效：可隐藏菜单、改显示名、调整顺序，所有用户看到的侧边栏一致。
          与「系统设置 → 维护 → 侧边栏模块」的开关叠加生效（那边关掉的菜单这里也不会出现）。
          个人仍可在「个人资料 → 侧边栏个人设置」里进一步收窄自己的菜单。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-3'>
        {loading ? (
          <Skeleton className='h-48 w-full' />
        ) : (
          <>
            <div className='rounded-lg border bg-muted/40 px-3 py-2 text-xs text-muted-foreground'>
              当前隐藏 {hiddenCount} 个菜单项。隐藏管理组菜单后，对应页面仍可通过直接访问
              URL 进入，但侧边栏不再提供入口，请谨慎隐藏「系统设置」等唯一入口。
            </div>

            {drafts.map((group, groupIndex) => (
              <div key={group.id} className='rounded-xl border p-3'>
                <div className='flex flex-wrap items-center gap-2'>
                  <Switch
                    checked={!group.hidden}
                    onCheckedChange={(v) =>
                      patchGroup(groupIndex, { hidden: !v })
                    }
                  />
                  <span className='flex items-center gap-1.5 text-sm font-medium'>
                    {group.hidden ? (
                      <EyeOff className='h-3.5 w-3.5' />
                    ) : (
                      <Eye className='h-3.5 w-3.5' />
                    )}
                    {group.title}
                  </span>
                  <Input
                    className='h-8 max-w-60'
                    value={group.label}
                    placeholder={`分组显示名（默认：${group.title}）`}
                    onChange={(e) =>
                      patchGroup(groupIndex, { label: e.target.value })
                    }
                  />
                  <div className='ml-auto flex items-center gap-1'>
                    <Button
                      size='icon'
                      variant='ghost'
                      className='h-8 w-8'
                      disabled={groupIndex === 0}
                      onClick={() =>
                        setDrafts((prev) => move(prev, groupIndex, -1))
                      }
                    >
                      <ArrowUp className='h-4 w-4' />
                    </Button>
                    <Button
                      size='icon'
                      variant='ghost'
                      className='h-8 w-8'
                      disabled={groupIndex === drafts.length - 1}
                      onClick={() =>
                        setDrafts((prev) => move(prev, groupIndex, 1))
                      }
                    >
                      <ArrowDown className='h-4 w-4' />
                    </Button>
                  </div>
                </div>

                {group.hidden ? (
                  <p className='mt-2 text-xs text-muted-foreground'>
                    整个分组已隐藏，对所有人不可见。
                  </p>
                ) : (
                  <div className='mt-3 space-y-2'>
                    {group.items.map((item, itemIndex) => (
                      <div
                        key={item.id}
                        className='flex flex-wrap items-center gap-2 rounded-lg border bg-background/60 px-3 py-2'
                      >
                        <Switch
                          checked={!item.hidden}
                          onCheckedChange={(v) =>
                            patchItem(groupIndex, itemIndex, { hidden: !v })
                          }
                        />
                        <span
                          className={
                            item.hidden
                              ? 'min-w-28 text-sm text-muted-foreground line-through'
                              : 'min-w-28 text-sm'
                          }
                        >
                          {item.title}
                        </span>
                        <Input
                          className='h-8 max-w-60'
                          value={item.label}
                          placeholder={`显示名（默认：${item.title}）`}
                          onChange={(e) =>
                            patchItem(groupIndex, itemIndex, {
                              label: e.target.value,
                            })
                          }
                        />
                        <div className='ml-auto flex items-center gap-1'>
                          <Button
                            size='icon'
                            variant='ghost'
                            className='h-8 w-8'
                            disabled={itemIndex === 0}
                            onClick={() =>
                              setDrafts((prev) =>
                                prev.map((g, gi) =>
                                  gi === groupIndex
                                    ? { ...g, items: move(g.items, itemIndex, -1) }
                                    : g
                                )
                              )
                            }
                          >
                            <ArrowUp className='h-4 w-4' />
                          </Button>
                          <Button
                            size='icon'
                            variant='ghost'
                            className='h-8 w-8'
                            disabled={itemIndex === group.items.length - 1}
                            onClick={() =>
                              setDrafts((prev) =>
                                prev.map((g, gi) =>
                                  gi === groupIndex
                                    ? { ...g, items: move(g.items, itemIndex, 1) }
                                    : g
                                )
                              )
                            }
                          >
                            <ArrowDown className='h-4 w-4' />
                          </Button>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ))}

            <div className='flex flex-col-reverse gap-2 border-t pt-4 sm:flex-row sm:justify-end'>
              <Button variant='outline' onClick={reset}>
                <RotateCcw className='mr-1 h-4 w-4' /> 恢复默认
              </Button>
              <Button onClick={save} disabled={saving}>
                {saving ? '保存中…' : '保存导航栏配置'}
              </Button>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
