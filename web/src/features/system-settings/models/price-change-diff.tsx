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
import {
  AlertCircle,
  Loader2,
  TrendingDown,
  TrendingUp,
  Undo2,
} from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { combineBillingExpr } from '@/features/pricing/lib/billing-expr'
import { formatQuota } from '@/lib/format'

import { previewRatioDiff } from '../api'
import type {
  PricingSyncValues,
  RatioDiffPreviewRequest,
  RatioDiffPreviewResponse,
} from '../types'
import {
  getPriceChangeKind,
  hasPricingValue,
  type ModelPricingSnapshot,
  type ModelRow,
  type PriceChangeKind,
} from './model-pricing-snapshots'
import {
  getSyncExpressionPricing,
  getSyncPriceLines,
} from './upstream-ratio-sync-helpers'

type Translate = (key: string) => string

const CHANGE_LABELS = {
  added: 'Added',
  modified: 'Modified',
  deleted: 'Pending deletion',
} as const

// One readable pill for every kind, in the amber of the unsaved-changes pill.
export function PriceChangeBadge(props: { kind: PriceChangeKind }) {
  const { t } = useTranslation()
  return (
    <StatusBadge
      label={t(CHANGE_LABELS[props.kind])}
      copyable={false}
      className='shrink-0 bg-amber-500/10 px-2 text-amber-800 ring-1 ring-amber-500/25 ring-inset dark:text-amber-200'
    />
  )
}

const SYNC_FIELDS = [
  ['price', 'model_price'],
  ['ratio', 'model_ratio'],
  ['completionRatio', 'completion_ratio'],
  ['cacheRatio', 'cache_ratio'],
  ['createCacheRatio', 'create_cache_ratio'],
  ['imageRatio', 'image_ratio'],
  ['audioRatio', 'audio_ratio'],
  ['audioCompletionRatio', 'audio_completion_ratio'],
] as const

type PriceLine = { label: string; value: string }
type PriceGroup = { key: string; title?: string; lines: PriceLine[] }

// Expression tiers in the lines the upstream price sync shows. A provider's
// own expression is titled with its plugin key.
function getExpressionGroups(
  expression: string,
  t: Translate,
  provider = ''
): PriceGroup[] {
  const parsed = getSyncExpressionPricing(expression, t)
  if (!parsed || parsed.tiers.every((tier) => tier.lines.length === 0)) {
    return [
      {
        key: provider,
        title: provider || undefined,
        lines: [{ label: t('Expression pricing'), value: expression }],
      },
    ]
  }
  const groups = parsed.tiers.map((tier, index) => {
    const tierTitle =
      parsed.tiers.length > 1 ? tier.condition || tier.label : ''
    return {
      key: `${provider}:${tier.condition || tier.label || index}`,
      title: [provider, tierTitle].filter(Boolean).join(' · ') || undefined,
      lines: tier.lines,
    }
  })
  if (parsed.requestRuleExpr) {
    groups.push({
      key: `${provider}:rules`,
      title: provider || undefined,
      lines: [
        { label: t('Includes request rules'), value: parsed.requestRuleExpr },
      ],
    })
  }
  return groups
}

function getPriceGroups(
  snapshot: ModelPricingSnapshot | undefined,
  t: Translate
): PriceGroup[] {
  if (!snapshot) return []
  const groups: PriceGroup[] = []
  if (snapshot.billingMode === 'tiered_expr' && snapshot.billingExpr) {
    groups.push(
      ...getExpressionGroups(
        combineBillingExpr(
          snapshot.billingExpr,
          snapshot.requestRuleExpr ?? ''
        ),
        t
      )
    )
  } else {
    const values: PricingSyncValues = {}
    for (const [from, to] of SYNC_FIELDS) {
      if (hasPricingValue(snapshot[from])) values[to] = Number(snapshot[from])
    }
    const lines = getSyncPriceLines(values, t)
    if (lines.length > 0) groups.push({ key: '', lines })
  }
  const providers = Object.entries(snapshot.pluginBillingExpr ?? {}).sort(
    ([a], [b]) => a.localeCompare(b)
  )
  for (const [plugin, expression] of providers) {
    groups.push(...getExpressionGroups(expression, t, plugin))
  }
  return groups
}

