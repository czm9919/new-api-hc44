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
 * 加速专线（/accel，hc48 / hc49）。
 *
 * 页面五块：
 *  1. 状态卡：未开通 / 生效中（剩余天数、进度条、到期提醒）、本期与今日用量
 *  2. 套餐卡：美元报价、并发、开通 / 换套餐（换套餐会作废剩余时长，需二次确认）
 *  3. 使用指引：分组名怎么填到令牌上（复制）
 *  4. 管理端：套餐 CRUD（美元输入）
 *  5. 管理端：订阅列表（补时长 / 强制停用）
 *
 * 价格一律用美元：后端返回 price_usd / total_charged_usd / paid_balance_usd，
 * 额度字段只用于内部计费，前端不再自己换算。
 */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  AlertTriangle,
  Check,
  Copy,
  Gauge,
  Rocket,
  ShieldCheck,
  Trash2,
  TrendingUp,
  Zap,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { useIsAdmin } from '@/hooks/use-admin'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { formatCurrencyUSD, formatTimestampToDate } from '@/lib/format'

import {
  createAccelPlan,
  deleteAccelPlan,
  extendAccelSub,
  getAccelLines,
  getAccelOverview,
  getAccelIngressStats,
  listAccelLines,
  listAccelPlans,
  listAccelSubs,
  revokeAccelSub,
  setAccelAutoRenew,
  subscribeAccel,
  updateAccelPlan,
  type AccelLimits,
  type AccelLine,
  type AccelLinesResponse,
  type AccelPlan,
  type AccelPlanInput,
  type AccelSubscription,
  type AccelSubRow,
  type AccelUsage,
} from './api'
import { IngressCard } from './ingress-card'

const DEFAULT_LIMITS: AccelLimits = {
  max_price_usd: 200,
  price_step_usd: 0.01,
  max_concurrency: 1000,
  max_extend_days: 3650,
}

const EMPTY_FORM: AccelPlanInput = {
  name: '',
  group_name: '',
  price_usd: 0,
  concurrency: 0,
  description: '',
  enabled: true,
  sort_order: 0,
}

/** 剩余天数占比，用于进度条；延长过时长的订阅可能超过 100%，夹紧到 100。 */
function remainingPercent(subscription: AccelSubscription): number {
  const now = Date.now() / 1000
  const total = subscription.expire_at - subscription.start_at
  if (total <= 0) {
    return 0
  }
  const left = Math.max(0, subscription.expire_at - now)
  return Math.min(100, Math.max(0, (left / total) * 100))
}

function daysLeftFromExpire(expireAt: number): number {
  const now = Date.now() / 1000
  if (expireAt <= now) {
    return 0
  }
  return Math.ceil((expireAt - now) / 86400)
}

function GroupChip({ group }: { group: string }) {
  const { copyToClipboard } = useCopyToClipboard()
  return (
    <button
      type='button'
      onClick={() => void copyToClipboard(group)}
      className='bg-muted/60 hover:bg-muted inline-flex items-center gap-1.5 rounded-md px-2 py-1 font-mono text-xs transition-colors'
    >
      {group}
      <Copy className='size-3 opacity-60' />
    </button>
  )
}

function StatCell({ label, value }: { label: string; value: string }) {
  return (
    <div className='rounded-md border px-3 py-2'>
      <div className='text-muted-foreground text-xs'>{label}</div>
      <div className='mt-0.5 text-sm font-medium tabular-nums'>{value}</div>
    </div>
  )
}

function UsageGrid({
  usage,
  todayUsage,
}: {
  usage: AccelUsage | null
  todayUsage: AccelUsage | null
}) {
  const { t } = useTranslation()
  const period = usage
  const today = todayUsage
  return (
    <div className='grid grid-cols-2 gap-2 sm:grid-cols-4'>
      <StatCell
        label={t('Requests this period')}
        value={String(period?.requests ?? 0)}
      />
      <StatCell
        label={t('Spend this period')}
        value={formatCurrencyUSD(period?.quota_usd ?? 0)}
      />
      <StatCell
        label={t('Requests today')}
        value={String(today?.requests ?? 0)}
      />
      <StatCell
        label={t('Spend today')}
        value={formatCurrencyUSD(today?.quota_usd ?? 0)}
      />
    </div>
  )
}

