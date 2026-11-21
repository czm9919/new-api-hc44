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
 * 管理端「通知事件 × 渠道 × 默认开关」覆盖表（hc31 · B213 的 UI 落地）。
 *
 * 后端 GET /api/option/notify_event_catalog 已经返回完整矩阵，这里只做三件事：
 *  1. 顶部三个数字：事件总数 / 站内覆盖面 / 外部渠道数；
 *  2. 矩阵：一行一个事件，一列一个渠道，格子里标默认是否送达、能否被用户关掉；
 *  3. 每行补充「受众 / 通知类型码 / 受哪个偏好开关控制」，便于对照代码排查漏通知。
 *
 * 纯只读页：不写 option、不改用户偏好，因此不做乐观更新，刷新即重新拉取。
 */
import { useQuery } from '@tanstack/react-query'
import {
  BellRing,
  Download,
  Inbox,
  RefreshCw,
  Search,
  Send,
  TriangleAlert,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { api } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import { SettingsSection } from '../components/settings-section'

/** 单渠道在某事件下的覆盖情况。 */
interface NotifyEventChannelCover {
  channel: string
  /** 默认是否送达；站内收件箱恒为 true。 */
  default_enabled: boolean
  /** 是否可由用户偏好改变。 */
  configurable: boolean
  note?: string
}

/** 单个通知事件的覆盖条目。 */
interface NotifyEventCover {
  event: string
  name: string
  /** 实际写入 dto.Notify.Type 的类型码（限流键也用它）。 */
  notify_type: string
  /** user / admin */
  audience: string
  description: string
  /** 控制该事件的 dto.UserSetting 字段名，空表示不受偏好控制。 */
  preference_key: string
  /** tri_state_bool / bool_default_off / channel_type */
  preference_type: string
  default_enabled: boolean
  channels: NotifyEventChannelCover[]
}

/** 覆盖表整体响应。 */
interface NotifyEventCatalog {
  generated_at: number
  note: string
  /** 全量渠道顺序（即矩阵列序）。 */
  channels: string[]
  events_inbox_covered: number
  events_total: number
  events: NotifyEventCover[]
}

async function getNotifyEventCatalog(): Promise<NotifyEventCatalog> {
  const res = await api.get('/api/option/notify_event_catalog')
  return requireServerSuccess(res.data)?.data
}

/** 渠道展示名：站内信箱用本地化文案，外部渠道沿用后端常量名。 */
function channelLabel(channel: string, t: (key: string) => string) {
  return channel === 'inbox' ? t('Inbox') : channel
}

/** 偏好字段语义 → 可读文案。 */
function preferenceTypeLabel(
  preferenceType: string,
  t: (key: string) => string
) {
  switch (preferenceType) {
    case 'tri_state_bool':
      return t('Tri-state toggle')
    case 'bool_default_off':
      return t('Off by default')
    case 'channel_type':
      return t('Channel picker')
    default:
      return preferenceType || t('None')
  }
}

/** 默认开关状态的通用文案（渠道格子与偏好列共用）。 */
function defaultStateLabel(enabled: boolean, t: (key: string) => string) {
  return enabled ? t('On by default') : t('Off by default')
}

/** 渠道格子里「是否送达」的文案：站内信箱恒开启，外部渠道看默认值。 */
function deliveryLabel(
  cover: NotifyEventChannelCover,
  t: (key: string) => string
) {
  if (cover.channel === 'inbox') {
    return t('Always on')
  }
  return defaultStateLabel(cover.default_enabled, t)
}

/**
 * 站内信箱未覆盖的事件：运营必须知道这些事件站内看不到，
 * 用户只能依赖外部渠道（或干脆收不到）。
 */
function eventsMissingInbox(events: NotifyEventCover[]) {
  return events.filter(
    (event) => !event.channels.some((cover) => cover.channel === 'inbox')
  )
}

/**
 * CSV 单元格转义：引号翻倍 + 防公式注入（约定与 usage-logs 导出一致：
 * 以 =+-@\t 开头且不是纯数字的内容前置单引号）。
 */
function csvCell(value: string) {
  const text =
    /^[=+\-@\t\r]/.test(value) && !/^-?\d+(\.\d+)?$/.test(value)
      ? `'${value}`
      : value
  return `"${text.replaceAll('"', '""')}"`
}

/**
 * 覆盖表 → CSV。导出「当前过滤结果」，渠道列展开成
 * `<channel>_default_enabled` / `<channel>_configurable` 两列，
 * 方便在表格软件里按渠道透视。
 */
function buildCatalogCsv(
  events: NotifyEventCover[],
  channels: string[]
): string {
  const header = [
    'event',
    'name',
    'notify_type',
    'audience',
    'preference_key',
    'preference_type',
    'default_enabled',
    ...channels.flatMap((channel) => [
      `${channel}_default_enabled`,
      `${channel}_configurable`,
    ]),
  ]
  const lines = [header.map(csvCell).join(',')]
  for (const event of events) {
    const row = [
      event.event,
      event.name,
      event.notify_type,
      event.audience,
      event.preference_key,
      event.preference_type,
      String(event.default_enabled),
    ]
    for (const channel of channels) {
      const cover = event.channels.find((item) => item.channel === channel)
      row.push(cover ? String(cover.default_enabled) : '')
      row.push(cover ? String(cover.configurable) : '')
    }
    lines.push(row.map(csvCell).join(','))
  }
  return `${lines.join('\r\n')}\r\n`
}

/** 保存为带 BOM 的 UTF-8 CSV，表格软件可直接打开中文列名。 */
function downloadCatalogCsv(content: string, filename: string) {
  const blob = new Blob(['\uFEFF', content], {
    type: 'text/csv;charset=utf-8',
  })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.click()
  URL.revokeObjectURL(url)
}

export function NotifyCatalogSection() {
  const { t } = useTranslation()
  const [keyword, setKeyword] = useState('')

  const catalogQuery = useQuery({
    queryKey: ['notify-event-catalog'],
    queryFn: getNotifyEventCatalog,
    staleTime: 60 * 1000,
  })

  const catalog = catalogQuery.data
  const externalChannels = catalog
    ? catalog.channels.filter((channel) => channel !== 'inbox')
    : []

  // 关键字过滤（事件名 / 类型码 / 描述 / 偏好键，大小写不敏感）
  const normalizedKeyword = keyword.trim().toLowerCase()
  const filteredEvents = useMemo(() => {
    if (!catalog) return []
    if (!normalizedKeyword) return catalog.events
    return catalog.events.filter((event) => {
      const haystack = [
        event.name,
        event.event,
        event.notify_type,
        event.description,
        event.preference_key,
      ]
        .join('\n')
        .toLowerCase()
      return haystack.includes(normalizedKeyword)
    })
  }, [catalog, normalizedKeyword])

  const missingInbox = useMemo(
    () => (catalog ? eventsMissingInbox(catalog.events) : []),
    [catalog]
  )

  const stats = [
    {
      key: 'events',
      icon: BellRing,
      label: t('Total events'),
      value: catalog ? String(catalog.events_total) : '-',
    },
    {
      key: 'inbox',
      icon: Inbox,
      label: t('Inbox covered'),
      value: catalog ? String(catalog.events_inbox_covered) : '-',
    },
    {
      key: 'channels',
      icon: Send,
      label: t('External channels'),
      value: catalog ? String(externalChannels.length) : '-',
    },
  ]

  return (
    <SettingsSection title={t('Notification Events')}>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Shows which channels each notification event reaches, and which user preference controls it.'
        )}
      </p>

      <div className='grid gap-4 sm:grid-cols-3'>
        {stats.map((item) => (
          <div key={item.key} className='rounded-lg border p-4'>
            <div className='text-muted-foreground flex items-center gap-2 text-sm'>
              <item.icon className='size-4' />
              {item.label}
            </div>
            <div className='mt-1 text-2xl font-semibold'>{item.value}</div>
          </div>
        ))}
      </div>

      {catalogQuery.isPending ? (
        <div className='space-y-2'>
          {[0, 1, 2, 3].map((row) => (
            <Skeleton key={row} className='h-12 w-full' />
          ))}
        </div>
      ) : null}

      {!catalogQuery.isPending && (catalogQuery.isError || !catalog) ? (
        <div className='flex flex-col items-start gap-3 rounded-lg border border-dashed p-4'>
          <p className='text-muted-foreground text-sm'>{t('Failed to load')}</p>
          <Button
            variant='outline'
            size='sm'
            onClick={() => void catalogQuery.refetch()}
          >
            <RefreshCw className='mr-1 size-4' />
            {t('Retry')}
          </Button>
        </div>
      ) : null}

      {!catalogQuery.isPending && !catalogQuery.isError && catalog ? (
        <>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <div className='text-muted-foreground text-xs'>
              {t('Generated at')}: {formatTimestamp(catalog.generated_at)}
            </div>
            <div className='flex items-center gap-2'>
              <div className='relative'>
                <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2' />
                <Input
                  value={keyword}
                  onChange={(event) => setKeyword(event.target.value)}
                  placeholder={t('Search events')}
                  aria-label={t('Search events')}
                  className='h-8 w-56 pl-8 text-xs'
                />
              </div>
              <Button
                variant='outline'
                size='sm'
                onClick={() =>
                  downloadCatalogCsv(
                    buildCatalogCsv(filteredEvents, catalog.channels),
                    `notify-event-catalog-${new Date()
                      .toISOString()
                      .slice(0, 10)}.csv`
                  )
                }
                disabled={filteredEvents.length === 0}
              >
                <Download className='mr-1 size-4' />
                {t('Export CSV')}
              </Button>
              <Button
                variant='outline'
                size='sm'
                onClick={() => void catalogQuery.refetch()}
                disabled={catalogQuery.isFetching}
              >
                <RefreshCw className='mr-1 size-4' />
                {t('Refresh')}
              </Button>
            </div>
          </div>

          {missingInbox.length > 0 ? (
            <div
              className='flex items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-500/5 p-3 text-sm'
              role='status'
            >
              <TriangleAlert className='mt-0.5 size-4 shrink-0 text-amber-500' />
              <div>
                <div className='font-medium'>
                  {t('Events not delivered to the inbox')}:{' '}
                  {missingInbox.length}
                </div>
                <div className='text-muted-foreground mt-1 text-xs'>
                  {t(
                    'These events are invisible in the in-app inbox; users can only learn about them through external channels.'
                  )}
                </div>
                <div className='text-muted-foreground mt-1 font-mono text-[11px]'>
                  {missingInbox.map((event) => event.notify_type).join(', ')}
                </div>
              </div>
            </div>
          ) : null}

          <div className='overflow-x-auto rounded-lg border'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className='min-w-[260px]'>{t('Event')}</TableHead>
                  <TableHead>{t('Audience')}</TableHead>
                  {catalog.channels.map((channel) => (
                    <TableHead key={channel} className='whitespace-nowrap'>
                      {channelLabel(channel, t)}
                    </TableHead>
                  ))}
                  <TableHead className='min-w-[200px]'>
                    {t('Preference key')}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredEvents.map((event) => (
                  <TableRow key={event.event}>
                    <TableCell>
                      <div className='font-medium'>{event.name}</div>
                      <div className='text-muted-foreground mt-0.5 text-xs'>
                        {event.description}
                      </div>
                      <div className='text-muted-foreground mt-1 font-mono text-[11px]'>
                        {event.notify_type}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={
                          event.audience === 'admin' ? 'default' : 'secondary'
                        }
                      >
                        {event.audience === 'admin' ? t('Admin') : t('User')}
                      </Badge>
                    </TableCell>
                    {catalog.channels.map((channel) => {
                      const cover = event.channels.find(
                        (item) => item.channel === channel
                      )
                      if (!cover) {
                        return (
                          <TableCell
                            key={channel}
                            className='text-muted-foreground'
                          >
                            {t('None')}
                          </TableCell>
                        )
                      }
                      return (
                        <TableCell key={channel}>
                          <div className='flex flex-col gap-1'>
                            <Badge
                              variant={
                                cover.default_enabled ? 'default' : 'outline'
                              }
                            >
                              {deliveryLabel(cover, t)}
                            </Badge>
                            <span className='text-muted-foreground text-[11px]'>
                              {cover.configurable
                                ? t('Configurable')
                                : t('Not configurable')}
                            </span>
                          </div>
                        </TableCell>
                      )
                    })}
                    <TableCell>
                      {event.preference_key ? (
                        <div className='flex flex-col gap-1'>
                          <span className='font-mono text-xs'>
                            {event.preference_key}
                          </span>
                          <span className='text-muted-foreground text-[11px]'>
                            {preferenceTypeLabel(event.preference_type, t)}
                            {' · '}
                            {defaultStateLabel(event.default_enabled, t)}
                          </span>
                        </div>
                      ) : (
                        <span className='text-muted-foreground text-xs'>
                          {t('None')}
                        </span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
                {filteredEvents.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={3 + catalog.channels.length}
                      className='text-muted-foreground py-6 text-center text-sm'
                    >
                      {t('No events match your search')}
                    </TableCell>
                  </TableRow>
                ) : null}
              </TableBody>
            </Table>
          </div>

          {catalog.note ? (
            <p className='text-muted-foreground text-xs leading-relaxed'>
              {catalog.note}
            </p>
          ) : null}
        </>
      ) : null}
    </SettingsSection>
  )
}