const priceLineKey = (group: PriceGroup, line: PriceLine) =>
  `${group.key}\n${line.label}`

function getPriceLineValues(
  snapshot: ModelPricingSnapshot | undefined,
  t: Translate
) {
  return new Map(
    getPriceGroups(snapshot, t).flatMap((group) =>
      group.lines.map((line) => [priceLineKey(group, line), line.value])
    )
  )
}

/** The changed prices of a modified row as old → new, at most two. */
export function PriceChangeSummary(props: { row: ModelRow }) {
  const { t } = useTranslation()
  const before = getPriceLineValues(props.row.saved, t)
  const changes = getPriceGroups(props.row.draft, t).flatMap((group) =>
    group.lines.flatMap((line) => {
      const key = priceLineKey(group, line)
      const old = before.get(key)
      return old === line.value ? [] : [{ ...line, key, old }]
    })
  )
  if (changes.length === 0) return null
  const hidden = changes.length - 2
  // One change per line on phones, where the row actions cover the cell end.
  return (
    <ul
      aria-label={t('Unsaved price changes')}
      className='text-foreground flex min-w-0 flex-col items-start text-xs sm:flex-row sm:flex-wrap sm:gap-x-3'
    >
      {changes.slice(0, 2).map((change, index) => (
        <li key={change.key} className='max-w-full break-words'>
          {change.label}{' '}
          {change.old !== undefined && (
            <span className='tabular-nums'>{change.old} → </span>
          )}
          <span className='font-semibold tabular-nums'>{change.value}</span>
          {index === 1 && hidden > 0 && (
            <span> {t('+{{count}} more', { count: hidden })}</span>
          )}
        </li>
      ))}
    </ul>
  )
}

function PriceValues(props: {
  snapshot?: ModelPricingSnapshot
  compareTo?: ModelPricingSnapshot
}) {
  const { t } = useTranslation()
  const groups = getPriceGroups(props.snapshot, t)
  if (groups.length === 0) {
    return <span className='text-muted-foreground'>{t('Unset price')}</span>
  }
  const compared = props.compareTo
    ? getPriceLineValues(props.compareTo, t)
    : undefined
  return (
    <div className='space-y-2'>
      {groups.map((group) => (
        <div key={group.key} className='space-y-1'>
          {group.title && (
            <div className='text-muted-foreground text-xs'>{group.title}</div>
          )}
          <dl className='flex min-w-0 flex-wrap gap-x-4 gap-y-2'>
            {group.lines.map((line) => (
              <div key={line.label} className='min-w-0'>
                <dt className='text-muted-foreground text-xs leading-4'>
                  {line.label}
                </dt>
                <dd className='leading-5 font-medium break-all tabular-nums'>
                  {compared &&
                  compared.get(priceLineKey(group, line)) !== line.value ? (
                    <mark className='rounded-sm bg-amber-500/25 px-0.5 text-inherit'>
                      {line.value}
                    </mark>
                  ) : (
                    line.value
                  )}
                </dd>
              </div>
            ))}
          </dl>
        </div>
      ))}
    </div>
  )
}

function PriceAfterSaving(props: { row: ModelRow }) {
  const { t } = useTranslation()
  if (props.row.isDraftDeleted) {
    return <span className='text-foreground'>{t('Remove saved price')}</span>
  }
  return (
    <PriceValues
      snapshot={props.row.draft}
      compareTo={props.row.isDraftNew ? undefined : props.row.saved}
    />
  )
}