function ActiveSubscriptionCard({
  subscription,
  paidBalanceUSD,
  usage,
  todayUsage,
}: {
  subscription: AccelSubscription
  paidBalanceUSD: number
  usage: AccelUsage | null
  todayUsage: AccelUsage | null
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const mutation = useMutation({
    mutationFn: setAccelAutoRenew,
    onSuccess: () => {
      toast.success(t('Auto renew updated'))
      void queryClient.invalidateQueries({ queryKey: ['accel'] })
    },
    onError: () => {
      toast.error(t('Failed to update auto renew'))
    },
  })

  const expired = !subscription.active
  const expiringSoon = !expired && subscription.days_left <= 3
  return (
    <Card className={expired ? 'border-destructive/40' : 'border-primary/30'}>
      <CardHeader className='gap-1'>
        <div className='flex flex-wrap items-center gap-2'>
          <CardTitle className='text-base'>
            {subscription.plan_name || subscription.group_name}
          </CardTitle>
          {expired ? (
            <Badge variant='destructive'>{t('Expired')}</Badge>
          ) : (
            <Badge>{t('{{days}} days left', { days: subscription.days_left })}</Badge>
          )}
          {subscription.group_name ? <GroupChip group={subscription.group_name} /> : null}
        </div>
        <CardDescription>
          {t('Expires at {{time}}', {
            time: formatTimestampToDate(subscription.expire_at),
          })}
          {' · '}
          {formatCurrencyUSD(subscription.price_usd)} {'/ 30 '}
          {t('days')}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-3'>
        {expired ? null : (
          <div className='space-y-1'>
            <Progress value={remainingPercent(subscription)} className='w-full' />
            <div className='text-muted-foreground flex justify-between text-xs'>
              <span>
                {t('Started at {{time}}', {
                  time: formatTimestampToDate(subscription.start_at),
                })}
              </span>
              <span>
                {t('Paid balance')}: {formatCurrencyUSD(paidBalanceUSD)}
              </span>
            </div>
          </div>
        )}

        {expiringSoon ? (
          <div className='flex items-start gap-2 rounded-md border border-amber-400/60 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:bg-amber-950/30 dark:text-amber-200'>
            <AlertTriangle className='mt-0.5 size-3.5 shrink-0' />
            <span>
              {subscription.auto_renew
                ? t(
                    'Expires in {{days}} days. Auto renew will charge {{amount}} from your paid balance.',
                    {
                      days: subscription.days_left,
                      amount: formatCurrencyUSD(subscription.price_usd),
                    }
                  )
                : t(
                    'Expires in {{days}} days and auto renew is off. The line stops when it expires.',
                    { days: subscription.days_left }
                  )}
            </span>
          </div>
        ) : null}

        {subscription.last_charge_error ? (
          <p className='text-destructive text-sm'>
            {subscription.last_charge_error}
          </p>
        ) : null}

        <UsageGrid usage={usage} todayUsage={todayUsage} />

        <div className='flex items-center justify-between rounded-md border px-3 py-2'>
          <div className='space-y-0.5'>
            <div className='text-sm font-medium'>{t('Auto renew')}</div>
            <div className='text-muted-foreground text-xs'>
              {t('Turn off to stop charging; access stays until expiry')}
            </div>
          </div>
          <Switch
            checked={subscription.auto_renew}
            disabled={mutation.isPending}
            onCheckedChange={(checked) => mutation.mutate(!!checked)}
          />
        </div>

        <div className='text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-xs'>
          <span>
            {t('Total charged')}: {formatCurrencyUSD(subscription.total_charged_usd)}
          </span>
          <span>
            {t('Monthly fee')}: {formatCurrencyUSD(subscription.price_usd)}
          </span>
        </div>
      </CardContent>
    </Card>
  )
}

function EmptySubscriptionCard({
  paidBalanceUSD,
  userGroup,
}: {
  paidBalanceUSD: number
  userGroup: string
}) {
  const { t } = useTranslation()
  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <ShieldCheck className='size-4' />
          {t('Not subscribed')}
        </CardTitle>
        <CardDescription>
          {t(
            'Pick a plan below to unlock the dedicated line. You currently use the {{group}} group.',
            { group: userGroup || 'default' }
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className='text-muted-foreground text-sm'>
          {t('Paid balance')}:{' '}
          <span className='text-foreground font-medium'>
            {formatCurrencyUSD(paidBalanceUSD)}
          </span>{' '}
          · {t('Monthly fee is charged from paid balance only')}
        </div>
      </CardContent>
    </Card>
  )
}

function PlanCard({
  plan,
  paidBalanceUSD,
  active,
  switching,
  pending,
  onSubscribe,
}: {
  plan: AccelPlan
  paidBalanceUSD: number
  active: boolean
  switching: boolean
  pending: boolean
  onSubscribe: (plan: AccelPlan) => void
}) {
  const { t } = useTranslation()
  const affordable = paidBalanceUSD + 1e-9 >= plan.price_usd
  const disabled = pending || !affordable || (active && !switching)
  let actionLabel = t('Subscribe')
  if (active) {
    actionLabel = t('Current plan')
  } else if (switching) {
    actionLabel = t('Switch to this plan')
  }
  return (
    <Card className={active ? 'border-primary/40 flex flex-col' : 'flex flex-col'}>
      <CardHeader className='gap-1'>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Zap className='size-4' />
          {plan.name}
        </CardTitle>
        <CardDescription>{plan.description}</CardDescription>
      </CardHeader>
      <CardContent className='flex flex-1 flex-col justify-between gap-3'>
        <div className='text-2xl font-medium'>
          {formatCurrencyUSD(plan.price_usd)}
          <span className='text-muted-foreground ml-1 text-xs font-normal'>
            / 30 {t('days')}
          </span>
        </div>
        <ul className='text-muted-foreground space-y-1 text-sm'>
          <li className='flex items-center gap-2'>
            <Gauge className='size-3.5' />
            {plan.concurrency > 0
              ? t('{{count}} concurrent requests', { count: plan.concurrency })
              : t('Unlimited concurrency')}
          </li>
          {plan.group_name ? (
            <li className='flex items-center gap-2'>
              <Rocket className='size-3.5' />
              {t('Group')}: <GroupChip group={plan.group_name} />
            </li>
          ) : null}
        </ul>
        <Button
          className='w-full'
          disabled={disabled}
          variant={active ? 'secondary' : 'default'}
          onClick={() => onSubscribe(plan)}
        >
          {actionLabel}
        </Button>
        {!affordable && !active ? (
          <p className='text-muted-foreground text-xs'>
            {t('Top up your paid balance to subscribe')}
          </p>
        ) : null}
        {switching && !active ? (
          <p className='text-muted-foreground text-xs'>
            {t('Switching charges the full monthly fee and resets the 30-day period')}
          </p>
        ) : null}
      </CardContent>
    </Card>
  )
}

function PlanFormDialog({
  open,
  onOpenChange,
  plan,
  limits,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  plan: AccelPlan | null
  limits: AccelLimits
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // 父组件用 key 控制重挂载，这里按初始 props 建一次 state 即可，
  // 避免在渲染期间 setState 触发额外渲染。
  const [form, setForm] = useState<AccelPlanInput>(() =>
    plan
      ? {
          name: plan.name,
          group_name: plan.group_name,
          price_usd: plan.price_usd,
          concurrency: plan.concurrency,
          description: plan.description,
          enabled: plan.enabled,
          sort_order: plan.sort_order,
        }
      : { ...EMPTY_FORM }
  )

  const mutation = useMutation({
    mutationFn: async () => {
      if (plan) {
        return updateAccelPlan(plan.id, form)
      }
      return createAccelPlan(form)
    },
    onSuccess: () => {
      toast.success(t('Saved'))
      void queryClient.invalidateQueries({ queryKey: ['accel-admin-plans'] })
      void queryClient.invalidateQueries({ queryKey: ['accel'] })
      onOpenChange(false)
    },
    onError: (error: unknown) => {
      const message = error instanceof Error ? error.message : String(error)
      toast.error(message)
    },
  })

  const patch = (next: Partial<AccelPlanInput>) => {
    setForm((prev) => ({ ...prev, ...next }))
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{plan ? t('Edit plan') : t('New plan')}</DialogTitle>
          <DialogDescription>
            {t(
              'The group must exist in group ratios and must not be listed in user usable groups.'
            )}
          </DialogDescription>
        </DialogHeader>
        <div className='grid gap-3'>
          <div className='grid gap-1.5'>
            <Label>{t('Name')}</Label>
            <Input
              value={form.name}
              onChange={(event) => patch({ name: event.target.value })}
            />
          </div>
          <div className='grid gap-1.5'>
            <Label>{t('Group')}</Label>
            <Input
              value={form.group_name}
              onChange={(event) => patch({ group_name: event.target.value })}
              placeholder={t('Leave empty = same as the main site')}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Optional. Leave empty and the accelerated link dispatches exactly like the main site.'
              )}
            </p>
          </div>
          <div className='grid grid-cols-2 gap-3'>
            <div className='grid gap-1.5'>
              <Label>{t('Monthly fee (USD)')}</Label>
              <Input
                type='number'
                min={0}
                max={limits.max_price_usd}
                step={limits.price_step_usd}
                value={form.price_usd}
                onChange={(event) =>
                  patch({ price_usd: Number(event.target.value) || 0 })
                }
              />
            </div>
            <div className='grid gap-1.5'>
              <Label>{t('Concurrency (0 = unlimited)')}</Label>
              <Input
                type='number'
                min={0}
                max={limits.max_concurrency}
                value={form.concurrency}
                onChange={(event) =>
                  patch({ concurrency: Number(event.target.value) || 0 })
                }
              />
            </div>
          </div>
          <div className='grid gap-1.5'>
            <Label>{t('Description')}</Label>
            <Input
              value={form.description}
              onChange={(event) => patch({ description: event.target.value })}
            />
          </div>
          <div className='flex items-center justify-between rounded-md border px-3 py-2'>
            <Label>{t('Enabled')}</Label>
            <Switch
              checked={form.enabled}
              onCheckedChange={(checked) => patch({ enabled: !!checked })}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            disabled={mutation.isPending || !form.name}
            onClick={() => mutation.mutate()}
          >
            {t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function AdminPlansSection({ limits }: { limits: AccelLimits }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<AccelPlan | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [linesGroup, setLinesGroup] = useState<string | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['accel-admin-plans'],
    queryFn: listAccelPlans,
  })

  const toggleMutation = useMutation({
    mutationFn: (plan: AccelPlan) =>
      updateAccelPlan(plan.id, {
        name: plan.name,
        group_name: plan.group_name,
        price_usd: plan.price_usd,
        concurrency: plan.concurrency,
        description: plan.description,
        enabled: !plan.enabled,
        sort_order: plan.sort_order,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['accel-admin-plans'] })
      void queryClient.invalidateQueries({ queryKey: ['accel'] })
    },
  })

  const removeMutation = useMutation({
    mutationFn: deleteAccelPlan,
    onSuccess: () => {
      toast.success(t('Deleted'))
      void queryClient.invalidateQueries({ queryKey: ['accel-admin-plans'] })
      void queryClient.invalidateQueries({ queryKey: ['accel'] })
    },
  })

  return (
    <Card>
      <CardHeader className='flex flex-row items-start justify-between gap-3'>
        <div className='space-y-1'>
          <CardTitle className='text-base'>{t('Plan management')}</CardTitle>
          <CardDescription>
            {t('Plans define the group, monthly fee and concurrency limit.')}
          </CardDescription>
        </div>
        <Button
          size='sm'
          onClick={() => {
            setEditing(null)
            setDialogOpen(true)
          }}
        >
          {t('New plan')}
        </Button>
      </CardHeader>
      <CardContent className='space-y-2'>
        {isLoading ? <Skeleton className='h-20 w-full' /> : null}
        {(data ?? []).map((plan) => (
          <div
            key={plan.id}
            className='flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2'
          >
            <div className='min-w-0 space-y-0.5'>
              <div className='flex items-center gap-2 text-sm font-medium'>
                {plan.name}
                <Badge variant='outline'>{plan.group_name}</Badge>
                {plan.enabled ? null : (
                  <Badge variant='secondary'>{t('Disabled')}</Badge>
                )}
              </div>
              <div className='text-muted-foreground text-xs'>
                {formatCurrencyUSD(plan.price_usd)} / 30 {t('days')}
                {' · '}
                {plan.concurrency > 0
                  ? t('{{count}} concurrent requests', {
                      count: plan.concurrency,
                    })
                  : t('Unlimited concurrency')}
              </div>
            </div>
            <div className='flex items-center gap-2'>
              <Switch
                checked={plan.enabled}
                onCheckedChange={() => toggleMutation.mutate(plan)}
              />
              <Button
                size='sm'
                variant='outline'
                onClick={() => setLinesGroup(plan.group_name)}
              >
                {t('Lines')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                onClick={() => {
                  setEditing(plan)
                  setDialogOpen(true)
                }}
              >
                {t('Edit')}
              </Button>
              <AlertDialog>
                <AlertDialogTrigger
                  render={
                    <Button
                      size='sm'
                      variant='ghost'
                      aria-label={t('Delete')}
                    />
                  }
                >
                  <Trash2 className='text-destructive size-4' />
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>{t('Delete this plan?')}</AlertDialogTitle>
                    <AlertDialogDescription>
                      {t(
                        'Existing subscriptions keep working until they expire, but nobody can buy it again.'
                      )}
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
                    <AlertDialogAction onClick={() => removeMutation.mutate(plan.id)}>
                      {t('Delete')}
                    </AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            </div>
          </div>
        ))}
        {!isLoading && (data ?? []).length === 0 ? (
          <p className='text-muted-foreground text-sm'>{t('No plans yet')}</p>
        ) : null}
      </CardContent>
      <PlanFormDialog
        key={editing ? `edit-${editing.id}` : 'new'}
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        plan={editing}
        limits={limits}
      />
      <AdminLinesDialog
        group={linesGroup}
        onClose={() => setLinesGroup(null)}
      />
    </Card>
  )
}

function AdminSubsSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [revoking, setRevoking] = useState<AccelSubRow | null>(null)
  const [reason, setReason] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['accel-admin-subs', page],
    queryFn: () => listAccelSubs(page, 20),
  })

  // 守卫计数（hc52）：拦截数突增 ≈ 域名/前缀被传播出去被人试了。
  const { data: ingressStats } = useQuery({
    queryKey: ['accel-ingress-stats'],
    queryFn: getAccelIngressStats,
    refetchInterval: 60000,
  })

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['accel-admin-subs'] })
    void queryClient.invalidateQueries({ queryKey: ['accel'] })
  }

  const extendMutation = useMutation({
    mutationFn: (userId: number) => extendAccelSub(userId, 30),
    onSuccess: () => {
      toast.success(t('30 days added'))
      refresh()
    },
    onError: (error: unknown) => {
      toast.error(error instanceof Error ? error.message : String(error))
    },
  })

  const revokeMutation = useMutation({
    mutationFn: (userId: number) => revokeAccelSub(userId, reason),
    onSuccess: () => {
      toast.success(t('Subscription revoked'))
      setRevoking(null)
      setReason('')
      refresh()
    },
    onError: (error: unknown) => {
      toast.error(error instanceof Error ? error.message : String(error))
    },
  })

  const total = data?.total ?? 0
  const pageSize = data?.page_size || 20
  const pageCount = Math.max(1, Math.ceil(total / pageSize))
  const now = Date.now() / 1000

  return (
    <Card>
      <CardHeader className='flex flex-row items-start justify-between gap-3'>
        <div className='space-y-1'>
          <CardTitle className='flex items-center gap-2 text-base'>
            <TrendingUp className='size-4' />
            {t('Subscriptions')}
          </CardTitle>
          <CardDescription>
            {t(
              'Extend for support cases; revoke stops access immediately and does not refund.'
            )}
          </CardDescription>
        </div>
        <div className='flex flex-wrap items-center gap-2'>
          {ingressStats && ingressStats.ingress_host ? (
            <Badge variant='outline' title={ingressStats.ingress_host}>
              {t('Guard: {{allowed}} allowed / {{denied}} denied', {
                allowed: ingressStats.allowed,
                denied: ingressStats.denied,
              })}
            </Badge>
          ) : null}
          <Badge variant='outline'>
            {t('{{count}} total', { count: total })}
          </Badge>
        </div>
      </CardHeader>
      <CardContent className='space-y-2'>
        {isLoading ? <Skeleton className='h-20 w-full' /> : null}
        {!isLoading && (data?.items ?? []).length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {t('Nobody has subscribed yet')}
          </p>
        ) : null}
        {(data?.items ?? []).map((row) => {
          const active = row.status === 'active' && row.expire_at > now
          const days = daysLeftFromExpire(row.expire_at)
          return (
            <div
              key={row.user_id}
              className='flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2'
            >
              <div className='min-w-0 space-y-0.5'>
                <div className='flex flex-wrap items-center gap-2 text-sm font-medium'>
                  <span className='truncate'>{row.username || `#${row.user_id}`}</span>
                  <Badge variant='outline'>{row.plan_name || row.group_name}</Badge>
                  {active ? (
                    <Badge>{t('{{days}} days left', { days })}</Badge>
                  ) : (
                    <Badge variant='destructive'>{t('Expired')}</Badge>
                  )}
                  {row.auto_renew ? null : (
                    <Badge variant='secondary'>{t('Auto renew off')}</Badge>
                  )}
                </div>
                <div className='text-muted-foreground text-xs'>
                  {t('Expires at {{time}}', {
                    time: formatTimestampToDate(row.expire_at),
                  })}
                  {' · '}
                  {formatCurrencyUSD(row.price_usd)} / 30 {t('days')}
                  {' · '}
                  {t('Total charged')}: {formatCurrencyUSD(row.total_charged_usd)}
                </div>
                {row.last_charge_error ? (
                  <div className='text-destructive text-xs'>
                    {row.last_charge_error}
                  </div>
                ) : null}
              </div>
              <div className='flex items-center gap-2'>
                <Button
                  size='sm'
                  variant='outline'
                  disabled={extendMutation.isPending}
                  onClick={() => extendMutation.mutate(row.user_id)}
                >
                  +30 {t('days')}
                </Button>
                <Button
                  size='sm'
                  variant='ghost'
                  disabled={!active || revokeMutation.isPending}
                  onClick={() => {
                    setRevoking(row)
                    setReason('')
                  }}
                >
                  {t('Revoke')}
                </Button>
              </div>
            </div>
          )
        })}
        {pageCount > 1 ? (
          <div className='flex items-center justify-between pt-1'>
            <span className='text-muted-foreground text-xs'>
              {page} / {pageCount}
            </span>
            <div className='flex gap-2'>
              <Button
                size='sm'
                variant='outline'
                disabled={page <= 1}
                onClick={() => setPage((prev) => Math.max(1, prev - 1))}
              >
                {t('Previous')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                disabled={page >= pageCount}
                onClick={() => setPage((prev) => prev + 1)}
              >
                {t('Next')}
              </Button>
            </div>
          </div>
        ) : null}
      </CardContent>

      <Dialog
        open={revoking !== null}
        onOpenChange={(open) => {
          if (!open) {
            setRevoking(null)
          }
        }}
      >
        <DialogContent className='sm:max-w-md'>
          <DialogHeader>
            <DialogTitle>{t('Revoke this subscription?')}</DialogTitle>
            <DialogDescription>
              {t(
                'Access stops immediately. No refund is issued automatically; handle the money manually.'
              )}
            </DialogDescription>
          </DialogHeader>
          <div className='grid gap-1.5'>
            <Label>{t('Reason (shown to the user)')}</Label>
            <Input
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              placeholder={t('Revoked by admin')}
            />
          </div>
          <DialogFooter>
            <Button variant='outline' onClick={() => setRevoking(null)}>
              {t('Cancel')}
            </Button>
            <Button
              variant='destructive'
              disabled={revokeMutation.isPending}
              onClick={() => {
                if (revoking) {
                  revokeMutation.mutate(revoking.user_id)
                }
              }}
            >
              {t('Revoke')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  )
}

