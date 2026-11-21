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
 * 成本优化建议页（/cost-insight，hc31 · B89 的前端落地）。
 *
 * 只做三件事，避免变成又一个「用量报表」：
 *  1. 顶部四个数字回答「这段时间花了多少、打了多少次」；
 *  2. 模型表回答「钱花在哪些模型上」（按消耗降序，带占比进度条）；
 *  3. 建议卡片回答「下一步能怎么省」——替代模型直接给出可省额度与百分比。
 *
 * 数据来源是 GET /api/user/cost-insight?days=，只读、无副作用，
 * 因此这里不做任何本地缓存合并，切窗口就重新拉一次。
 */
import { useQuery } from '@tanstack/react-query'
import { Lightbulb, RefreshCw, TrendingDown, Wallet } from 'lucide-react'
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
import { formatNumber, formatPercent, formatQuota } from '@/lib/format'

import {
  COST_INSIGHT_DAY_OPTIONS,
  getCostInsight,
  type CostInsight,
  type CostInsightModel,
  type CostInsightSuggestion,
} from './api'

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

function SummaryTiles(props: { data: CostInsight }) {
  const { t } = useTranslation()
  const locale = useLocale()
  const { data } = props
  return (
    <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
      <StatTile
        icon={<Wallet className='h-4 w-4' />}
        label={t('Total spent')}
        value={formatQuota(data.total_used_quota)}
        hint={t('Last {{days}} days', { days: data.window_days })}
      />
      <StatTile
        icon={<TrendingDown className='h-4 w-4' />}
        label={t('Requests')}
        value={formatNumber(data.total_requests, locale)}
      />
      <StatTile
        icon={<Lightbulb className='h-4 w-4' />}
        label={t('Models used')}
        value={formatNumber(data.models.length, locale)}
      />
      <StatTile
        icon={<Lightbulb className='h-4 w-4' />}
        label={t('Suggestions')}
        value={formatNumber(data.suggestions.length, locale)}
        hint={
          data.suggestions.length > 0
            ? t('Switch a model to save')
            : t('No obvious saving found')
        }
      />
    </div>
  )
}

