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
 * 自研模型展示页（/lab）：纯展示 + 求赞助。
 *
 * 定位是「展厅」而不是「车间」：与主站路由、计费、模型列表完全隔离——
 * 该模型不进 abilities 表，任何业务请求都不会路由到它，也不产生计费。
 * 页面只消费两个匿名只读接口：展示配置（LabSetting）与推理状态代理。
 *
 * 设计原则（按站长叙事）：限制即进度条——上下文低、速度低都如实展示，
 * 并与赞助目标挂钩；不与任何商业模型做并排对比，只讲自己的故事。
 */
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  Activity,
  Cpu,
  Gauge,
  Heart,
  Laptop,
  Sparkles,
  Thermometer,
  Timer,
  Zap,
} from 'lucide-react'
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
 * 自研模型展示页（/lab）：纯展示 + 求赞助。
 *
 * 定位是「展厅」而不是「车间」：与主站路由、计费、模型列表完全隔离——
 * 该模型不进 abilities 表，任何业务请求都不会路由到它，也不产生计费。
 * 页面只消费两个匿名只读接口：展示配置（LabSetting）与推理状态代理。
 *
 * 设计原则（按站长叙事）：限制即进度条——上下文低、速度低都如实展示，
 * 并与赞助目标挂钩；不与任何商业模型做并排对比，只讲自己的故事。
 */
import { useMemo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'

import { getLabConfig, getLabStatus } from './api'
import type { LabConfig, LabSponsor, LabTimelineEntry } from './types'

const STATUS_REFRESH_SECONDS = 10

/** 推理状态端点的常见字段 → 展示标签与图标（未识别的字段不展示）。 */
const STATUS_FIELD_LABELS: Array<{
  key: string
  labelKey: string
  suffix?: string
  icon: ReactNode
}> = [
  { key: 'gpu', labelKey: 'GPU', icon: <Cpu className='h-4 w-4' /> },
  {
    key: 'util',
    labelKey: 'Utilization',
    suffix: '%',
    icon: <Activity className='h-4 w-4' />,
  },
  {
    key: 'temp',
    labelKey: 'Temperature',
    suffix: '°C',
    icon: <Thermometer className='h-4 w-4' />,
  },
  { key: 'uptime', labelKey: 'Uptime', icon: <Timer className='h-4 w-4' /> },
  {
    key: 'tokens_total',
    labelKey: 'tokens generated',
    icon: <Sparkles className='h-4 w-4' />,
  },
  {
    key: 'rps',
    labelKey: 'Speed',
    suffix: ' req/s',
    icon: <Gauge className='h-4 w-4' />,
  },
]

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

function formatInt(value?: number) {
  if (!value || value <= 0) return null
  return value.toLocaleString()
}

function StatTile(props: { icon: ReactNode; label: string; value: ReactNode }) {
  return (
    <div className='bg-card flex items-start gap-3 rounded-xl border p-4'>
      <div className='text-primary mt-0.5'>{props.icon}</div>
      <div className='min-w-0'>
        <p className='text-muted-foreground text-xs'>{props.label}</p>
        <p className='mt-1 truncate text-lg font-semibold'>{props.value}</p>
      </div>
    </div>
  )
}

function StatusPanel(props: { status?: Record<string, unknown> }) {
  const { t } = useTranslation()
  const rows = STATUS_FIELD_LABELS.filter((field) => {
    const value = props.status?.[field.key]
    return value !== undefined && value !== null && value !== ''
  })
  return (
    <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3'>
      {rows.map((field) => (
        <StatTile
          key={field.key}
          icon={field.icon}
          label={t(field.labelKey)}
          value={`${String(props.status?.[field.key])}${field.suffix || ''}`}
        />
      ))}
    </div>
  )
}

function TimelineBlock(props: { entries: LabTimelineEntry[] }) {
  const { t } = useTranslation()
  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Sparkles className='h-4 w-4' /> {t('Training Timeline')}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <ol className='relative space-y-6 border-l pl-6'>
          {props.entries.map((entry) => (
            <li key={`${entry.date || ''}-${entry.title}`} className='relative'>
              <span className='bg-primary absolute top-1.5 -left-[31px] h-2.5 w-2.5 rounded-full' />
              {entry.date ? (
                <p className='text-muted-foreground text-xs'>{entry.date}</p>
              ) : null}
              <p className='font-medium'>{entry.title}</p>
              {entry.detail ? (
                <p className='text-muted-foreground mt-1 text-sm'>
                  {entry.detail}
                </p>
              ) : null}
            </li>
          ))}
        </ol>
      </CardContent>
    </Card>
  )
}

