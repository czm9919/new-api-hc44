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
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { PageTransition } from '@/components/page-transition'
import { Skeleton } from '@/components/ui/skeleton'

import {
  MarketShareSection,
  ModelsSection,
  PricingLeaderboardSection,
  PulseSection,
  RankingsHero,
  UserLeaderboardSection,
} from './components'
import { useRankings } from './hooks/use-rankings'
import type { RankingPeriod, RankingsSnapshot } from './types'

const VALID_PERIODS = new Set<RankingPeriod>([
  'today',
  'week',
  'month',
  'year',
])

export function Rankings() {
  const search = useSearch({ from: '/rankings/' })
  const navigate = useNavigate()

  const period: RankingPeriod = VALID_PERIODS.has(
    search.period as RankingPeriod
  )
    ? (search.period as RankingPeriod)
    : 'week'

  const rankingsQuery = useRankings(period)
  const snapshot = rankingsQuery.data?.data

  const handlePeriodChange = (next: RankingPeriod) => {
    navigate({
      to: '/rankings',
      search: (prev) => ({ ...prev, period: next }),
    })
  }

  return (
    <PublicLayout showMainContainer={false}>
      <div className='relative'>
        <div
          aria-hidden
          className='pointer-events-none absolute inset-x-0 top-0 h-[600px] opacity-20 dark:opacity-[0.10]'
          style={{
            background: [
              'radial-gradient(ellipse 60% 50% at 20% 20%, oklch(0.72 0.18 250 / 80%) 0%, transparent 70%)',
              'radial-gradient(ellipse 50% 40% at 80% 15%, oklch(0.65 0.15 200 / 60%) 0%, transparent 70%)',
              'radial-gradient(ellipse 40% 35% at 50% 70%, oklch(0.70 0.12 280 / 40%) 0%, transparent 70%)',
            ].join(', '),
            maskImage:
              'linear-gradient(to bottom, black 40%, transparent 100%)',
            WebkitMaskImage:
              'linear-gradient(to bottom, black 40%, transparent 100%)',
          }}
        />
        <PageTransition className='relative mx-auto w-full max-w-[1280px] space-y-8 px-3 pt-16 pb-10 sm:px-6 sm:pt-20 sm:pb-12 xl:px-8'>
          <RankingsHero period={period} onPeriodChange={handlePeriodChange} />

          {/* 价格榜（hc41）：只依赖本站定价，用量数据为空时也照常展示。 */}
          <PricingLeaderboardSection />

          {/* 用户消耗榜（hc47）：独立接口，公开可看榜，登录后显示「我的排名」。 */}
          <UserLeaderboardSection period={period} />

          <SnapshotSections
            isLoading={rankingsQuery.isLoading}
            snapshot={snapshot}
            error={rankingsQuery.error}
            period={period}
          />
        </PageTransition>
      </div>
    </PublicLayout>
  )
}

/** 用量数据三态（加载中 / 失败 / 有数据）放在这里，避免页面主体堆嵌套三元。 */
function SnapshotSections(props: {
  isLoading: boolean
  snapshot?: RankingsSnapshot
  error: unknown
  period: RankingPeriod
}) {
  const { t } = useTranslation()
  const { isLoading, snapshot, error, period } = props

  if (isLoading) {
    return <RankingsLoading />
  }
  if (!snapshot) {
    return (
      <RankingsError
        message={
          error instanceof Error
            ? error.message
            : t('Unable to load rankings data')
        }
      />
    )
  }
  return (
    <>
      <ModelsSection
        history={snapshot.models_history}
        rows={snapshot.models}
        period={period}
      />

      <MarketShareSection
        history={snapshot.vendor_share_history}
        rows={snapshot.vendors}
        period={period}
      />

      <PulseSection
        movers={snapshot.top_movers}
        droppers={snapshot.top_droppers}
      />
    </>
  )
}

function RankingsLoading() {
  return (
    <div className='space-y-6'>
      <Skeleton className='h-[420px] w-full rounded-xl' />
      <Skeleton className='h-[360px] w-full rounded-xl' />
      <Skeleton className='h-[180px] w-full rounded-xl' />
    </div>
  )
}

function RankingsError(props: { message: string }) {
  const { t } = useTranslation()
  return (
    <div className='bg-card rounded-xl border border-dashed px-6 py-12 text-center'>
      <h2 className='text-foreground text-base font-semibold'>
        {t('Unable to load rankings')}
      </h2>
      <p className='text-muted-foreground mx-auto mt-2 max-w-md text-sm'>
        {props.message}
      </p>
    </div>
  )
}