function UsageGuide({ group }: { group: string }) {
  const { t } = useTranslation()
  const steps = [
    t('Create a dedicated token and set its group to the plan group'),
    t('Or pass the group in the request body when calling the API'),
    t('Keep the token on the acceleration group, other tokens stay on the normal line'),
  ]
  return (
    <Card>
      <CardHeader className='gap-1'>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Check className='size-4' />
          {t('How to use')}
        </CardTitle>
        <CardDescription>
          {t('The plan unlocks a group; you still choose it per token.')}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-2'>
        <ol className='text-muted-foreground list-decimal space-y-1 pl-5 text-sm'>
          {steps.map((step) => (
            <li key={step}>{step}</li>
          ))}
        </ol>
        <div className='flex items-center gap-2 text-sm'>
          {t('Group')}: <GroupChip group={group} />
        </div>
      </CardContent>
    </Card>
  )
}

// ---------------------------------------------------------------- 线路状态
//
// 「线路」就是加速分组下的出口渠道。这里只展示名称/状态/健康度，
// 不含出口地址与密钥 —— 用户能判断线路好坏，但拿不到可滥用的信息。
function lineStatusLabel(
  line: AccelLine,
  t: (key: string) => string
): string {
  if (line.status !== 1) {
    return line.status_text === 'auto_disabled'
      ? t('Auto disabled')
      : t('Offline')
  }
  if (line.cooling_down) {
    return t('Cooling down')
  }
  if (line.degraded) {
    return t('Degraded')
  }
  return t('Online')
}