function ModelsTable(props: { models: CostInsightModel[] }) {
  const { t } = useTranslation()
  const locale = useLocale()

  if (props.models.length === 0) {
    return (
      <p className='text-muted-foreground py-6 text-center text-sm'>
        {t('No usage in this window')}
      </p>
    )
  }

  return (
    <div className='overflow-x-auto'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className='min-w-48'>{t('Model')}</TableHead>
            <TableHead className='text-right'>{t('Spent')}</TableHead>
            <TableHead className='min-w-40'>{t('Share')}</TableHead>
            <TableHead className='text-right'>{t('Requests')}</TableHead>
            <TableHead className='text-right'>{t('Tokens')}</TableHead>
            <TableHead className='text-right'>{t('Ratio')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {props.models.map((model) => (
            <TableRow key={model.model_name}>
              <TableCell className='font-medium'>{model.model_name}</TableCell>
              <TableCell className='text-right tabular-nums'>
                {formatQuota(model.used_quota)}
              </TableCell>
              <TableCell>
                <div className='flex items-center gap-2'>
                  <Progress
                    value={Math.min(100, Math.round(model.quota_share * 100))}
                    className='h-2 w-24'
                  />
                  <span className='text-muted-foreground text-xs tabular-nums'>
                    {formatPercent(model.quota_share * 100)}
                  </span>
                </div>
              </TableCell>
              <TableCell className='text-right tabular-nums'>
                {formatNumber(model.request_count, locale)}
              </TableCell>
              <TableCell className='text-right text-xs tabular-nums'>
                {formatNumber(model.prompt_tokens, locale)} /{' '}
                {formatNumber(model.completion_tokens, locale)}
              </TableCell>
              <TableCell className='text-right tabular-nums'>
                {formatNumber(model.effective_ratio, locale)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function reasonBadge(suggestion: CostInsightSuggestion) {
  if (suggestion.reason_code === 'cheaper_alternative') {
    return {
      textKey: 'Cheaper alternative',
      variant: 'warning' as const,
    }
  }
  return { textKey: 'Dominant spending', variant: 'secondary' as const }
}

function SuggestionCard(props: { suggestion: CostInsightSuggestion }) {
  const { t } = useTranslation()
  const locale = useLocale()
  const { suggestion } = props
  const badge = reasonBadge(suggestion)

  return (
    <Card>
      <CardHeader className='pb-3'>
        <CardTitle className='flex flex-wrap items-center gap-2 text-base'>
          <Lightbulb className='h-4 w-4' />
          <span className='truncate'>{suggestion.model_name}</span>
          <Badge variant={badge.variant}>{t(badge.textKey)}</Badge>
        </CardTitle>
        {suggestion.reason ? (
          <CardDescription>{suggestion.reason}</CardDescription>
        ) : null}
      </CardHeader>
      <CardContent className='space-y-3'>
        <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
          <span>
            {t('Spent')} {formatQuota(suggestion.model.used_quota)}
          </span>
          <span>
            {t('Share')} {formatPercent(suggestion.model.quota_share * 100)}
          </span>
          <span>
            {t('Effective ratio')}{' '}
            {formatNumber(suggestion.model.effective_ratio, locale)}
          </span>
        </div>

        {suggestion.alternatives.length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {t('No cheaper model found for this one')}
          </p>
        ) : (
          <div className='divide-y rounded-lg border'>
            {suggestion.alternatives.map((alternative) => (
              <div
                key={alternative.model_name}
                className='flex flex-wrap items-center justify-between gap-2 px-3 py-2'
              >
                <div className='min-w-0'>
                  <p className='truncate text-sm font-medium'>
                    {alternative.model_name}
                  </p>
                  <p className='text-muted-foreground text-xs'>
                    {t('Ratio')}{' '}
                    {formatNumber(alternative.effective_ratio, locale)}
                  </p>
                </div>
                <div className='flex items-center gap-2'>
                  <Badge variant='outline' className='tabular-nums'>
                    -{formatPercent(alternative.estimated_saving_percent * 100)}
                  </Badge>
                  <span className='text-sm font-medium tabular-nums'>
                    {formatQuota(alternative.estimated_saving_quota)}
                  </span>
                </div>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

export function CostInsightPage() {
  const { t } = useTranslation()
  // 窗口天数用局部 state 托管，默认 30 天（与后端缺省一致）。
  const [days, setDays] = useState(30)

  const query = useQuery({
    queryKey: ['cost-insight', days],
    staleTime: 60 * 1000,
    queryFn: () => getCostInsight(days),
  })

  const data = query.data

  const body = useMemo<ReactNode>(() => {
    if (query.isLoading) {
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
              {t('Where the money went')}
            </CardTitle>
            <CardDescription>
              {t('Sorted by spend. Share is of the total in this window.')}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <ModelsTable models={data.models} />
          </CardContent>
        </Card>

        <section className='space-y-3'>
          <div className='flex items-center gap-2'>
            <Lightbulb className='text-primary h-4 w-4' />
            <h3 className='text-sm font-semibold'>{t('What to change')}</h3>
          </div>
          {data.suggestions.length === 0 ? (
            <p className='text-muted-foreground text-sm'>
              {t(
                'No saving opportunity detected. Spending is spread across models already.'
              )}
            </p>
          ) : (
            <div className='grid gap-3 lg:grid-cols-2'>
              {data.suggestions.map((suggestion) => (
                <SuggestionCard
                  key={`${suggestion.reason_code}-${suggestion.model_name}`}
                  suggestion={suggestion}
                />
              ))}
            </div>
          )}
        </section>
      </div>
    )
  }, [data, query.error, query.isError, query.isLoading, t])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Cost Insights')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Select value={String(days)} onValueChange={(v) => setDays(Number(v))}>
          <SelectTrigger className='w-40' aria-label={t('Window')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {COST_INSIGHT_DAY_OPTIONS.map((option) => (
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
