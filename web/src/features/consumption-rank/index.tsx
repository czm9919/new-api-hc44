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
 * 用户消耗排行榜页（/consumption-rank，hc34）。
 *
 * 只回答三个问题，不做成又一个用量报表：
 *  1. 这段时间全站花了多少、多少人参与（顶部四个数字）；
 *  2. 钱花在谁身上（额度倒序，带占比进度条与前 3 名徽标）；
 *  3. 谁快没钱了、谁已经走了（余额 / 累计消耗 / 已删除标记）——运营据此
 *     决定要不要主动联系大客户续费。
 *
 * 数据来源 GET /api/user/consumption_rank（管理员专属），只读无副作用。
 * 搜索与 CSV 导出都只作用于「当前结果集」，不会额外请求后端。
 */
import { useQuery } from '@tanstack/react-query'
import {
  Download,
  RefreshCw,
  Search,
  Trophy,
  Users,
  Wallet,
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toIntlLocale } from '@/i18n/languages'
import {
  formatNumber,
  formatPercent,
  formatQuota,
  formatTimestampToDate,
} from '@/lib/format'

import {
  CONSUMPTION_RANK_LIMIT_OPTIONS,
  CONSUMPTION_RANK_WINDOW_OPTIONS,
  getConsumptionRank,
  type ConsumptionRank,
  type ConsumptionRankEntry,
} from './api'

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

/** 榜单 → CSV。导出当前过滤结果，额度同时给「展示值」与「原始 quota」。 */
function buildRankCsv(entries: ConsumptionRankEntry[]): string {
  const header = [
    'rank',
    'user_id',
    'username',
    'display_name',
    'used_quota',
    'used_quota_display',
    'quota_share',
    'request_count',
    'last_active',
    'remaining_quota',
    'lifetime_quota',
    'status',
    'deleted',
  ]
  const lines = [header.map(csvCell).join(',')]
  for (const entry of entries) {
    const row = [
      String(entry.rank),
      String(entry.user_id),
      entry.username,
      entry.display_name,
      String(entry.used_quota),
      formatQuota(entry.used_quota),
      entry.quota_share.toFixed(6),
      String(entry.request_count),
      entry.last_active ? formatTimestampToDate(entry.last_active) : '',
      String(entry.remaining_quota),
      String(entry.lifetime_quota),
      String(entry.status),
      String(entry.deleted),
    ]
    lines.push(row.map(csvCell).join(','))
  }
  return `${lines.join('\r\n')}\r\n`
}

/** 触发浏览器下载：BOM + UTF-8 CSV，文件名带日期。 */
function downloadRankCsv(content: string) {
  const blob = new Blob(['\uFEFF', content], {
    type: 'text/csv;charset=utf-8',
  })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  const stamp = new Date().toISOString().slice(0, 10)
  anchor.href = url
  anchor.download = `consumption-rank-${stamp}.csv`
  anchor.click()
  URL.revokeObjectURL(url)
}

