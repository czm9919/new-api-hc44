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
 * 密钥风险自查页（/token-risk，hc31 · B93 的前端落地）。
 *
 * 顶部回答「最近有没有密钥用得不对劲」；下面按风险等级（high > medium > low）
 * 逐把展开：命中原因、不同 IP 数、IP 明细（白名单外的 IP 标红）、以及后端给的
 * 处置建议（补白名单 / 缩窗口 / 直接重置）。
 */
import { useQuery } from '@tanstack/react-query'
import { KeyRound, RefreshCw, ShieldAlert, ShieldCheck } from 'lucide-react'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { formatTimestampToDate } from '@/lib/format'

import {
  TOKEN_RISK_DAY_OPTIONS,
  getTokenRisk,
  type TokenRiskItem,
  type TokenRiskReport,
} from './api'

/** 风险等级 → 展示徽章。后端只有 high/medium/low 三档 + 空串（无风险）。 */
function riskBadge(level: string) {
  if (level === 'high')
    return { textKey: 'High risk', variant: 'destructive' as const }
  if (level === 'medium')
    return { textKey: 'Medium risk', variant: 'warning' as const }
  if (level === 'low')
    return { textKey: 'Low risk', variant: 'secondary' as const }
  return { textKey: 'Normal', variant: 'outline' as const }
}

/** 风险等级 → 排序权重。 */
function riskOrder(level: string): number {
  if (level === 'high') return 0
  if (level === 'medium') return 1
  if (level === 'low') return 2
  return 3
}

/** 命中原因 → 展示文案。 */
function reasonText(reason: string) {
  if (reason === 'ip_outside_allowlist') return 'IP outside allowlist'
  if (reason === 'multi_ip') return 'Used from many IPs'
  return reason
}

function Summary(props: { report: TokenRiskReport }) {
  const { t } = useTranslation()
  const { report } = props
  const risky = report.risky_tokens
  const allClear = risky === 0 && report.total_tokens > 0

  return (
    <div className='grid gap-3 sm:grid-cols-3'>
      <div className='bg-card flex items-start gap-3 rounded-xl border p-4'>
        <div className='text-primary mt-0.5'>
          <KeyRound className='h-4 w-4' />
        </div>
        <div className='min-w-0'>
          <p className='text-muted-foreground text-xs'>{t('Keys in use')}</p>
          <p className='mt-1 text-lg font-semibold'>{report.total_tokens}</p>
        </div>
      </div>
      <div className='bg-card flex items-start gap-3 rounded-xl border p-4'>
        <div
          className={`mt-0.5 ${risky > 0 ? 'text-destructive' : 'text-emerald-600'}`}
        >
          {risky > 0 ? (
            <ShieldAlert className='h-4 w-4' />
          ) : (
            <ShieldCheck className='h-4 w-4' />
          )}
        </div>
        <div className='min-w-0'>
          <p className='text-muted-foreground text-xs'>{t('Risky keys')}</p>
          <p className='mt-1 text-lg font-semibold'>{risky}</p>
        </div>
      </div>
      <div className='bg-card flex items-center gap-3 rounded-xl border p-4'>
        {allClear ? (
          <p className='text-sm'>{t('All keys look normal in this window.')}</p>
        ) : (
          <p className='text-muted-foreground text-sm'>
            {t('Check the flagged keys below and rotate them if needed.')}
          </p>
        )}
      </div>
    </div>
  )
}

