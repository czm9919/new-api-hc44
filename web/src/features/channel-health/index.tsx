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
 * 渠道健康页（/channel-health，hc35 · B13–B15）。
 *
 * 只回答运营最关心的三件事：
 *  1. 谁在持续失败——渠道表按成功率升序（后端已排好），失败置顶；
 *  2. 谁在冷却 / 谁没被调用过——冷却徽标与「暂无样本」区分两种盲区；
 *  3. auto 智能选型现在信谁——模型级健康表 (group|model)。
 *
 * 数据来自进程内健康记忆，刷新只重拉一次；搜索与 CSV 导出都只作用于
 * 「当前结果集」，不额外请求后端。
 */
import { useQuery } from '@tanstack/react-query'
import {
  Activity,
  Download,
  HeartPulse,
  PauseCircle,
  RefreshCw,
  Search,
  Snowflake,
} from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import {
  CHANNEL_HEALTH_STATUS,
  getChannelHealthOverview,
  type ChannelHealthRow,
  type ModelHealthRow,
} from './api'

function rateTone(rate: number) {
  if (rate < 0.5) {
    return 'text-destructive'
  }
  if (rate < 0.9) {
    return 'text-amber-600 dark:text-amber-400'
  }
  return 'text-emerald-600 dark:text-emerald-400'
}

function msText(value: number) {
  return value > 0 ? `${Math.round(value)} ms` : '-'
}

/**
 * CSV 单元格：统一加引号，并按仓库既有约定防公式注入——
 * 以 = + - @ 制表符 回车 开头且不是纯数字的内容前置单引号。
 */
function csvCell(value: string) {
  const text =
    /^[=+\-@\t\r]/.test(value) && !/^-?\d+(\.\d+)?$/.test(value)
      ? `'${value}`
      : value
  return `"${text.replaceAll('"', '""')}"`
}

/** 渠道健康表 → CSV。导出当前过滤结果（hc35 · B15）。 */
function buildHealthCsv(rows: ChannelHealthRow[]): string {
  const header = [
    'channel_id',
    'name',
    'type',
    'status',
    'group',
    'tag',
    'priority',
    'weight',
    'has_samples',
    'success_rate',
    'samples',
    'ttft_ms',
    'latency_ms',
    'cooling_down',
    'active_count',
  ]
  const lines = [header.map(csvCell).join(',')]
  for (const row of rows) {
    const cells = [
      String(row.channel_id),
      row.name,
      String(row.type),
      String(row.status),
      row.group,
      row.tag,
      String(row.priority),
      String(row.weight),
      String(row.has_samples),
      row.has_samples ? row.success_rate.toFixed(6) : '',
      row.has_samples ? String(Math.round(row.samples * 1000) / 1000) : '',
      msText(row.ttft_ms),
      msText(row.latency_ms),
      String(row.cooling_down),
      String(row.active_count),
    ]
    lines.push(cells.map(csvCell).join(','))
  }
  return `${lines.join('\r\n')}\r\n`
}

/** 触发浏览器下载：BOM + UTF-8 CSV，文件名带日期。 */
function downloadHealthCsv(content: string) {
  const blob = new Blob(['\uFEFF', content], {
    type: 'text/csv;charset=utf-8',
  })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  const stamp = new Date().toISOString().slice(0, 10)
  anchor.href = url
  anchor.download = `channel-health-${stamp}.csv`
  anchor.click()
  URL.revokeObjectURL(url)
}

function StatTile(props: {
  icon: ReactNode
  label: string
  value: string
  hint?: string
}) {
  return (
    <div className='bg-card flex items-start gap-3 rounded-xl border p-4'>
      <div className='text-primary mt-0.5'>{props.icon}</div>
      <div className='min-w-0'>
        <p className='text-muted-foreground text-xs'>{props.label}</p>
        <p className='mt-1 truncate text-lg font-semibold'>{props.value}</p>
        {props.hint ? (
          <p className='text-muted-foreground mt-0.5 truncate text-xs'>
            {props.hint}
          </p>
        ) : null}
      </div>
    </div>
  )
}

