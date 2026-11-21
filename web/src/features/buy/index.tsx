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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Check, Crown, Loader2 } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { getPublicSubscriptionPlans } from './api'
import type { PublicSubscriptionPlan } from './types'

function currencySymbol(currency?: string) {
  switch ((currency || '').toUpperCase()) {
    case 'CNY':
    case 'RMB':
    case 'JPY':
      return '¥'
    case 'USD':
      return '$'
    case 'EUR':
      return '€'
    default:
      return ''
  }
}

function formatDurationLabel(
  plan: PublicSubscriptionPlan,
  t: (key: string) => string
) {
  if (plan.duration_unit === 'custom') {
    const seconds = plan.custom_seconds || 0
    if (seconds >= 86400) return `${Math.floor(seconds / 86400)} ${t('days')}`
    if (seconds >= 3600) return `${Math.floor(seconds / 3600)} ${t('hours')}`
    return `${seconds} ${t('seconds')}`
  }
  const unitLabels: Record<string, string> = {
    year: t('years'),
    month: t('months'),
    day: t('days'),
    hour: t('hours'),
  }
  return `${plan.duration_value || 1} ${unitLabels[plan.duration_unit] || plan.duration_unit}`
}

function formatResetLabel(
  plan: PublicSubscriptionPlan,
  t: (key: string) => string
) {
  switch (plan.quota_reset_period) {
    case 'daily':
      return t('Daily')
    case 'weekly':
      return t('Weekly')
    case 'monthly':
      return t('Monthly')
    case 'never':
    case '':
      return t('No Reset')
    default:
      return plan.quota_reset_period
  }
}

function PlanCard(props: {
  plan: PublicSubscriptionPlan
  isLoggedIn: boolean
}) {
  const { t } = useTranslation()
  const { plan, isLoggedIn } = props
  const symbol = currencySymbol(plan.currency)
  const price = Number(plan.price_amount || 0).toFixed(2)
  const benefits = [
    `${t('Validity Period')}: ${formatDurationLabel(plan, t)}`,
    `${t('Quota Reset')}: ${formatResetLabel(plan, t)}`,
    plan.total_amount > 0
      ? `${t('Total Quota')}: ${formatQuota(plan.total_amount)}`
      : `${t('Total Quota')}: ${t('Unlimited')}`,
  ]
  return (
    <Card data-card-hover='false' className='h-full'>
      <CardContent className='flex h-full flex-col p-4'>
        <div className='min-w-0'>
          <h3 className='truncate text-base font-semibold'>
            {plan.title || t('Subscription Plans')}
          </h3>
          {plan.subtitle && (
            <p className='text-muted-foreground mt-0.5 line-clamp-2 text-xs'>
              {plan.subtitle}
            </p>
          )}
        </div>
        <div className='py-3'>
          <span className='text-primary text-2xl font-bold tabular-nums'>
            {symbol}
            {price}
          </span>
          {symbol === '' && plan.currency && (
            <span className='text-muted-foreground ml-1 text-xs'>
              {plan.currency}
            </span>
          )}
        </div>
        <ul className='flex-1 space-y-1.5 pb-3'>
          {benefits.map((label) => (
            <li
              key={label}
              className='text-muted-foreground flex items-center gap-2 text-xs'
            >
              <Check className='text-primary h-3 w-3 shrink-0' />
              <span>{label}</span>
            </li>
          ))}
        </ul>
        <Button
          className='mt-auto w-full'
          render={
            <Link
              to={isLoggedIn ? '/wallet' : '/sign-in'}
              search={isLoggedIn ? undefined : { redirect: '/buy' }}
            />
          }
        >
          {isLoggedIn ? t('Buy Now') : t('Login to Purchase')}
        </Button>
      </CardContent>
    </Card>
  )
}

export function Buy() {
  const { t } = useTranslation()
  const { auth } = useAuthStore()
  const isLoggedIn = Boolean(auth.user)

  const plansQuery = useQuery({
    queryKey: ['public-subscription-plans'],
    staleTime: 60 * 1000,
    queryFn: async () => {
      const res = await getPublicSubscriptionPlans()
      if (!res.success) {
        throw new Error(res.message || 'Failed to load plans')
      }
      return res.data
    },
  })

  let body: ReactNode
  if (plansQuery.isLoading) {
    body = (
      <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3'>
        {['a', 'b', 'c'].map((key) => (
          <Skeleton key={key} className='h-56 w-full' />
        ))}
      </div>
    )
  } else if (plansQuery.isError) {
    body = (
      <div className='text-destructive flex items-center justify-center gap-2 py-8 text-sm'>
        <Loader2 className='size-4 animate-spin' aria-hidden='true' />
        {plansQuery.error.message}
      </div>
    )
  } else if ((plansQuery.data ?? []).length === 0) {
    body = (
      <div className='text-muted-foreground py-8 text-center text-sm'>
        {t('No plans available')}
      </div>
    )
  } else {
    body = (
      <>
        <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3'>
          {(plansQuery.data ?? []).map((plan) => (
            <PlanCard key={plan.id} plan={plan} isLoggedIn={isLoggedIn} />
          ))}
        </div>
        <p className='text-muted-foreground mt-6 text-center text-sm'>
          {isLoggedIn
            ? t('Go to Wallet to Purchase')
            : t('View available plans and top up')}
        </p>
      </>
    )
  }

  return (
    <PublicLayout>
      <div className='mx-auto w-full max-w-5xl py-8'>
        <header className='mb-8 text-center'>
          <div className='text-primary mx-auto mb-3 flex justify-center'>
            <Crown className='h-8 w-8' aria-hidden='true' />
          </div>
          <h1 className='text-3xl font-bold tracking-tight'>
            {t('Buy Plans')}
          </h1>
          <p className='text-muted-foreground mt-2 text-sm'>
            {t('View available plans and top up')}
          </p>
        </header>

        {body}
      </div>
    </PublicLayout>
  )
}