function RiskItemCard(props: { item: TokenRiskItem }) {
  const { t } = useTranslation()
  const { item } = props
  const badge = riskBadge(item.risk_level)

  return (
    <Card>
      <CardHeader className='pb-3'>
        <CardTitle className='flex flex-wrap items-center gap-2 text-base'>
          <KeyRound className='h-4 w-4' />
          <span className='truncate'>{item.token_name}</span>
          <Badge variant={badge.variant}>{t(badge.textKey)}</Badge>
        </CardTitle>
        <CardDescription className='flex flex-wrap gap-x-4 gap-y-1'>
          <span>
            {t('Requests')} {item.request_count}
          </span>
          <span>
            {t('Distinct IPs')} {item.distinct_ips}
          </span>
          <span>
            {t('Last used')} {formatTimestampToDate(item.last_used_at)}
          </span>
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-3'>
        <div className='flex flex-wrap gap-2'>
          {item.reasons.map((reason) => (
            <Badge key={reason} variant='destructive'>
              {t(reasonText(reason))}
            </Badge>
          ))}
        </div>

        {item.ips.length > 0 ? (
          <div className='divide-y rounded-lg border'>
            {item.ips.map((ipInfo) => (
              <div
                key={ipInfo.ip}
                className='flex items-center justify-between gap-2 px-3 py-2 text-sm'
              >
                <div className='flex min-w-0 items-center gap-2'>
                  <span
                    className={`truncate font-mono ${ipInfo.allowed ? '' : 'text-destructive font-medium'}`}
                  >
                    {ipInfo.ip}
                  </span>
                  {!ipInfo.allowed ? (
                    <Badge variant='destructive'>{t('Not allowed')}</Badge>
                  ) : null}
                </div>
                <span className='text-muted-foreground shrink-0 text-xs tabular-nums'>
                  {ipInfo.request_count} ·{' '}
                  {formatTimestampToDate(ipInfo.last_seen_at)}
                </span>
              </div>
            ))}
          </div>
        ) : null}

        {item.allow_ips.length > 0 ? (
          <p className='text-muted-foreground text-xs'>
            {t('Allowlist')}: {item.allow_ips.join(', ')}
          </p>
        ) : null}

        {item.recommendation ? (
          <div className='bg-muted/40 rounded-lg p-3 text-sm'>
            <p className='text-muted-foreground mb-1 text-xs font-medium'>
              {t('Recommendation')}
            </p>
            <p>{item.recommendation}</p>
          </div>
        ) : null}
      </CardContent>
    </Card>
  )
}

export function TokenRiskPage() {
  const { t } = useTranslation()
  // 默认 30 天窗口（与后端缺省一致）。
  const [days, setDays] = useState(30)

  const query = useQuery({
    queryKey: ['token-risk', days],
    staleTime: 60 * 1000,
    queryFn: () => getTokenRisk(days),
  })

  const report = query.data
  // high > medium > low > 无风险；同档内按请求数降序，风险大的排前面。
  const items = useMemo(() => {
    if (!report) return []
    return [...report.items].sort((a, b) => {
      const byLevel = riskOrder(a.risk_level) - riskOrder(b.risk_level)
      if (byLevel !== 0) return byLevel
      return b.request_count - a.request_count
    })
  }, [report])

  const body = useMemo<ReactNode>(() => {
    if (query.isLoading) {
      return (
        <div className='space-y-4'>
          <Skeleton className='h-20 w-full' />
          <Skeleton className='h-56 w-full' />
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
    if (!report) {
      return (
        <p className='text-muted-foreground py-8 text-center text-sm'>
          {t('No data')}
        </p>
      )
    }
    return (
      <div className='space-y-6'>
        <Summary report={report} />

        <section className='space-y-3'>
          <div className='flex items-center gap-2'>
            <ShieldAlert className='text-primary h-4 w-4' />
            <h3 className='text-sm font-semibold'>{t('Key details')}</h3>
          </div>
          {items.length === 0 ? (
            <p className='text-muted-foreground text-sm'>
              {t('No key usage recorded in this window.')}
            </p>
          ) : (
            <div className='grid gap-3 lg:grid-cols-2'>
              {items.map((item) => (
                <RiskItemCard key={item.token_id} item={item} />
              ))}
            </div>
          )}
        </section>
      </div>
    )
  }, [items, query.error, query.isError, query.isLoading, report, t])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Key Risk Check')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Select value={String(days)} onValueChange={(v) => setDays(Number(v))}>
          <SelectTrigger className='w-40' aria-label={t('Window')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {TOKEN_RISK_DAY_OPTIONS.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {t(option.labelKey)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
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
