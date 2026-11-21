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
 * 站内通知中心（/notifications，hc31 · B211 的前端落地）。
 *
 * 收件箱是「通知不丢失」的兜底：外部渠道（邮件/webhook/bark/gotify）没配或发送
 * 失败，事件仍会落库并出现在这里。列表由后端保证「未读在前 + 时间倒序」，
 * 因此前端不再二次排序，只在标记已读后重新拉取（未读项会自然沉到后面）。
 */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Bell, CheckCheck, Mail, MailOpen, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { formatTimestampRelative } from '@/lib/format'

import {
  getNotifications,
  getNotificationUnreadCount,
  markAllNotificationsRead,
  markNotificationRead,
  type NotificationItem,
} from './api'

const PAGE_SIZE = 20

/** 事件类型 → 徽章样式；未登记的类型统一走 outline。 */
function typeBadgeVariant(type: string) {
  if (type === 'quota_exceed' || type === 'usage_anomaly') {
    return 'destructive' as const
  }
  if (type === 'channel_update' || type === 'upstream_model_update') {
    return 'warning' as const
  }
  return 'outline' as const
}

function NotificationRow(props: {
  item: NotificationItem
  onRead: (id: number) => void
  reading: boolean
}) {
  const { t } = useTranslation()
  const { item } = props
  return (
    <div
      className={`flex items-start gap-3 rounded-lg border p-3 ${
        item.is_read ? '' : 'bg-muted/40'
      }`}
    >
      <div className='text-muted-foreground mt-0.5'>
        {item.is_read ? (
          <MailOpen className='h-4 w-4' />
        ) : (
          <Mail className='text-primary h-4 w-4' />
        )}
      </div>
      <div className='min-w-0 flex-1 space-y-1'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='truncate text-sm font-medium'>{item.title}</span>
          {item.type ? (
            <Badge variant={typeBadgeVariant(item.type)}>{item.type}</Badge>
          ) : null}
          <span className='text-muted-foreground text-xs'>
            {formatTimestampRelative(item.created_at)}
          </span>
        </div>
        {item.content ? (
          <p className='text-muted-foreground text-sm whitespace-pre-wrap'>
            {item.content}
          </p>
        ) : null}
      </div>
      {item.is_read ? null : (
        <Button
          variant='ghost'
          size='sm'
          className='shrink-0'
          disabled={props.reading}
          onClick={() => props.onRead(item.id)}
        >
          {t('Mark read')}
        </Button>
      )}
    </div>
  )
}

export function NotificationsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)

  const listQuery = useQuery({
    queryKey: ['notifications', page],
    queryFn: () => getNotifications({ page, page_size: PAGE_SIZE }),
  })

  // 未读数单独拉一次：即使列表翻到第 N 页，角标也要是全量未读数。
  const unreadQuery = useQuery({
    queryKey: ['notifications-unread'],
    staleTime: 30 * 1000,
    queryFn: () => getNotificationUnreadCount(),
  })

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['notifications'] })
    queryClient.invalidateQueries({ queryKey: ['notifications-unread'] })
  }

  const readOne = useMutation({
    mutationFn: (id: number) => markNotificationRead(id),
    onSuccess: (ok) => {
      // 后端返回 false = 已读或不存在，重新拉一次让 UI 与服务端对齐。
      if (!ok) toast.info(t('Already read'))
      invalidate()
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const readAll = useMutation({
    mutationFn: () => markAllNotificationsRead(),
    onSuccess: (marked) => {
      toast.success(
        t('Marked {{count}} notification(s) as read', { count: marked })
      )
      invalidate()
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const data = listQuery.data
  const unread = unreadQuery.data ?? data?.unread_count ?? 0
  const totalPages = data
    ? Math.max(1, Math.ceil(data.total / data.page_size))
    : 1

  let body
  if (listQuery.isLoading) {
    body = (
      <div className='space-y-3'>
        <Skeleton className='h-20 w-full' />
        <Skeleton className='h-20 w-full' />
      </div>
    )
  } else if (listQuery.isError) {
    body = (
      <p className='text-destructive py-8 text-center text-sm'>
        {(listQuery.error as Error).message}
      </p>
    )
  } else if (!data || data.items.length === 0) {
    body = (
      <Card>
        <CardContent className='text-muted-foreground flex flex-col items-center gap-2 py-12 text-sm'>
          <Bell className='h-8 w-8 opacity-40' />
          {t('Nothing here yet')}
        </CardContent>
      </Card>
    )
  } else {
    body = (
      <div className='space-y-3'>
        <div className='space-y-2'>
          {data.items.map((item) => (
            <NotificationRow
              key={item.id}
              item={item}
              onRead={(id) => readOne.mutate(id)}
              reading={readOne.isPending}
            />
          ))}
        </div>

        <div className='flex items-center justify-between gap-2 pt-1'>
          <span className='text-muted-foreground text-xs'>
            {t('Page {{page}} of {{total}}', {
              page: data.page,
              total: totalPages,
            })}
          </span>
          <div className='flex gap-2'>
            <Button
              variant='outline'
              size='sm'
              disabled={data.page <= 1}
              onClick={() => setPage((p) => Math.max(1, p - 1))}
            >
              {t('Previous')}
            </Button>
            <Button
              variant='outline'
              size='sm'
              disabled={data.page >= totalPages}
              onClick={() => setPage((p) => p + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        </div>
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <span className='inline-flex min-w-0 items-center gap-2'>
          <span className='truncate'>{t('Notifications')}</span>
          {unread > 0 ? (
            <Badge variant='destructive' className='shrink-0 tabular-nums'>
              {unread}
            </Badge>
          ) : null}
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          size='sm'
          onClick={() => listQuery.refetch()}
          disabled={listQuery.isFetching}
        >
          <RefreshCw
            className={`mr-1 h-4 w-4 ${listQuery.isFetching ? 'animate-spin' : ''}`}
          />
          {t('Refresh')}
        </Button>
        <Button
          size='sm'
          onClick={() => readAll.mutate()}
          disabled={unread === 0 || readAll.isPending}
        >
          <CheckCheck className='mr-1 h-4 w-4' />
          {t('Mark all read')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>{body}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