/** Saved price next to the draft, shown above the editor of a changed model. */
export function PriceChangeCard(props: { row: ModelRow; onUndo: () => void }) {
  const { t } = useTranslation()
  return (
    <section
      aria-label={t('Unsaved price changes')}
      className='bg-muted/20 rounded-lg border'
    >
      <div className='flex items-center justify-between gap-3 border-b px-3 py-2'>
        <div className='flex items-center gap-2 text-sm font-medium'>
          <span
            aria-hidden='true'
            className='size-1.5 rounded-full bg-amber-500'
          />
          {t('Unsaved price changes')}
        </div>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='-my-1'
          onClick={props.onUndo}
        >
          <Undo2 data-icon='inline-start' />
          {t('Undo changes')}
        </Button>
      </div>
      <div className='grid gap-4 px-3 py-3 sm:grid-cols-2'>
        <div className='min-w-0 space-y-2'>
          <div className='text-muted-foreground text-xs'>
            {t('Saved price')}
          </div>
          <PriceValues snapshot={props.row.saved} />
        </div>
        <div className='min-w-0 space-y-2'>
          <div className='text-muted-foreground text-xs'>
            {t('Price after saving')}
          </div>
          <PriceAfterSaving row={props.row} />
        </div>
      </div>
    </section>
  )
}

/** The ratio maps the backend needs to estimate the impact of this save. */
function collectRatioChanges(
  rows: ModelRow[],
  field: 'ratio' | 'completionRatio'
): Record<string, number> {
  const changes: Record<string, number> = {}
  for (const row of rows) {
    const before = row.saved?.[field] ?? ''
    const after = row.draft?.[field] ?? ''
    if (before === after) continue
    // A cleared ratio means the model is billed at zero from now on.
    const next = hasPricingValue(after) ? Number(after) : 0
    changes[row.name] = Number.isFinite(next) ? next : 0
  }
  return changes
}

const RATIO_TYPE_LABELS = {
  model: 'Model ratio',
  completion: 'Completion ratio',
  group: 'Group ratios',
} as const

/**
 * v1.1.0 M1 · B22：保存前的倍率影响面预览。后端按近 7 天用量线性外推
 * 新旧倍率差额，这里只做展示，失败不影响保存流程。
 */
function RatioDiffEstimate(props: {
  modelChanges: Record<string, number>
  completionChanges: Record<string, number>
}) {
  const { t } = useTranslation()
  const requests = useMemo(() => {
    const list: RatioDiffPreviewRequest[] = []
    if (Object.keys(props.modelChanges).length > 0) {
      list.push({ ratio_type: 'model', new_ratio: props.modelChanges })
    }
    if (Object.keys(props.completionChanges).length > 0) {
      list.push({
        ratio_type: 'completion',
        new_ratio: props.completionChanges,
      })
    }
    return list
  }, [props.modelChanges, props.completionChanges])

  const previewQuery = useQuery({
    queryKey: ['ratio-diff-preview', requests],
    staleTime: 60 * 1000,
    queryFn: async () => {
      const responses = await Promise.all(
        requests.map((request) => previewRatioDiff(request))
      )
      for (const response of responses) {
        if (!response.success) {
          throw new Error(response.message || 'Preview request failed')
        }
      }
      return responses.map(
        (response: RatioDiffPreviewResponse) => response.data
      )
    },
  })

  if (previewQuery.isLoading) {
    return (
      <div className='bg-muted/20 flex items-center gap-2 rounded-lg border px-3 py-2 text-xs'>
        <Loader2 className='size-3.5 animate-spin' aria-hidden='true' />
        {t('Ratio Change Preview')}
      </div>
    )
  }
  if (previewQuery.isError) {
    return (
      <div className='text-destructive border-destructive/30 flex items-start gap-2 rounded-lg border px-3 py-2 text-xs'>
        <AlertCircle className='mt-0.5 size-3.5 shrink-0' aria-hidden='true' />
        <span className='min-w-0 break-words'>
          {t('Ratio Change Preview')}: {previewQuery.error.message}
        </span>
      </div>
    )
  }
  const previews = previewQuery.data ?? []
  if (previews.length === 0) return null
  return (
    <section
      aria-label={t('Ratio Change Preview')}
      className='bg-muted/20 space-y-3 rounded-lg border px-3 py-3'
    >
      <div className='flex items-center gap-2 text-sm font-medium'>
        <span
          aria-hidden='true'
          className='size-1.5 rounded-full bg-amber-500'
        />
        {t('Ratio Change Preview')}
      </div>
      {previews.map((preview) => {
        const delta = preview.delta_quota
        const TrendIcon = delta < 0 ? TrendingDown : TrendingUp
        return (
          <div key={preview.ratio_type} className='space-y-1'>
            <p className='text-muted-foreground text-xs leading-5'>
              {t(RATIO_TYPE_LABELS[preview.ratio_type])}
              {' · '}
              {t(
                'This change affects {{models}} of {{total}} models. Estimated 7-day impact:',
                {
                  models: preview.changed_models,
                  total: preview.total_entries,
                }
              )}
            </p>
            <div className='flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-sm'>
              <span className='text-muted-foreground text-xs'>
                {t('Estimated revenue change')}
              </span>
              <span
                className={`font-semibold tabular-nums ${
                  delta < 0
                    ? 'text-destructive'
                    : 'text-emerald-600 dark:text-emerald-400'
                }`}
              >
                <TrendIcon
                  className='mr-1 inline size-3.5 align-[-2px]'
                  aria-hidden='true'
                />
                {delta > 0 && '+'}
                {formatQuota(delta)}
              </span>
            </div>
          </div>
        )
      })}
    </section>
  )
}

