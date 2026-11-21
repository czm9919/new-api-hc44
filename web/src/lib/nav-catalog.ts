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
 * 侧边栏导航目录（单一事实来源）。
 *
 * hc22 之前，根导航写死在 `use-sidebar-data.ts` 里，没有任何稳定标识，
 * 因此「管理员自定义导航栏」无从下手：既无法对某个菜单项持久化开关/改名/排序，
 * 也无法在管理页里列出可配置的菜单。
 *
 * 这里把导航定义抽成一份纯数据目录：
 *   - 每个分组、每个菜单项都有稳定 id（不随文案/路径变化）；
 *   - 标题只存 i18n key，渲染时再翻译，避免切换语言后配置失效；
 *   - 图标与角色要求一并放在这里，侧边栏与管理页共用同一份定义。
 */
import {
  Activity,
  Box,
  ClipboardList,
  CreditCard,
  FileText,
  FlaskConical,
  Key,
  KeyRound,
  LayoutDashboard,
  ListTodo,
  type LucideIcon,
  MessageSquare,
  PlugZap,
  Radio,
  ServerCog,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Ticket,
  User,
  Users,
  Wallet,
  Workflow,
} from 'lucide-react'

import { ROLE } from '@/lib/roles'

export type NavCatalogItem = {
  /** 稳定标识：形如 `<groupId>.<itemId>`，用于持久化导航自定义配置 */
  id: string
  /** 默认标题的 i18n key */
  titleKey: string
  /** 目标路径（chat-presets 这类动态项没有固定路径） */
  url?: string
  icon: LucideIcon
  /** 动态聊天预设列表项 */
  type?: 'chat-presets'
  activeUrls?: string[]
  configUrls?: string[]
  /** 低于该角色不渲染 */
  requiredRole?: number
}

export type NavCatalogGroup = {
  /** 稳定分组标识，用于持久化分组开关/顺序 */
  id: string
  /** 分组标题的 i18n key */
  titleKey: string
  items: NavCatalogItem[]
}

/**
 * 根导航目录。
 *
 * 顺序即默认顺序；管理员在「扩展设置 → 导航栏配置」里调整的顺序、
 * 改名与隐藏项都叠加在这份默认值之上。
 */
export const NAV_CATALOG: NavCatalogGroup[] = [
  {
    id: 'chat',
    titleKey: 'Chat',
    items: [
      {
        id: 'chat.playground',
        titleKey: 'Playground',
        url: '/playground',
        icon: FlaskConical,
      },
      {
        id: 'chat.presets',
        titleKey: 'Chat',
        type: 'chat-presets',
        icon: MessageSquare,
      },
    ],
  },
  {
    id: 'general',
    titleKey: 'General',
    items: [
      {
        id: 'general.overview',
        titleKey: 'Overview',
        url: '/dashboard/overview',
        icon: Activity,
      },
      {
        id: 'general.dashboard',
        titleKey: 'Dashboard',
        url: '/dashboard/models',
        icon: LayoutDashboard,
      },
      {
        id: 'general.keys',
        titleKey: 'API Keys',
        url: '/keys',
        icon: Key,
      },
      {
        id: 'general.usageLogs',
        titleKey: 'Usage Logs',
        url: '/usage-logs/common',
        icon: FileText,
      },
      {
        id: 'general.auditLogs',
        titleKey: 'Audit Logs',
        url: '/usage-logs/audit',
        icon: ClipboardList,
      },
      {
        id: 'general.taskLogs',
        titleKey: 'Task Logs',
        url: '/usage-logs/task',
        activeUrls: ['/usage-logs/drawing'],
        configUrls: ['/usage-logs/drawing', '/usage-logs/task'],
        icon: ListTodo,
      },
    ],
  },
  {
    id: 'personal',
    titleKey: 'Personal',
    items: [
      {
        id: 'personal.wallet',
        titleKey: 'Wallet',
        url: '/wallet',
        icon: Wallet,
      },
      {
        id: 'personal.profile',
        titleKey: 'Profile',
        url: '/profile',
        icon: User,
      },
      {
        id: 'personal.security',
        titleKey: 'Security & Access',
        url: '/security',
        icon: ShieldCheck,
      },
      {
        // 本站定制：号池（贡献上游密钥 + 我的号池订阅 + 管理员运营面板）
        id: 'personal.keyPool',
        titleKey: 'Key Pool',
        url: '/contribute-key',
        icon: KeyRound,
      },
    ],
  },
  {
    id: 'admin',
    titleKey: 'Admin',
    items: [
      {
        id: 'admin.channels',
        titleKey: 'Channels',
        url: '/channels',
        icon: Radio,
        requiredRole: ROLE.ADMIN,
      },
      {
        id: 'admin.models',
        titleKey: 'Models',
        url: '/models/metadata',
        icon: Box,
        requiredRole: ROLE.ADMIN,
      },
      {
        id: 'admin.users',
        titleKey: 'Users',
        url: '/users',
        icon: Users,
        requiredRole: ROLE.ADMIN,
      },
      {
        id: 'admin.redemptionCodes',
        titleKey: 'Redemption Codes',
        url: '/redemption-codes',
        icon: Ticket,
        requiredRole: ROLE.ADMIN,
      },
      {
        id: 'admin.subscriptions',
        titleKey: 'Subscriptions',
        url: '/subscriptions',
        icon: CreditCard,
        requiredRole: ROLE.ADMIN,
      },
      {
        id: 'admin.systemInfo',
        titleKey: 'System Info',
        url: '/system-info',
        icon: ServerCog,
        requiredRole: ROLE.SUPER_ADMIN,
      },
      {
        id: 'admin.taskPlugins',
        titleKey: 'Task Plugins',
        url: '/task-plugins',
        icon: PlugZap,
        requiredRole: ROLE.SUPER_ADMIN,
      },
      {
        id: 'admin.requestProcessing',
        titleKey: 'Request processing',
        url: '/request-processing',
        icon: Workflow,
        requiredRole: ROLE.SUPER_ADMIN,
      },
      {
        id: 'admin.extendedSettings',
        titleKey: 'Extended Settings',
        url: '/enhanced-settings',
        icon: SlidersHorizontal,
        requiredRole: ROLE.ADMIN,
      },
      {
        id: 'admin.systemSettings',
        titleKey: 'System Settings',
        url: '/system-settings/site',
        activeUrls: ['/system-settings'],
        icon: Settings,
        requiredRole: ROLE.SUPER_ADMIN,
      },
    ],
  },
]

/** 目录中是否包含给定菜单项 id（用于丢弃配置里的历史残留项）。 */
export function hasNavItemId(id: string): boolean {
  return NAV_CATALOG.some((group) => group.items.some((item) => item.id === id))
}