function SponsorPanel(props: { sponsor: LabSponsor }) {
  const { t } = useTranslation()
  const { sponsor } = props
  const target = sponsor.target || 0
  const raised = sponsor.raised || 0
  const percent =
    target > 0 ? Math.min(100, Math.round((raised / target) * 100)) : 0
  const symbol = currencySymbol(sponsor.currency)

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Heart className='h-4 w-4' /> {t('Sponsorship')}
        </CardTitle>
      </CardHeader>
      <CardContent className='space-y-4'>
        {target > 0 ? (
          <div className='space-y-2'>
            <div className='flex items-baseline justify-between text-sm'>
              <span className='font-semibold'>
                {symbol}
                {raised.toLocaleString()}
              </span>
              <span className='text-muted-foreground'>
                {t('Raised')} {percent}% · {t('Target')} {symbol}
                {target.toLocaleString()}
              </span>
            </div>
            <Progress value={percent} />
          </div>
        ) : null}

        {sponsor.note ? (
          <p className='text-muted-foreground text-sm'>{sponsor.note}</p>
        ) : null}

        {(sponsor.methods || []).length > 0 ? (
          <div className='grid gap-4 sm:grid-cols-2'>
            {(sponsor.methods || []).map((method) => (
              <div
                key={method.name}
                className='space-y-2 rounded-lg border p-3'
              >
                <p className='text-sm font-medium'>{method.name}</p>
                {method.qrcode_url ? (
                  <img
                    src={method.qrcode_url}
                    alt={method.name}
                    className='h-36 w-36 rounded border object-contain'
                    loading='lazy'
                  />
                ) : null}
                {method.url ? (
                  <a
                    href={method.url}
                    target='_blank'
                    rel='noreferrer'
                    className='text-primary inline-flex items-center gap-1 text-sm hover:underline'
                  >
                    {t('Sponsor')} →
                  </a>
                ) : null}
              </div>
            ))}
          </div>
        ) : null}
      </CardContent>
    </Card>
  )
}

function DemoPanel(props: { config: LabConfig }) {
  const { t } = useTranslation()
  if (!props.config.demo_url) {
    return null
  }
  if (props.config.demo_embed) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className='flex items-center gap-2 text-base'>
            <Zap className='h-4 w-4' /> {t('Try the Demo')}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <iframe
            src={props.config.demo_url}
            title='demo'
            className='h-[480px] w-full rounded-lg border'
            loading='lazy'
            sandbox='allow-scripts allow-forms allow-popups'
          />
        </CardContent>
      </Card>
    )
  }
  return (
    <a
      href={props.config.demo_url}
      target='_blank'
      rel='noreferrer'
      className='text-primary inline-flex items-center gap-1 text-sm hover:underline'
    >
      <Zap className='h-4 w-4' /> {t('Try the Demo')} →
    </a>
  )
}

