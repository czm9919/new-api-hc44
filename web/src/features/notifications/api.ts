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
 * 站内通知中心（hc31 · B211）前端类型与接口。
 *
 * 收件箱由 NotifyUser 漏斗同步落库：外部渠道没配置 / 发送失败时，
 * 通知也会出现在这里，所以页面上看到的一定是完整事件流。
 * 已读操作都带 user_id 条件，前端直接乐观更新列表即可。
 */
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

/** 一条站内通知。 */
export interface NotificationItem {
  id: number
  /** 事件类型码，例如 quota_exceed / channel_update / channel_test。 */
  type: string
  title: string
  content: string
  is_read: boolean
  created_at: number
  read_at: number
}

/** 收件箱分页结果（未读在前、时间倒序）。 */
export interface NotificationPage {
  items: NotificationItem[]
  total: number
  page: number
  page_size: number
  unread_count: number
}

/** 读取收件箱分页。 */
export async function getNotifications(params: {
  page?: number
  page_size?: number
}): Promise<NotificationPage> {
  const res = await api.get('/api/user/notifications', { params })
  return requireServerSuccess(res.data)?.data
}

/** 读取未读通知数（顶栏角标轮询用）。 */
export async function getNotificationUnreadCount(): Promise<number> {
  const res = await api.get('/api/user/notifications/unread-count')
  return requireServerSuccess(res.data)?.data?.unread_count ?? 0
}

/** 标记单条通知已读。返回 false 表示该条不存在或已读。 */
export async function markNotificationRead(id: number): Promise<boolean> {
  const res = await api.post(`/api/user/notifications/${id}/read`)
  return requireServerSuccess(res.data).success === true
}

/** 全部标记已读，返回受影响条数。 */
export async function markAllNotificationsRead(): Promise<number> {
  const res = await api.post('/api/user/notifications/read-all')
  return requireServerSuccess(res.data)?.data?.marked ?? 0
}