export function PriceChangesDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  rows: ModelRow[]
  onConfirm: () => void
  isLoading?: boolean
}) {
  const { t } = useTranslation()
  const modelRatioChanges = useMemo(
    () => collectRatioChanges(props.rows, 'ratio'),
    [props.rows]
  )
  const completionRatioChanges = useMemo(
    () => collectRatioChanges(props.rows, 'completionRatio'),
    [props.rows]
  )
  const hasRatioChanges =
    Object.keys(modelRatioChanges).length > 0 ||
    Object.keys(completionRatioChanges).length > 0
  return (
    <ConfirmDialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Confirm price changes')}
      desc={t(
        'New prices apply to requests as soon as you save. {{count}} models change.',
        { count: props.rows.length }
      )}
      className='max-h-[min(90dvh,var(--dialog-available-height))] overflow-y-auto data-[size=default]:max-w-[calc(100%-2rem)] data-[size=default]:sm:max-w-4xl'
      confirmText={t('Confirm and save')}
      isLoading={props.isLoading}
      handleConfirm={props.onConfirm}
    >
      {hasRatioChanges && (
        <RatioDiffEstimate
          modelChanges={modelRatioChanges}
          completionChanges={completionRatioChanges}
        />
      )}
      <ul className='max-h-[60dvh] divide-y overflow-y-auto rounded-lg border sm:hidden'>
        {props.rows.map((row) => {
          const kind = getPriceChangeKind(row)
          return (
            <li key={row.name} className='space-y-3 p-3'>
              <div className='flex min-w-0 items-center gap-2'>
                <span className='min-w-0 truncate font-medium'>{row.name}</span>
                {kind && <PriceChangeBadge kind={kind} />}
              </div>
              <div className='space-y-1'>
                <div className='text-muted-foreground text-xs'>
                  {t('Current Price')}
                </div>
                <PriceValues snapshot={row.saved} />
              </div>
              <div className='space-y-1'>
                <div className='text-muted-foreground text-xs'>
                  {t('Price after saving')}
                </div>
                <PriceAfterSaving row={row} />
              </div>
            </li>
          )
        })}
      </ul>
      <StaticDataTable
        className='hidden max-h-[60dvh] overflow-y-auto sm:block'
        tableClassName='min-w-[640px]'
        data={props.rows}
        getRowKey={(row) => row.name}
        columns={[
          {
            id: 'model',
            header: t('Model'),
            className: 'w-56',
            cellClassName: 'align-top',
            cell: (row) => {
              const kind = getPriceChangeKind(row)
              return (
                <div className='flex min-w-0 flex-col items-start gap-1.5'>
                  <span className='max-w-full truncate font-medium'>
                    {row.name}
                  </span>
                  {kind && <PriceChangeBadge kind={kind} />}
                </div>
              )
            },
          },
          {
            id: 'saved',
            header: t('Current Price'),
            cellClassName: 'align-top',
            cell: (row) => <PriceValues snapshot={row.saved} />,
          },
          {
            id: 'draft',
            header: t('Price after saving'),
            cellClassName: 'align-top',
            cell: (row) => <PriceAfterSaving row={row} />,
          },
        ]}
      />
    </ConfirmDialog>
  )
}
