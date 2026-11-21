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
 * 安全巡检页（/security-overview，hc36 · B93 管理端）。
 *
 * 管理员每天扫一眼要回答的两个问题：
 *  1. 谁的密钥用得不对劲——跨用户风险密钥表（风险等级 + 命中原因 + IP 证据）；
 *  2. 账户侧防护铺开了没有——全站两步验证覆盖率。
 *
 * 窗口选择、本地搜索与 CSV 导出都只作用于当前结果集，不额外请求后端。
 * 风险等级排序与用户侧自查页一致：high > medium > low > 无风险。
 */
import { useQuery } from '@tanstack/react-query'
import {
  Download,
  KeyRound,
  RefreshCw,
  Search,
  ShieldAlert,
  ShieldCheck,
  UserCheck,
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

import {
  SECURITY_OVERVIEW_DAY_OPTIONS,
  getSecurityOverview,
  getTwoFAStats,
  type TokenRiskItem,
} from './api'

function riskBadge(level: string) {
  if (level === 'high') {
    return { textKey: 'High risk', variant: 'destructive' as const }
  }
  if (level === 'medium') {
    return { textKey: 'Medium risk', variant: 'warning' as const }
  }
  if (level === 'low') {
    return { textKey: 'Low risk', variant: 'secondary' as const }
  }
  return { textKey: 'Normal', variant: 'outline' as const }
}

function riskOrder(level: string): number {
  if (level === 'high') return 0
  if (level === 'medium') return 1
  if (level === 'low') return 2
  return 3
}

function reasonText(reason: string) {
  if (reason === 'ip_outside_allowlist') return 'IP outside allowlist'
  if (reason === 'multi_ip') return 'Used from many IPs'
  return reason
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

/** 风险密钥表 → CSV。导出当前过滤结果。 */
function buildSecurityCsv(items: TokenRiskItem[]): string {
  const header = [
    'token_id',
    'token_name',
    'user_id',
    'username',
    'risk_level',
    'reasons',
    'distinct_ips',
    'request_count',
    'last_used_at',
  ]
  const lines = [header.map(csvCell).join(',')]
  for (const item of items) {
    const cells = [
      String(item.token_id),
      item.token_name,
      String(item.user_id),
      item.username,
      item.risk_level,
      item.reasons.join('|'),
      String(item.distinct_ips),
      String(item.request_count),
      String(item.last_used_at),
    ]
    lines.push(cells.map(csvCell).join(','))
  }
  return `${lines.join('\r\n')}\r\n`
}

/** 触发浏览器下载：BOM + UTF-8 CSV，文件名带日期。 */
function downloadSecurityCsv(content: string) {
  const blob = new Blob(['\uFEFF', content], {
    type: 'text/csv;charset=utf-8',
  })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  const stamp = new Date().toISOString().slice(0, 10)
  anchor.href = url
  anchor.download = `security-overview-${stamp}.csv`
  anchor.click()
  URL.revokeObjectURL(url)
}

function StatTile(props: {
  icon: ReactNode
  label: string
  value: string
  hint?: string
  tone?: 'default' | 'danger' | 'ok'
}) {
  const tone = props.tone ?? 'default'
  let toneClass = 'text-primary'
  if (tone === 'danger') {
    toneClass = 'text-destructive'
  } else if (tone === 'ok') {
    toneClass = 'text-emerald-600 dark:text-emerald-400'
  }
  return (
    <div className='bg-card flex items-start gap-3 rounded-xl border p-4'>
      <div className={`mt-0.5 ${toneClass}`}>{props.icon}</div>
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

function RiskRow(props: { item: TokenRiskItem }) {
  const { t } = useTranslation()
  const item = props.item
  const badge = riskBadge(item.risk_level)
  return (
    <TableRow>
      <TableCell>
        <div className='flex items-center gap-2'>
          <KeyRound className='text-muted-foreground h-4 w-4 shrink-0' />
          <span className='font-medium'>{item.token_name}</span>
        </div>
        <p className='text-muted-foreground mt-0.5 text-xs tabular-nums'>
          #{item.token_id}
        </p>
      </TableCell>
      <TableCell>
        <span className='text-sm'>{item.username || `#${item.user_id}`}</span>
      </TableCell>
      <TableCell>
        <div className='flex flex-wrap items-center gap-1.5'>
          <Badge variant={badge.variant}>{t(badge.textKey)}</Badge>
          {item.reasons.map((reason) => (
            <Badge key={reason} variant='outline'>
              {t(reasonText(reason))}
            </Badge>
          ))}
        </div>
      </TableCell>
      <TableCell className='text-right text-xs tabular-nums'>
        {item.distinct_ips}
      </TableCell>
      <TableCell className='text-right text-xs tabular-nums'>
        {item.request_count}
      </TableCell>
      <TableCell className='text-muted-foreground text-right text-xs tabular-nums'>
        {item.last_used_at > 0
          ? new Date(item.last_used_at * 1000).toLocaleString()
          : '-'}
      </TableCell>
    </TableRow>
  )
}

export function SecurityOverviewPage() {
  const { t } = useTranslation()
  // 默认 30 天窗口（与后端缺省一致）。
  const [days, setDays] = useState(30)
  const [keyword, setKeyword] = useState('')

  const riskQuery = useQuery({
    queryKey: ['security-overview', days],
    staleTime: 60 * 1000,
    queryFn: () => getSecurityOverview(days),
  })
  const twofaQuery = useQuery({
    queryKey: ['security-overview-2fa'],
    staleTime: 5 * 60 * 1000,
    queryFn: () => getTwoFAStats(),
  })

  const report = riskQuery.data
  const twofa = twofaQuery.data

  // high > medium > low > 无风险；同档内按请求数降序，风险大的排前面。
  const riskyItems = useMemo(() => {
    if (!report) return []
    return [...report.items]
      .filter((item) => item.risk_level !== '')
      .sort((a, b) => {
        const byLevel = riskOrder(a.risk_level) - riskOrder(b.risk_level)
        if (byLevel !== 0) return byLevel
        return b.request_count - a.request_count
      })
  }, [report])

  const normalizedKeyword = keyword.trim().toLowerCase()
  const filteredItems = useMemo(() => {
    if (!normalizedKeyword) {
      return riskyItems
    }
    return riskyItems.filter((item) => {
      const haystack = [item.token_name, item.username, String(item.token_id)]
        .join(' ')
        .toLowerCase()
      return haystack.includes(normalizedKeyword)
    })
  }, [riskyItems, normalizedKeyword])

  const body = useMemo<ReactNode>(() => {
    if (riskQuery.isPending) {
      return (
        <div className='space-y-4'>
          <Skeleton className='h-24 w-full' />
          <Skeleton className='h-64 w-full' />
        </div>
      )
    }
    if (riskQuery.isError) {
      return (
        <p className='text-destructive py-8 text-center text-sm'>
          {(riskQuery.error as Error).message}
        </p>
      )
    }
    if (!report) {
      return (
        <p className='text-muted-foreground py-8 text-center text-sm'>
          {t('No data')}
        </p>
      )
    }
    return (
      <div className='space-y-6'>
        <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
          <StatTile
            icon={<KeyRound className='h-4 w-4' />}
            label={t('Keys in use')}
            value={String(report.total_tokens)}
          />
          <StatTile
            icon={<ShieldAlert className='h-4 w-4' />}
            label={t('Risky keys')}
            value={String(report.risky_tokens)}
            tone={report.risky_tokens > 0 ? 'danger' : 'ok'}
            hint={
              report.risky_tokens > 0
                ? t('Rotate flagged keys below')
                : t('All keys look normal in this window.')
            }
          />
          {twofa ? (
            <>
              <StatTile
                icon={<UserCheck className='h-4 w-4' />}
                label={t('Users')}
                value={String(twofa.total_users)}
              />
              <StatTile
                icon={<ShieldCheck className='h-4 w-4' />}
                label={t('Two-factor coverage')}
                value={twofa.enabled_rate}
                hint={t('{{enabled}} of {{total}} users', {
                  enabled: twofa.enabled_users,
                  total: twofa.total_users,
                })}
              />
            </>
          ) : (
            <StatTile
              icon={<UserCheck className='h-4 w-4' />}
              label={t('Users')}
              value='-'
            />
          )}
        </div>

        <Card>
          <CardHeader>
            <CardTitle className='text-base'>{t('Flagged keys')}</CardTitle>
            <CardDescription>
              {t(
                'Keys flagged by risk rules across all users; rotate the high-risk ones first.'
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {filteredItems.length === 0 ? (
              <p className='text-muted-foreground py-8 text-center text-sm'>
                {normalizedKeyword
                  ? t('No keys match your search')
                  : t('No risky keys in this window.')}
              </p>
            ) : (
              <div className='overflow-x-auto'>
                <Table aria-label={t('Flagged keys')}>
                  <TableHeader>
                    <TableRow>
                      <TableHead className='min-w-40'>{t('Key')}</TableHead>
                      <TableHead>{t('User')}</TableHead>
                      <TableHead>{t('Risk level')}</TableHead>
                      <TableHead className='text-right'>
                        {t('Distinct IPs')}
                      </TableHead>
                      <TableHead className='text-right'>
                        {t('Requests')}
                      </TableHead>
                      <TableHead className='text-right'>
                        {t('Last used')}
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {filteredItems.map((item) => (
                      <RiskRow key={item.token_id} item={item} />
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
    filteredItems,
    normalizedKeyword,
    report,
    riskQuery.error,
    riskQuery.isError,
    riskQuery.isPending,
    t,
    twofa,
  ])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Security Overview')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Select value={String(days)} onValueChange={(v) => setDays(Number(v))}>
          <SelectTrigger className='w-40' aria-label={t('Window')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {SECURITY_OVERVIEW_DAY_OPTIONS.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {t(option.labelKey)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <div className='relative'>
          <Search className='text-muted-foreground absolute top-1/2 left-2 h-4 w-4 -translate-y-1/2' />
          <Input
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            placeholder={t('Search keys')}
            aria-label={t('Search keys')}
            className='w-44 pl-8'
          />
        </div>
        <Button
          variant='outline'
          size='sm'
          onClick={() => downloadSecurityCsv(buildSecurityCsv(filteredItems))}
          disabled={filteredItems.length === 0}
        >
          <Download className='mr-1 h-4 w-4' />
          {t('Export CSV')}
        </Button>
        <Button
          variant='outline'
          size='sm'
          onClick={() => riskQuery.refetch()}
          disabled={riskQuery.isFetching}
        >
          <RefreshCw
            className={`mr-1 h-4 w-4 ${riskQuery.isFetching ? 'animate-spin' : ''}`}
          />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>{body}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