function lineStatusVariant(
  line: AccelLine
): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (line.status !== 1 || line.degraded) {
    return 'destructive'
  }
  if (line.cooling_down) {
    return 'secondary'
  }
  return 'outline'
}

function LineRow({ line }: { line: AccelLine }) {
  const { t } = useTranslation()
  const successText = line.has_samples
    ? `${Math.round(line.success_rate * 100)}%`
    : '—'
  const latencies = [
    line.ttft_ms > 0
      ? `${t('First token')} ${Math.round(line.ttft_ms)}ms`
      : null,
    line.latency_ms > 0 ? `${Math.round(line.latency_ms)}ms` : null,
  ].filter(Boolean)
  return (
    <div className='flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2'>
      <div className='min-w-0 space-y-0.5'>
        <div className='flex items-center gap-2 text-sm font-medium'>
          <span className='truncate'>{line.name || `#${line.channel_id}`}</span>
          <Badge variant={lineStatusVariant(line)}>
            {lineStatusLabel(line, t)}
          </Badge>
        </div>
        <div className='text-muted-foreground text-xs'>
          {t('Success rate')}: {successText}
          {latencies.length > 0 ? ` · ${latencies.join(' · ')}` : ''}
          {line.active > 0
            ? ` · ${t('{{count}} in flight', { count: line.active })}`
            : ''}
        </div>
      </div>
      {line.models > 0 ? (
        <span className='text-muted-foreground text-xs'>
          {t('{{count}} models', { count: line.models })}
        </span>
      ) : null}
    </div>
  )
}

