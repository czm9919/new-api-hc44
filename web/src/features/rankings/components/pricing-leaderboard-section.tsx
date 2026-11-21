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
import { Coins, Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'

import { usePricingLeaderboard } from '../hooks/use-pricing-leaderboard'
import { discountTone, formatDiscount, formatUnitPrice } from '../lib/pricing'
import type { PricingLeaderboard, PricingLeaderboardRow } from '../types'

const TONE_CLASS: Record<'cheaper' | 'pricier' | 'flat', string> = {
  cheaper: 'text-emerald-600 dark:text-emerald-400',
  pricier: 'text-rose-600 dark:text-rose-400',
  flat: 'text-muted-foreground',
}

function formatFetchedAt(value?: number): string {
  if (!value) {
    return '—'
  }
  const date = new Date(value * 1000)
  if (Number.isNaN(date.getTime())) {
    return '—'
  }
  return date.toLocaleString()
}

type Translate = (key: string, options?: Record<string, unknown>) => string

/** 把秒数说成人话：刚刚 / x 分钟前 / x 小时前 / x 天前。 */
function formatAge(t: Translate, seconds?: number): string {
  if (seconds === undefined || seconds < 0) {
    return '—'
  }
  if (seconds < 60) {
    return t('just now')
  }
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) {
    return t('{{count}} min ago', { count: minutes })
  }
  const hours = Math.floor(minutes / 60)
  if (hours < 24) {
    return t('{{count}} h ago', { count: hours })
  }
  return t('{{count}} d ago', { count: Math.floor(hours / 24) })
}

/** 同步间隔也只说人话：6 小时、30 分钟。 */
function formatInterval(t: Translate, seconds?: number): string {
  if (!seconds || seconds <= 0) {
    return '—'
  }
  if (seconds < 3600) {
    return t('{{count}} min', { count: Math.max(1, Math.round(seconds / 60)) })
  }
  return t('{{count}} h', { count: Math.round(seconds / 3600) })
}

/**
 * 底部状态行。除了「什么时候同步的」，还要把三种异常说清楚：
 * 自动同步开没开、快照是不是正在后台更新、有没有源这次抓失败在吃旧数据。
 */
function buildFooter(
  t: Translate,
  data: PricingLeaderboard | undefined
): string[] {
  if (!data) {
    return []
  }
  if (!data.official_available) {
    return [t('Official prices have not been synced by the administrator yet.')]
  }
  const parts = [
    t('Official prices synced {{time}}', {
      time: formatFetchedAt(data.fetched_at),
    }),
  ]
  if (data.age_seconds !== undefined) {
    parts.push(`(${formatAge(t, data.age_seconds)})`)
  }
  if (data.auto_sync) {
    parts.push(
      t('auto-sync every {{interval}}', {
        interval: formatInterval(t, data.sync_interval),
      })
    )
  }
  if (data.stale) {
    parts.push(t('refreshing in background'))
  }
  if (data.carried && data.carried.length > 0) {
    parts.push(
      t('{{sources}} fetch failed, using the previous data', {
        sources: data.carried.join(', '),
      })
    )
  }
  return parts
}

function LoadingRows() {
  return (
    <div className='space-y-2'>
      <Skeleton className='h-8 w-full' />
      <Skeleton className='h-8 w-full' />
      <Skeleton className='h-8 w-full' />
    </div>
  )
}

/**
 * 价格榜：本站模型单价从低到高排列，方便用户挑最省钱的模型。
 * 官方对比列只在管理员「一键同步官方定价」之后才出现。
 */
