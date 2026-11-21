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

import { useStatus } from '@/hooks/use-status'

/**
 * 余额显示配置（管理员全局生效，hc23）。
 *
 * 背景：`user.quota` 是工作余额总额，其中 `free_quota` 是签到等赠送的免费部分，
 * 付费余额 = `quota - free_quota`。用户侧钱包页已拆开展示，管理员在用户管理列表里
 * 却只看到合并后的一个数，分不清「真金白银」和「赠送额度」。这里把展示方式做成
 * 可配置项，落库在 option `BalanceDisplaySetting`：
 *
 * ```json
 * {
 *   "split_enabled": true,
 *   "paid_label": "",
 *   "free_label": "",
 *   "hide_free": false,
 *   "show_in_user_list": true
 * }
 * ```
 *
 * 设计要点：
 *  - 字段一律「缺省即默认」，配置损坏 / 老版本没这个字段都不会让列表页崩掉；
 *  - `paid_label` / `free_label` 为空时走 i18n 默认文案（`Paid balance` / `Free quota`），
 *    避免把某一种语言的文案写死进配置；
 *  - `hide_free` 只在 `split_enabled` 为真时有意义（拆分关掉时本来就只有一个合计值）。
 */
export type BalanceDisplayConfig = {
  /** 拆分显示总开关：关闭后所有页面回到「可用余额」单值展示。 */
  splitEnabled: boolean
  /** 付费余额的自定义显示名；空串表示用默认翻译文案。 */
  paidLabel: string
  /** 免费额度的自定义显示名；空串表示用默认翻译文案。 */
  freeLabel: string
  /** 单独隐藏免费额度：拆分仍然开启，但不再展示免费那一行。 */
  hideFree: boolean
  /** 是否在「用户管理」列表里应用拆分展示（关闭后列表只显示合计余额）。 */
  showInUserList: boolean
}

export const DEFAULT_BALANCE_DISPLAY: BalanceDisplayConfig = {
  splitEnabled: true,
  paidLabel: '',
  freeLabel: '',
  hideFree: false,
  showInUserList: true,
}

function readBool(
  record: Record<string, unknown>,
  key: string,
  fallback: boolean
) {
  const value = record[key]
  if (typeof value === 'boolean') return value
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase()
    if (normalized === 'true') return true
    if (normalized === 'false') return false
  }
  return fallback
}

function readLabel(record: Record<string, unknown>, key: string): string {
  const value = record[key]
  return typeof value === 'string' ? value.trim() : ''
}

/**
 * 解析后端下发的配置。空值、非法 JSON、结构不符一律回退默认值。
 * 同时兼容「JSON 字符串」与「已经解析好的对象」两种形态，避免调用方各自判断。
 */
export function parseBalanceDisplay(value: unknown): BalanceDisplayConfig {
  let raw: unknown = value
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (trimmed === '') return DEFAULT_BALANCE_DISPLAY
    try {
      raw = JSON.parse(trimmed)
    } catch {
      return DEFAULT_BALANCE_DISPLAY
    }
  }
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    return DEFAULT_BALANCE_DISPLAY
  }
  const record = raw as Record<string, unknown>
  return {
    splitEnabled: readBool(record, 'split_enabled', true),
    paidLabel: readLabel(record, 'paid_label'),
    freeLabel: readLabel(record, 'free_label'),
    hideFree: readBool(record, 'hide_free', false),
    showInUserList: readBool(record, 'show_in_user_list', true),
  }
}

/** 序列化配置（保存到 option 前调用）。 */
export function serializeBalanceDisplay(config: BalanceDisplayConfig): string {
  return JSON.stringify({
    split_enabled: config.splitEnabled,
    paid_label: config.paidLabel.trim(),
    free_label: config.freeLabel.trim(),
    hide_free: config.hideFree,
    show_in_user_list: config.showInUserList,
  })
}

/**
 * 余额拆分是否生效。位置差异留给调用方判断
 * （例如用户管理列表还要求 `showInUserList`）。
 */
export function isSplitActive(config: BalanceDisplayConfig): boolean {
  return config.splitEnabled
}

/** 读取管理员全局余额显示配置（走共享的 /api/status 缓存，不额外发请求）。 */
export function useBalanceDisplay(): BalanceDisplayConfig {
  const { status } = useStatus()
  // /api/status 的响应体既有顶层字段也有 data 包一层，两个位置都兜一下
  const bag = status as unknown as Record<string, unknown> | null
  const dataBag = bag?.data as Record<string, unknown> | undefined
  const raw = bag?.BalanceDisplay ?? dataBag?.BalanceDisplay
  return useMemo(() => parseBalanceDisplay(raw), [raw])
}