function LineList({ data }: { data: AccelLinesResponse }) {
  const { t } = useTranslation()
  const { summary, lines } = data
  if (lines.length === 0) {
    return (
      <p className='text-muted-foreground text-sm'>
        {t('No line is bound to this group yet.')}
      </p>
    )
  }
  return (
    <div className='space-y-2'>
      {summary.all_down ? (
        <div className='border-destructive/50 bg-destructive/5 text-destructive flex items-start gap-2 rounded-md border px-3 py-2 text-sm'>
          <AlertTriangle className='mt-0.5 size-4 shrink-0' />
          <span>
            {t(
              'Every line under this group is unavailable right now. Please retry later.'
            )}
          </span>
        </div>
      ) : (
        <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
          <span>
            {t('Available')}: {summary.available}/{summary.total}
          </span>
          {summary.degraded > 0 ? (
            <span className='text-destructive'>
              {t('Degraded')}: {summary.degraded}
            </span>
          ) : null}
          {summary.cooling_down > 0 ? (
            <span>
              {t('Cooling down')}: {summary.cooling_down}
            </span>
          ) : null}
        </div>
      )}
      {lines.map((line) => (
        <LineRow key={line.channel_id} line={line} />
      ))}
    </div>
  )
}

function LinesSection({ group }: { group: string }) {
  const { t } = useTranslation()
  const { data, isLoading } = useQuery({
    queryKey: ['accel-lines', group],
    queryFn: getAccelLines,
    refetchInterval: 60000,
  })
  return (
    <Card>
      <CardHeader className='gap-1'>
        <CardTitle className='flex items-center gap-2 text-base'>
          <TrendingUp className='size-4' />
          {t('Line status')}
        </CardTitle>
        <CardDescription>
          {t(
            'Live health of the exits under your group. Success rate and latency are measured on this node.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {isLoading ? <Skeleton className='h-16 w-full' /> : null}
        {!isLoading && data && !data.visible ? (
          <p className='text-muted-foreground text-sm'>
            {t('Line status is hidden by the operator.')}
          </p>
        ) : null}
        {!isLoading && data && data.visible ? <LineList data={data} /> : null}
      </CardContent>
    </Card>
  )
}

// 管理端：按分组查全部线路（含已禁用），用于排查「用户说加速很慢」。
function AdminLinesDialog({
  group,
  onClose,
}: {
  group: string | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  const { data, isLoading } = useQuery({
    queryKey: ['accel-admin-lines', group],
    queryFn: () => listAccelLines(group ?? ''),
    enabled: group !== null,
  })
  const memoryBackend = data?.slot_backend === 'memory'
  return (
    <Dialog
      open={group !== null}
      onOpenChange={(open) => {
        if (!open) {
          onClose()
        }
      }}
    >
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>
            {t('Lines in group {{group}}', { group: group ?? '' })}
          </DialogTitle>
          <DialogDescription>
            {t(
              'Channels bound to this group, including disabled ones. Exit addresses and keys are never exposed.'
            )}
          </DialogDescription>
        </DialogHeader>
        {isLoading ? <Skeleton className='h-24 w-full' /> : null}
        {!isLoading && data ? <LineList data={data} /> : null}
        {memoryBackend ? (
          <p className='text-muted-foreground text-xs'>
            {t(
              'Concurrency is counted in-process: with multiple instances each one allows the full limit. Configure Redis to share the limit.'
            )}
          </p>
        ) : null}
        <DialogFooter>
          <Button variant='outline' onClick={onClose}>
            {t('Close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function AccelPage() {
  const { t } = useTranslation()
  const isAdmin = useIsAdmin()
  const queryClient = useQueryClient()
  const [switchTarget, setSwitchTarget] = useState<AccelPlan | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['accel'],
    queryFn: getAccelOverview,
    refetchInterval: 30000,
  })

  const subscribeMutation = useMutation({
    mutationFn: (vars: { planId: number; switchPlan: boolean }) =>
      subscribeAccel(vars.planId, vars.switchPlan),
    onSuccess: (_, vars) => {
      toast.success(vars.switchPlan ? t('Plan switched') : t('Subscribed'))
      setSwitchTarget(null)
      void queryClient.invalidateQueries({ queryKey: ['accel'] })
    },
    onError: (error: unknown) => {
      const message = error instanceof Error ? error.message : String(error)
      toast.error(message)
    },
  })

  const subscription = data?.subscription ?? null
  const paidBalanceUSD = data?.paid_balance_usd ?? 0
  const plans = data?.plans ?? []
  const limits = data?.limits ?? DEFAULT_LIMITS
  const isActive = subscription?.active === true
  const lockedGroup = subscription ? subscription.group_name : plans[0]?.group_name

  const handleSubscribe = (plan: AccelPlan) => {
    if (isActive && subscription && subscription.plan_id !== plan.id) {
      setSwitchTarget(plan)
      return
    }
    subscribeMutation.mutate({ planId: plan.id, switchPlan: false })
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Acceleration')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          {isLoading ? <Skeleton className='h-32 w-full' /> : null}
          {!isLoading && subscription ? (
            <ActiveSubscriptionCard
              subscription={subscription}
              paidBalanceUSD={paidBalanceUSD}
              usage={data?.usage ?? null}
              todayUsage={data?.today_usage ?? null}
            />
          ) : null}
          {!isLoading && !subscription ? (
            <EmptySubscriptionCard
              paidBalanceUSD={paidBalanceUSD}
              userGroup={data?.user_group ?? ''}
            />
          ) : null}

          {plans.length > 0 ? (
            <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3'>
              {plans.map((plan) => (
                <PlanCard
                  key={plan.id}
                  plan={plan}
                  paidBalanceUSD={paidBalanceUSD}
                  active={isActive && subscription?.plan_id === plan.id}
                  switching={isActive && subscription?.plan_id !== plan.id}
                  pending={subscribeMutation.isPending}
                  onSubscribe={handleSubscribe}
                />
              ))}
            </div>
          ) : null}

          {lockedGroup ? <UsageGuide group={lockedGroup} /> : null}
          {subscription ? <LinesSection group={subscription.group_name} /> : null}
          {data?.ingress ? <IngressCard ingress={data.ingress} /> : null}
          {isAdmin ? <AdminPlansSection limits={limits} /> : null}
          {isAdmin ? <AdminSubsSection /> : null}
        </div>
      </SectionPageLayout.Content>

      <AlertDialog
        open={switchTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setSwitchTarget(null)
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Switch to this plan?')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'You will be charged {{amount}} right now and a new 30-day period starts. The remaining days on your current plan are forfeited.',
                { amount: formatCurrencyUSD(switchTarget?.price_usd ?? 0) }
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (switchTarget) {
                  subscribeMutation.mutate({
                    planId: switchTarget.id,
                    switchPlan: true,
                  })
                }
              }}
            >
              {t('Confirm switch')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SectionPageLayout>
  )
}
