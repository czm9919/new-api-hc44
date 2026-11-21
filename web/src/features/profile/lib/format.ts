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
import type { UserProfile, UserSettings } from '../types'

// ============================================================================
// Profile Formatting Utilities
// ============================================================================

/**
 * Parse user settings from JSON string
 */
export function parseUserSettings(settingsJson?: string): UserSettings {
  if (!settingsJson) return {}

  try {
    return JSON.parse(settingsJson) as UserSettings
  } catch {
    return {}
  }
}

/**
 * Get display name or fallback to username
 */
export function getDisplayName(user?: UserProfile): string {
  if (!user) return ''
  return user.display_name || user.username
}

/**
 * Get user initials for avatar
 */
export function getUserInitials(user?: UserProfile): string {
  if (!user) return '?'
  const name = getDisplayName(user)
  if (!name) return '?'

  const parts = name.trim().split(/\s+/)
  if (parts.length >= 2) {
    return `${parts[0][0]}${parts[1][0]}`.toUpperCase()
  }
  return name.slice(0, 2).toUpperCase()
}

export type ProfileQuotaSplit = {
  /** 总可用余额（user.quota，免费额度已包含在内） */
  total: number
  /** 免费额度（签到等赠送，user.free_quota，是 total 的一部分） */
  free: number
  /** 付费余额 = total - free */
  paid: number
}

/**
 * 把钱包的两个数字拆成「付费余额 / 免费额度 / 总可用」。
 *
 * user.quota 是工作余额**总额**，free_quota 只是其中标了「免费」的那部分，
 * 所以付费余额 = quota - free_quota。签到发奖时这两个字段同步 +奖励，
 * 付费余额不变 —— 页面若把 quota 直接当「余额」展示，签到后就会出现
 * 「付费额度」和「免费额度」两张卡同时上涨、看起来像重复发奖的假象。
 */
export function splitProfileQuota(
  quota?: number,
  freeQuota?: number
): ProfileQuotaSplit {
  const total = quota ?? 0
  const free = Math.max(0, freeQuota ?? 0)
  const paid = total - free
  // 总额为正时不允许出现负的付费余额（免费额度理论上不会超过总额，
  // 超了说明数据不一致，显示 0 比显示负数更不容易误导）；透支时保留负号。
  return {
    total,
    free,
    paid: total >= 0 && paid < 0 ? 0 : paid,
  }
}