function StatusBadge(props: { status: number }) {
  const { t } = useTranslation()
  if (props.status === CHANNEL_HEALTH_STATUS.ENABLED) {
    return <Badge variant='secondary'>{t('Enabled')}</Badge>
  }
  if (props.status === CHANNEL_HEALTH_STATUS.AUTO_DISABLED) {
    return <Badge variant='destructive'>{t('Auto disabled')}</Badge>
  }
  return <Badge variant='outline'>{t('Disabled')}</Badge>
}

function HealthRow(props: { row: ChannelHealthRow }) {
  const { t } = useTranslation()
  const row = props.row
  return (
    <TableRow>
      <TableCell>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='font-medium'>
            {row.name || `#${row.channel_id}`}
          </span>
          {row.tag ? <Badge variant='outline'>{row.tag}</Badge> : null}
          {row.cooling_down ? (
            <Badge variant='destructive'>{t('Cooling down')}</Badge>
          ) : null}
        </div>
        <p className='text-muted-foreground mt-0.5 text-xs tabular-nums'>
          #{row.channel_id} · {row.group}
        </p>
      </TableCell>
      <TableCell>
        <StatusBadge status={row.status} />
      </TableCell>
      <TableCell>
        {row.has_samples ? (
          <div className='flex items-center gap-2'>
            <Progress
              value={Math.min(100, Math.round(row.success_rate * 100))}
              className='h-2 w-20'
            />
            <span
              className={`text-xs font-medium tabular-nums ${rateTone(row.success_rate)}`}
            >
              {Math.round(row.success_rate * 100)}%
            </span>
          </div>
        ) : (
          <span className='text-muted-foreground text-xs'>
            {t('No samples yet')}
          </span>
        )}
      </TableCell>
      <TableCell className='text-right text-xs tabular-nums'>
        {row.has_samples ? Math.round(row.samples * 100) / 100 : '-'}
      </TableCell>
      <TableCell className='text-right text-xs tabular-nums'>
        {msText(row.ttft_ms)}
      </TableCell>
      <TableCell className='text-right text-xs tabular-nums'>
        {msText(row.latency_ms)}
      </TableCell>
      <TableCell className='text-right text-xs tabular-nums'>
        {row.active_count}
      </TableCell>
    </TableRow>
  )
}