export function Lab() {
  const { t } = useTranslation()

  const configQuery = useQuery({
    queryKey: ['lab-config'],
    staleTime: 60 * 1000,
    queryFn: async () => {
      const res = await getLabConfig()
      if (!res.success) {
        throw new Error(res.message || 'Failed to load lab config')
      }
      return res.data
    },
  })

  const config = configQuery.data
  const hasStatus = Boolean(config?.status_url)

  const statusQuery = useQuery({
    queryKey: ['lab-status'],
    enabled: hasStatus,
    refetchInterval: STATUS_REFRESH_SECONDS * 1000,
    queryFn: async () => {
      const res = await getLabStatus()
      if (!res.success) {
        throw new Error(res.message || 'Failed to load lab status')
      }
      return res.data
    },
  })

  const timeline = useMemo(
    () => (config?.timeline || []).filter((entry) => entry.title),
    [config]
  )

  let body: ReactNode
  if (configQuery.isLoading) {
    body = (
      <div className='space-y-6'>
        <Skeleton className='h-40 w-full' />
        <Skeleton className='h-56 w-full' />
      </div>
    )
  } else if (configQuery.isError) {
    body = (
      <p className='text-destructive py-8 text-center text-sm'>
        {configQuery.error.message}
      </p>
    )
  } else if (!config || config.enabled === false || !config.model_name) {
    body = (
      <p className='text-muted-foreground py-8 text-center text-sm'>
        {t('Not Available Yet')}
      </p>
    )
  } else {
    const contextLabel = formatInt(config.context_tokens)
    const statusPayload = statusQuery.data?.status

    let statusBody: ReactNode
    if (!hasStatus) {
      statusBody = (
        <p className='text-muted-foreground text-sm'>
          {t('Status feed not configured')}
        </p>
      )
    } else if (statusQuery.isLoading) {
      statusBody = <Skeleton className='h-24 w-full' />
    } else if (statusQuery.isError) {
      statusBody = (
        <p className='text-destructive text-sm'>{statusQuery.error.message}</p>
      )
    } else if (statusQuery.data?.online && statusPayload) {
      statusBody = <StatusPanel status={statusPayload} />
    } else {
      statusBody = (
        <p className='text-muted-foreground text-sm'>
          {t('Status unavailable')}
        </p>
      )
    }
    body = (
      <div className='space-y-8'>
        <section className='from-primary/10 via-card to-card relative overflow-hidden rounded-2xl border bg-gradient-to-br p-8'>
          <div className='flex flex-wrap items-center gap-2'>
            <Badge variant='secondary'>
              {t('A model built from scratch, no pre-trained base')}
            </Badge>
            <Badge variant='secondary'>
              {t("Runs on the operator's own server")}
            </Badge>
          </div>
          <h1 className='mt-4 text-3xl font-bold tracking-tight'>
            {config.model_name}
          </h1>
          {config.tagline ? (
            <p className='text-muted-foreground mt-2 max-w-2xl text-sm'>
              {config.tagline}
            </p>
          ) : null}
          <div className='mt-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
            <StatTile
              icon={<Laptop className='h-4 w-4' />}
              label={t('Hardware')}
              value={config.hardware || t('Self-hosted')}
            />
            <StatTile
              icon={<Cpu className='h-4 w-4' />}
              label={t('Parameters')}
              value={config.params || '—'}
            />
            <StatTile
              icon={<Timer className='h-4 w-4' />}
              label={t('Context')}
              value={contextLabel ? `${contextLabel} tokens` : '—'}
            />
            <StatTile
              icon={<Gauge className='h-4 w-4' />}
              label={t('Speed')}
              value={
                config.speed_tps && config.speed_tps > 0
                  ? `${config.speed_tps} tok/s`
                  : '—'
              }
            />
          </div>
        </section>

        <section>
          <div className='mb-3 flex items-center gap-2'>
            <Activity className='text-primary h-4 w-4' />
            <h2 className='text-lg font-semibold'>{t('Live Status')}</h2>
            <Badge variant={statusQuery.data?.online ? 'default' : 'secondary'}>
              {statusQuery.data?.online ? t('Online') : t('Offline')}
            </Badge>
          </div>
          {statusBody}
        </section>

        {config.intro ? (
          <Card>
            <CardContent className='pt-6'>
              <p className='text-sm whitespace-pre-wrap'>{config.intro}</p>
            </CardContent>
          </Card>
        ) : null}

        {timeline.length > 0 ? <TimelineBlock entries={timeline} /> : null}

        <DemoPanel config={config} />

        {config.sponsor ? <SponsorPanel sponsor={config.sponsor} /> : null}

        {(config.acknowledgements || []).length > 0 ? (
          <Card>
            <CardHeader>
              <CardTitle className='text-base'>
                {t('Acknowledgements')}
              </CardTitle>
            </CardHeader>
            <CardContent className='flex flex-wrap gap-2'>
              {(config.acknowledgements || []).map((name) => (
                <Badge key={name} variant='outline'>
                  {name}
                </Badge>
              ))}
            </CardContent>
          </Card>
        ) : null}

        <div className='text-muted-foreground space-y-1 text-center text-xs'>
          <p>
            {t(
              'This lab is fully isolated from the main service: not listed, not billed, never routed'
            )}
          </p>
          <p>
            <Link to='/' className='text-primary hover:underline'>
              {t('Back to Home')}
            </Link>
          </p>
        </div>
      </div>
    )
  }

  return (
    <PublicLayout>
      <div className='mx-auto w-full max-w-5xl py-8'>{body}</div>
    </PublicLayout>
  )
}
