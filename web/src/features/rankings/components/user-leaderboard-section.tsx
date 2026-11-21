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
 * 用户消耗榜（hc47）。
 *
 * 这里只回答两个问题，不做成又一张报表：
 *  1. 本周期谁花得最多（打码用户名 + 占比）；
 *  2. 我自己排第几、离上一名还差多少（登录才有）。
 *
 * 用户名打码由后端完成，前端不再二次处理；`is_self` 只用来高亮自己那一行。
 */
import { Info, LogIn, Trophy } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatPercent, formatQuota } from '@/lib/format'

import { useUserRanking } from '../hooks/use-user-ranking'
import type {
  RankingPeriod,
  UserRankingEntry,
  UserRankingSelf,
  UserRankingSnapshot,
} from '../types'

const MEDAL_CLASSES: Record<number, string> = {
  1: 'bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/40',
  2: 'bg-slate-400/15 text-slate-600 dark:text-slate-300 border-slate-400/40',
  3: 'bg-orange-600/15 text-orange-700 dark:text-orange-400 border-orange-600/40',
}

function RankBadge(props: { rank: number }) {
  const medal = MEDAL_CLASSES[props.rank]
  if (!medal) {
    return (
      <span className='text-muted-foreground inline-block w-6 text-right font-mono text-xs tabular-nums'>
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

function useLocale() {
  const { i18n } = useTranslation()
  return toIntlLocale(i18n.resolvedLanguage || i18n.language)
}

/** 「我的排名」卡片：有榜次说差距，没榜次说怎么上榜。 */
function MyRankCard(props: { self: UserRankingSelf }) {
  const { t } = useTranslation()
  const { self } = props

  if (!self.ranked) {
    return (
      <div className='bg-muted/30 rounded-lg border border-dashed px-4 py-3'>
        <p className='text-sm font-medium'>{t('Not on the board yet')}</p>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t(
            'No consumption in this period yet — send one request to get on the board.'
          )}
        </p>
      </div>
    )
  }

  const isTop = self.rank <= 1
  return (
    <div className='bg-primary/5 border-primary/20 rounded-lg border px-4 py-3'>
      <div className='flex flex-wrap items-center gap-x-8 gap-y-3'>
        <div>
          <p className='text-muted-foreground text-xs'>{t('My rank')}</p>
          <p className='text-foreground font-mono text-2xl font-semibold tabular-nums'>
            #{self.rank}
            <span className='text-muted-foreground ml-1 text-xs font-normal'>
              {t('of {{count}}', { count: self.total_users })}
            </span>
          </p>
        </div>
        <div>
          <p className='text-muted-foreground text-xs'>{t('My spend')}</p>
          <p className='font-mono text-lg font-semibold tabular-nums'>
            {formatQuota(self.used_quota)}
          </p>
        </div>
        <div>
          <p className='text-muted-foreground text-xs'>
            {t('Gap to the next rank')}
          </p>
          <p className='text-lg font-semibold tabular-nums'>
            {isTop ? '—' : formatQuota(self.gap_to_ahead ?? 0)}
          </p>
        </div>
        <div className='min-w-0'>
          <p className='text-muted-foreground text-xs'>
            {t('Ahead of {{percent}} of users', {
              percent: formatPercent(self.percentile * 100),
            })}
          </p>
          <p className='mt-1 text-sm font-medium'>
            {isTop
              ? t('You are #1 this period — keep it up.')
              : t('Spend {{amount}} more to take the spot above you', {
                  amount: formatQuota(self.gap_to_ahead ?? 0),
                })}
          </p>
        </div>
      </div>
    </div>
  )
}

function BoardTable(props: { rows: UserRankingEntry[] }) {
  const { t } = useTranslation()
  const locale = useLocale()

  if (props.rows.length === 0) {
    return (
      <p className='text-muted-foreground/80 py-6 text-center text-sm'>
        {t('No consumption in this period yet')}
      </p>
    )
  }

  return (
    <div className='hover-scrollbar max-h-[520px] overflow-y-auto rounded-md border'>
      <table className='w-full border-collapse text-sm'>
        <thead className='bg-muted/40 sticky top-0 z-10 backdrop-blur'>
          <tr className='text-muted-foreground text-left text-xs'>
            <th className='w-14 px-3 py-2 text-right font-medium'>
              {t('Rank')}
            </th>
            <th className='px-3 py-2 font-medium'>{t('User')}</th>
            <th className='px-3 py-2 text-right font-medium'>{t('Spent')}</th>
            <th className='hidden px-3 py-2 text-right font-medium sm:table-cell'>
              {t('Requests')}
            </th>
            <th className='hidden px-3 py-2 font-medium md:table-cell'>
              {t('Share')}
            </th>
          </tr>
        </thead>
        <tbody>
          {props.rows.map((row) => (
            <tr
              key={row.user_id}
              className={
                row.is_self
                  ? 'bg-primary/5 border-t'
                  : 'hover:bg-muted/30 border-t'
              }
            >
              <td className='px-3 py-2 text-right'>
                <RankBadge rank={row.rank} />
              </td>
              <td className='max-w-[220px] px-3 py-2'>
                <span className='flex min-w-0 items-center gap-2'>
                  <span
                    className={`truncate ${row.is_self ? 'font-semibold' : ''}`}
                  >
                    {row.username}
                  </span>
                  {row.is_self ? (
                    <Badge variant='secondary' className='shrink-0 text-[10px]'>
                      {t('Me')}
                    </Badge>
                  ) : null}
                </span>
              </td>
              <td className='px-3 py-2 text-right font-mono tabular-nums'>
                {formatQuota(row.used_quota)}
              </td>
              <td className='text-muted-foreground hidden px-3 py-2 text-right tabular-nums sm:table-cell'>
                {formatNumber(row.request_count, locale)}
              </td>
              <td className='hidden px-3 py-2 md:table-cell'>
                <div className='flex items-center gap-2'>
                  <Progress
                    value={Math.min(
                      100,
                      Math.round(row.quota_share * 1000) / 10
                    )}
                    className='h-2 w-24'
                  />
                  <span className='text-muted-foreground text-xs tabular-nums'>
                    {formatPercent(row.quota_share * 100)}
                  </span>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

/** 登录引导：匿名访客看到的是这个，而不是一张没有数字的「我的排名」卡。 */
function SignInHint() {
  const { t } = useTranslation()
  return (
    <div className='bg-muted/30 flex items-start gap-2 rounded-lg border border-dashed px-4 py-3'>
      <LogIn className='text-muted-foreground mt-0.5 size-4 shrink-0' />
      <div>
        <p className='text-sm font-medium'>{t('Sign in to see your rank')}</p>
        <p className='text-muted-foreground mt-0.5 text-xs'>
          <a className='underline underline-offset-2' href='/sign-in'>
            {t('Sign in')}
          </a>{' '}
          {t(
            'and this card shows your rank and the gap to the user above you.'
          )}
        </p>
      </div>
    </div>
  )
}

/**
 * 正文三态：加载中 / 拿不到数据 / 有数据。
 *
 * 拆成独立组件是为了避开 no-nested-ternary：三态写成 if 链更好读，
 * 也方便单测单独命中某一态。
 */
function SectionBody(props: {
  isLoading: boolean
  data?: UserRankingSnapshot
  error: unknown
}) {
  const { t } = useTranslation()

  if (props.isLoading) {
    return (
      <div className='space-y-2'>
        <Skeleton className='h-16 w-full' />
        <Skeleton className='h-8 w-full' />
        <Skeleton className='h-8 w-full' />
      </div>
    )
  }

  if (!props.data) {
    return (
      <p className='text-muted-foreground/80 py-6 text-center text-sm'>
        {props.error instanceof Error
          ? props.error.message
          : t('Unable to load the user leaderboard')}
      </p>
    )
  }

  const data = props.data
  return (
    <>
      {data.self ? <MyRankCard self={data.self} /> : <SignInHint />}

      <BoardTable rows={data.leaderboard} />

      <p className='text-muted-foreground/70 flex items-start gap-1.5 text-xs leading-relaxed'>
        <Info className='mt-0.5 size-3.5 shrink-0' />
        <span>
          {t(
            'Usernames are masked. The board is recomputed every minute; only consumption logs count.'
          )}
        </span>
      </p>
    </>
  )
}

export function UserLeaderboardSection(props: { period: RankingPeriod }) {
  const { t } = useTranslation()
  const locale = useLocale()
  const query = useUserRanking(props.period)
  const data = query.data?.data

  return (
    <section className='bg-card overflow-hidden rounded-lg border'>
      <header className='flex items-start justify-between gap-4 px-5 py-4'>
        <div className='min-w-0 flex-1'>
          <h2 className='text-foreground inline-flex items-center gap-2 text-base font-semibold'>
            <Trophy className='text-primary size-4' />
            {t('User Leaderboard')}
          </h2>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t(
              'Ranked by consumption in this period. Usernames are masked; sign in to see your own rank.'
            )}
          </p>
        </div>
        {data && (
          <div className='shrink-0 text-right'>
            <div className='text-foreground font-mono text-2xl font-semibold tabular-nums'>
              {formatNumber(data.totals.user_count, locale)}
            </div>
            <div className='text-muted-foreground/80 text-[10px] font-medium tracking-widest uppercase'>
              {t('participants')}
            </div>
          </div>
        )}
      </header>

      <div className='space-y-4 px-5 pb-4'>
        <SectionBody
          isLoading={query.isLoading}
          data={data}
          error={query.error}
        />
      </div>
    </section>
  )
}