export function ChannelHealthPage() {
  const { t } = useTranslation()
  const [keyword, setKeyword] = useState('')

  const query = useQuery({
    queryKey: ['channel-health'],
    staleTime: 30 * 1000,
    queryFn: () => getChannelHealthOverview(),
  })

  const data = query.data

  const normalizedKeyword = keyword.trim().toLowerCase()
  const filteredRows = useMemo(() => {
    if (!data) {
      return []
    }
    if (!normalizedKeyword) {
      return data.channels
    }
    return data.channels.filter((row) => {
      const haystack = [row.name, row.group, row.tag, String(row.channel_id)]
        .join(' ')
        .toLowerCase()
      return haystack.includes(normalizedKeyword)
    })
  }, [data, normalizedKeyword])

  const body = useMemo<ReactNode>(() => {
    if (query.isPending) {
      return (
        <div className='space-y-4'>
          <Skeleton className='h-24 w-full' />
          <Skeleton className='h-64 w-full' />
        </div>
      )
    }
    if (query.isError) {
      return (
        <p className='text-destructive py-8 text-center text-sm'>
          {(query.error as Error).message}
        </p>
      )
    }
    if (!data) {
      return (
        <p className='text-muted-foreground py-8 text-center text-sm'>
          {t('No data')}
        </p>
      )
    }
    const settings = data.settings
    return (
      <div className='space-y-6'>
        <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
          <StatTile
            icon={<Activity className='h-4 w-4' />}
            label={t('Channels')}
            value={String(data.summary.total)}
            hint={t('Decay half-life: {{minutes}} min', {
              minutes: settings.half_life_minutes,
            })}
          />
          <StatTile
            icon={<HeartPulse className='h-4 w-4' />}
            label={t('With samples')}
            value={String(data.summary.with_samples)}
            hint={t('Confidence threshold: {{count}} samples', {
              count: settings.min_samples,
            })}
          />
          <StatTile
            icon={<Snowflake className='h-4 w-4' />}
            label={t('Cooling down')}
            value={String(data.summary.cooling_down)}
            hint={t('Fail cooldown: {{seconds}}s', {
              seconds: settings.fail_cooldown_seconds,
            })}
          />
          <StatTile
            icon={<PauseCircle className='h-4 w-4' />}
            label={t('Degraded')}
            value={String(data.summary.degraded)}
            hint={t('Enough samples but success rate below 50%')}
          />
        </div>

        <Card>
          <CardHeader>
            <CardTitle className='text-base'>
              {t('Channel reliability')}
            </CardTitle>
            <CardDescription>
              {t(
                'Worst channels first; channels without samples are listed last.'
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {filteredRows.length === 0 ? (
              <p className='text-muted-foreground py-8 text-center text-sm'>
                {normalizedKeyword
                  ? t('No channels match your search')
                  : t('No samples yet')}
              </p>
            ) : (
              <div className='overflow-x-auto'>
                <Table aria-label={t('Channel reliability')}>
                  <TableHeader>
                    <TableRow>
                      <TableHead className='min-w-48'>{t('Channel')}</TableHead>
                      <TableHead>{t('Status')}</TableHead>
                      <TableHead>{t('Success rate')}</TableHead>
                      <TableHead className='text-right'>
                        {t('Samples count')}
                      </TableHead>
                      <TableHead className='text-right'>
                        {t('TTFT (avg)')}
                      </TableHead>
                      <TableHead className='text-right'>
                        {t('Latency (avg)')}
                      </TableHead>
                      <TableHead className='text-right'>
                        {t('Active requests')}
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {filteredRows.map((row) => (
                      <HealthRow key={row.channel_id} row={row} />
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className='text-base'>
              {t('Model-level health')}
            </CardTitle>
            <CardDescription>
              {t('Signals used by auto smart model selection')}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {data.models.length === 0 ? (
              <p className='text-muted-foreground py-8 text-center text-sm'>
                {t('No model samples yet')}
              </p>
            ) : (
              <div className='overflow-x-auto'>
                <Table aria-label={t('Model-level health')}>
                  <TableHeader>
                    <TableRow>
                      <TableHead className='min-w-40'>{t('Group')}</TableHead>
                      <TableHead>{t('Model')}</TableHead>
                      <TableHead>{t('Success rate')}</TableHead>
                      <TableHead className='text-right'>
                        {t('Samples count')}
                      </TableHead>
                      <TableHead className='text-right'>
                        {t('TTFT (avg)')}
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {data.models.map((row: ModelHealthRow) => (
                      <TableRow key={`${row.group}|${row.model}`}>
                        <TableCell className='text-xs'>{row.group}</TableCell>
                        <TableCell className='text-xs font-medium'>
                          {row.model}
                        </TableCell>
                        <TableCell>
                          <span
                            className={`text-xs font-medium tabular-nums ${rateTone(row.success_rate)}`}
                          >
                            {Math.round(row.success_rate * 100)}%
                          </span>
                        </TableCell>
                        <TableCell className='text-right text-xs tabular-nums'>
                          {Math.round(row.samples * 100) / 100}
                        </TableCell>
                        <TableCell className='text-right text-xs tabular-nums'>
                          {msText(row.ttft_ms)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    )
  }, [
    data,
    filteredRows,
    normalizedKeyword,
    query.error,
    query.isError,
    query.isPending,
    t,
  ])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Channel Health')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='relative'>
          <Search className='text-muted-foreground absolute top-1/2 left-2 h-4 w-4 -translate-y-1/2' />
          <Input
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            placeholder={t('Search channels')}
            aria-label={t('Search channels')}
            className='w-44 pl-8'
          />
        </div>
        <Button
          variant='outline'
          size='sm'
          onClick={() => downloadHealthCsv(buildHealthCsv(filteredRows))}
          disabled={filteredRows.length === 0}
        >
          <Download className='mr-1 h-4 w-4' />
          {t('Export CSV')}
        </Button>
        <Button
          variant='outline'
          size='sm'
          onClick={() => query.refetch()}
          disabled={query.isFetching}
        >
          <RefreshCw
            className={`mr-1 h-4 w-4 ${query.isFetching ? 'animate-spin' : ''}`}
          />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>{body}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