/** 数字格式化统一走 Intl，语言切换后立即生效。 */
function useLocale() {
  const { i18n } = useTranslation()
  return toIntlLocale(i18n.resolvedLanguage || i18n.language)
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

function SummaryTiles(props: { data: ConsumptionRank }) {
  const { t } = useTranslation()
  const locale = useLocale()
  const { data } = props
  const windowHint =
    data.window.days > 0
      ? t('Last {{days}} days', { days: data.window.days })
      : t('All time')
  return (
    <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
      <StatTile
        icon={<Wallet className='h-4 w-4' />}
        label={t('Total spent')}
        value={formatQuota(data.totals.used_quota)}
        hint={windowHint}
      />
      <StatTile
        icon={<RefreshCw className='h-4 w-4' />}
        label={t('Requests')}
        value={formatNumber(data.totals.request_count, locale)}
      />
      <StatTile
        icon={<Users className='h-4 w-4' />}
        label={t('Users')}
        value={formatNumber(data.totals.user_count, locale)}
        hint={t('Users with consumption in this window')}
      />
      <StatTile
        icon={<Trophy className='h-4 w-4' />}
        label={t('Board size')}
        value={formatNumber(data.leaderboard.length, locale)}
        hint={t('Top {{count}} users shown', {
          count: data.leaderboard.length,
        })}
      />
    </div>
  )
}

const MEDAL_CLASSES: Record<number, string> = {
  1: 'bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/40',
  2: 'bg-slate-400/15 text-slate-600 dark:text-slate-300 border-slate-400/40',
  3: 'bg-orange-600/15 text-orange-700 dark:text-orange-400 border-orange-600/40',
}

function RankBadge(props: { rank: number }) {
  const medal = MEDAL_CLASSES[props.rank]
  if (!medal) {
    return (
      <span className='text-muted-foreground inline-block w-6 text-right text-xs tabular-nums'>
        {props.rank}
      </span>
    )
  }
  return (
    <span
      className={`inline-flex h-6 w-6 items-center justify-center rounded-full border text-xs font-semibold ${medal}`}
    >
      {props.rank}
    </span>
  )
}

function userLabel(entry: ConsumptionRankEntry) {
  if (entry.display_name) {
    return entry.display_name
  }
  return entry.username || `#${entry.user_id}`
}

function RankTable(props: { entries: ConsumptionRankEntry[] }) {
  const { t } = useTranslation()
  const locale = useLocale()

  if (props.entries.length === 0) {
    return (
      <p className='text-muted-foreground py-8 text-center text-sm'>
        {t('No consumption in this window')}
      </p>
    )
  }

  return (
    <div className='overflow-x-auto'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className='w-14 text-right'>{t('Rank')}</TableHead>
            <TableHead className='min-w-48'>{t('User')}</TableHead>
            <TableHead className='text-right'>{t('Spent')}</TableHead>
            <TableHead className='min-w-40'>{t('Share')}</TableHead>
            <TableHead className='text-right'>{t('Requests')}</TableHead>
            <TableHead className='text-right'>{t('Last active')}</TableHead>
            <TableHead className='text-right'>{t('Remaining quota')}</TableHead>
            <TableHead className='text-right'>{t('Lifetime spent')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {props.entries.map((entry) => (
            <TableRow key={entry.user_id}>
              <TableCell className='text-right'>
                <RankBadge rank={entry.rank} />
              </TableCell>
              <TableCell>
                <div className='flex flex-wrap items-center gap-2'>
                  <span className='font-medium'>{userLabel(entry)}</span>
                  {entry.display_name && entry.username ? (
                    <span className='text-muted-foreground text-xs'>
                      @{entry.username}
                    </span>
                  ) : null}
                  {entry.deleted ? (
                    <Badge variant='outline'>{t('Deleted user')}</Badge>
                  ) : null}
                  {!entry.deleted && entry.status !== 1 ? (
                    <Badge variant='secondary'>{t('disabled')}</Badge>
                  ) : null}
                </div>
                <p className='text-muted-foreground mt-0.5 text-xs tabular-nums'>
                  #{entry.user_id}
                </p>
              </TableCell>
              <TableCell className='text-right font-medium tabular-nums'>
                {formatQuota(entry.used_quota)}
              </TableCell>
              <TableCell>
                <div className='flex items-center gap-2'>
                  <Progress
                    value={Math.min(
                      100,
                      Math.round(entry.quota_share * 100 * 10) / 10
                    )}
                    className='h-2 w-24'
                  />
                  <span className='text-muted-foreground text-xs tabular-nums'>
                    {formatPercent(entry.quota_share * 100)}
                  </span>
                </div>
              </TableCell>
              <TableCell className='text-right tabular-nums'>
                {formatNumber(entry.request_count, locale)}
              </TableCell>
              <TableCell className='text-muted-foreground text-right text-xs tabular-nums'>
                {entry.last_active
                  ? formatTimestampToDate(entry.last_active)
                  : '-'}
              </TableCell>
              <TableCell
                className={`text-right tabular-nums ${
                  !entry.deleted && entry.remaining_quota <= 0
                    ? 'text-destructive'
                    : ''
                }`}
              >
                {entry.deleted ? '-' : formatQuota(entry.remaining_quota)}
              </TableCell>
              <TableCell className='text-right text-xs tabular-nums'>
                {entry.deleted ? '-' : formatQuota(entry.lifetime_quota)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

export function ConsumptionRankPage() {
  const { t } = useTranslation()
  // 窗口默认 30 天（与后端缺省一致），条数默认 20。
  const [days, setDays] = useState(30)
  const [limit, setLimit] = useState(20)
  const [keyword, setKeyword] = useState('')

  const query = useQuery({
    queryKey: ['consumption-rank', days, limit],
    staleTime: 60 * 1000,
    queryFn: () => getConsumptionRank(days, limit),
  })

  const data = query.data

  const normalizedKeyword = keyword.trim().toLowerCase()
  const filteredEntries = useMemo(() => {
    if (!data) {
      return []
    }
    if (!normalizedKeyword) {
      return data.leaderboard
    }
    return data.leaderboard.filter((entry) => {
      const haystack = [
        entry.username,
        entry.display_name,
        String(entry.user_id),
      ]
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
    return (
      <div className='space-y-6'>
        <SummaryTiles data={data} />

        <Card>
          <CardHeader>
            <CardTitle className='text-base'>
              {t('Top users by spend')}
            </CardTitle>
            <CardDescription>
              {t(
                'Sorted by consumption in the selected window. Share is of the platform total.'
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <RankTable entries={filteredEntries} />
            {data.leaderboard.length > 0 && filteredEntries.length === 0 ? (
              <p className='text-muted-foreground py-4 text-center text-sm'>
                {t('No users match your search')}
              </p>
            ) : null}
          </CardContent>
        </Card>
      </div>
    )
  }, [data, filteredEntries, query.error, query.isError, query.isPending, t])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Consumption Rank')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='relative'>
          <Search className='text-muted-foreground absolute top-1/2 left-2 h-4 w-4 -translate-y-1/2' />
          <Input
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            placeholder={t('Search users')}
            aria-label={t('Search users')}
            className='w-44 pl-8'
          />
        </div>
        <Select
          value={String(limit)}
          onValueChange={(v) => setLimit(Number(v))}
        >
          <SelectTrigger className='w-28' aria-label={t('Board size')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {CONSUMPTION_RANK_LIMIT_OPTIONS.map((option) => (
              <SelectItem key={option} value={option}>
                {t('Top {{count}}', { count: option })}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={String(days)} onValueChange={(v) => setDays(Number(v))}>
          <SelectTrigger className='w-40' aria-label={t('Window')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {CONSUMPTION_RANK_WINDOW_OPTIONS.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {t(option.labelKey)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          variant='outline'
          size='sm'
          onClick={() => downloadRankCsv(buildRankCsv(filteredEntries))}
          disabled={filteredEntries.length === 0}
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