export function PricingLeaderboardSection() {
  const { t } = useTranslation()
  const query = usePricingLeaderboard()
  const data = query.data?.data

  const renderRows = (rows: PricingLeaderboardRow[], withOfficial: boolean) => (
    <div className='hover-scrollbar max-h-[520px] overflow-y-auto rounded-md border'>
      <table className='w-full border-collapse text-sm'>
        <thead className='bg-muted/40 sticky top-0 z-10 backdrop-blur'>
          <tr className='text-muted-foreground text-left text-xs'>
            <th className='w-12 px-3 py-2 font-medium'>#</th>
            <th className='px-3 py-2 font-medium'>{t('Model')}</th>
            <th className='hidden px-3 py-2 font-medium sm:table-cell'>
              {t('Vendor')}
            </th>
            <th className='px-3 py-2 text-right font-medium'>
              {t('Input / 1M')}
            </th>
            <th className='hidden px-3 py-2 text-right font-medium sm:table-cell'>
              {t('Output / 1M')}
            </th>
            {withOfficial && (
              <th className='hidden px-3 py-2 text-right font-medium md:table-cell'>
                {t('Official input / 1M')}
              </th>
            )}
            {withOfficial && (
              <th className='px-3 py-2 text-right font-medium'>
                {t('vs Official')}
              </th>
            )}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const isRequest = row.unit === 'request'
            return (
              <tr
                key={row.model_name}
                className='hover:bg-muted/30 border-t last:border-b-0'
              >
                <td className='text-muted-foreground px-3 py-2 font-mono text-xs tabular-nums'>
                  {row.rank}
                </td>
                <td className='max-w-[240px] truncate px-3 py-2 font-medium'>
                  {row.model_name}
                </td>
                <td className='text-muted-foreground hidden max-w-[140px] truncate px-3 py-2 text-xs sm:table-cell'>
                  {row.vendor || '—'}
                </td>
                <td className='px-3 py-2 text-right font-mono tabular-nums'>
                  {isRequest
                    ? t('{{price}} / call', {
                        price: formatUnitPrice(row.site_request_price),
                      })
                    : formatUnitPrice(row.site_input_price)}
                </td>
                <td className='text-muted-foreground hidden px-3 py-2 text-right font-mono tabular-nums sm:table-cell'>
                  {isRequest ? '—' : formatUnitPrice(row.site_output_price)}
                </td>
                {withOfficial && (
                  <td className='text-muted-foreground hidden px-3 py-2 text-right font-mono tabular-nums md:table-cell'>
                    {row.has_official
                      ? formatUnitPrice(row.official_input_price)
                      : '—'}
                  </td>
                )}
                {withOfficial && (
                  <td
                    className={`px-3 py-2 text-right font-mono tabular-nums ${TONE_CLASS[discountTone(row.discount_pct)]}`}
                  >
                    {row.has_official && !isRequest
                      ? formatDiscount(row.discount_pct)
                      : '—'}
                  </td>
                )}
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )

  const renderBody = (snapshot: PricingLeaderboard) => {
    if (query.isLoading) {
      return <LoadingRows />
    }
    if (query.isError) {
      return (
        <p className='text-muted-foreground/80 py-6 text-center text-sm'>
          {query.error instanceof Error
            ? query.error.message
            : t('Unable to load price leaderboard')}
        </p>
      )
    }
    if (snapshot.rows.length === 0) {
      return (
        <p className='text-muted-foreground/80 py-6 text-center text-sm'>
          {t('No priced models yet')}
        </p>
      )
    }
    return renderRows(snapshot.rows, snapshot.official_available)
  }

  const footerParts = buildFooter(t, data)

  return (
    <section className='bg-card overflow-hidden rounded-lg border'>
      <header className='flex items-start justify-between gap-4 px-5 py-4'>
        <div className='min-w-0 flex-1'>
          <h2 className='text-foreground inline-flex items-center gap-2 text-base font-semibold'>
            <Coins className='text-primary size-4' />
            {t('Price Leaderboard')}
          </h2>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t('Models ranked by our price, cheapest first')}
          </p>
        </div>
        {data && (
          <div className='shrink-0 text-right'>
            <div className='text-foreground font-mono text-2xl font-semibold tabular-nums'>
              {data.total}
            </div>
            <div className='text-muted-foreground/80 text-[10px] font-medium tracking-widest uppercase'>
              {t('models')}
            </div>
          </div>
        )}
      </header>

      <div className='px-5 pb-4'>
        {data ? renderBody(data) : <LoadingRows />}

        <p className='text-muted-foreground/70 mt-3 flex items-start gap-1.5 text-xs leading-relaxed'>
          <Info className='mt-0.5 size-3.5 shrink-0' />
          <span>
            {t(
              'Prices are in USD per 1M tokens; per-call models are listed after token-priced models.'
            )}
            {footerParts.length > 0 && <>{' '}{footerParts.join(' · ')}</>}
          </span>
        </p>
      </div>
    </section>
  )
}
