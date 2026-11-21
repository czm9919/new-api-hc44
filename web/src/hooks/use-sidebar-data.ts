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
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'

import type { NavItem, SidebarBadges, SidebarData } from '@/components/layout/types'
import { useSidebarNavCustom } from '@/hooks/use-sidebar-nav-custom'
import { NAV_CATALOG, type NavCatalogItem } from '@/lib/nav-catalog'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

/**
 * Root navigation groups for the application sidebar.
 *
 * The navigation definition itself lives in `@/lib/nav-catalog` (single source of
 * truth shared with the admin "navigation customisation" panel); this hook only
 * localises it, applies the *global* admin customisation (hide / rename / reorder)
 * and then drops entries the current role cannot see.
 *
 * These are shown when the URL does not match any nested sidebar view
 * registered in `layout/lib/sidebar-view-registry.ts`.
 */
export function useSidebarData(badges: SidebarBadges = {}): SidebarData {
  const { t } = useTranslation()
  const { auth } = useAuthStore()
  const userRole = auth?.user?.role ?? ROLE.GUEST
  const navCustom = useSidebarNavCustom()

  const navGroups: SidebarData['navGroups'] = NAV_CATALOG.map((group) => ({
    id: group.id,
    title: t(group.titleKey),
    items: group.items.map((item) => toNavItem(item, t, badges)),
  }))

  // requiredRole 过滤：低于所需角色的菜单项直接不渲染。
  // 此前该字段只有声明没有消费点，普通用户也能看到整个管理组菜单；
  // 页面路由虽有 beforeLoad 守卫会被重定向到 403，但菜单入口不该暴露。
  const visibleGroups = navCustom
    .apply(navGroups)
    .map((group) => ({
      ...group,
      items: group.items.filter(
        (item) => item.requiredRole === undefined || userRole >= item.requiredRole
      ),
    }))
    .filter((group) => group.items.length > 0)

  return { navGroups: visibleGroups }
}

/** 把目录项转换成侧边栏渲染用的 NavItem（区分动态 chat-presets 与普通链接）。 */
function toNavItem(item: NavCatalogItem, t: TFunction, badges: SidebarBadges): NavItem {
  const base = {
    id: item.id,
    title: t(item.titleKey),
    icon: item.icon,
    requiredRole: item.requiredRole,
    activeUrls: item.activeUrls,
    configUrls: item.configUrls,
  }
  if (item.type === 'chat-presets') {
    return { ...base, type: 'chat-presets' }
  }
  // Model pricing lives in System Settings（与上游行为一致）。
  if (item.id === 'admin.systemSettings') {
    return { ...base, url: item.url ?? '', badge: badges.modelPricing }
  }
  return { ...base, url: item.url ?? '' }
}
