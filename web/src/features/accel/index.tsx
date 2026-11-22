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
 * 加速专线（/accel，hc48）。
 *
 * 页面四块：
 *  1. 状态卡：未开通 / 生效中剩 N 天 / 已到期，附付费余额
 *  2. 套餐卡：价格、并发、开通按钮（余额不足直接禁用并说明原因）
 *  3. 使用指引：分组名怎么填到令牌上（复制），以及「一个专用令牌」的建议
 *  4. 管理端：套餐 CRUD（仅管理员可见）
 *
 * 注意：这个页面只负责「卖」，能不能用加速分组最终由后端 TokenAuth 判定，
 * 前端隐藏按钮不构成任何限制。
 */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, Gauge, Rocket, ShieldCheck, Trash2, Zap } from 'lucide-react'
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
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { useIsAdmin } from '@/hooks/use-admin'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { formatQuota, formatTimestampToDate } from '@/lib/format'

import {
  createAccelPlan,
  deleteAccelPlan,
  getAccelOverview,
  listAccelPlans,
  setAccelAutoRenew,
  subscribeAccel,
  updateAccelPlan,
  type AccelPlan,
  type AccelPlanInput,
  type AccelSubscription,
} from './api'

const EMPTY_FORM: AccelPlanInput = {
  name: '',
  group_name: '',
  price_per_month: 0,
  concurrency: 0,
  description: '',
  enabled: true,
  sort_order: 0,
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

function SubscriptionCard({
  subscription,
  paidBalance,
  userGroup,
}: {
  subscription: AccelSubscription | null
  paidBalance: number
  userGroup: string
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

  if (!subscription) {
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
              {formatQuota(paidBalance)}
            </span>{' '}
            · {t('Monthly fee is charged from paid balance only')}
          </div>
        </CardContent>
      </Card>
    )
  }

  const expired = !subscription.active
  return (
    <Card className={expired ? 'border-destructive/40' : 'border-primary/30'}>
      <CardHeader className='gap-1'>
        <div className='flex flex-wrap items-center gap-2'>
          <CardTitle className='text-base'>{subscription.plan_name}</CardTitle>
          {expired ? (
            <Badge variant='destructive'>{t('Expired')}</Badge>
          ) : (
            <Badge>{t('{{days}} days left', { days: subscription.days_left })}</Badge>
          )}
          <GroupChip group={subscription.group_name} />
        </div>
        <CardDescription>
          {t('Expires at {{time}}', {
            time: formatTimestampToDate(subscription.expire_at),
          })}
          {' · '}
          {t('Renewed every 30 days')}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-3'>
        <div className='text-sm'>
          {t('Paid balance')}:{' '}
          <span className='font-medium'>{formatQuota(paidBalance)}</span>
          {' · '}
          {t('Monthly fee')}:{' '}
          <span className='font-medium'>
            {formatQuota(subscription.price_per_month)}
          </span>
        </div>
        {subscription.last_charge_error ? (
          <p className='text-destructive text-sm'>
            {subscription.last_charge_error}
          </p>
        ) : null}
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
      </CardContent>
    </Card>
  )
}

function PlanCard({
  plan,
  paidBalance,
  active,
  onSubscribe,
  pending,
}: {
  plan: AccelPlan
  paidBalance: number
  active: boolean
  onSubscribe: (planId: number) => void
  pending: boolean
}) {
  const { t } = useTranslation()
  const affordable = paidBalance >= plan.price_per_month
  const disabled = active || pending || !affordable
  return (
    <Card className='flex flex-col'>
      <CardHeader className='gap-1'>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Zap className='size-4' />
          {plan.name}
        </CardTitle>
        <CardDescription>{plan.description || plan.group_name}</CardDescription>
      </CardHeader>
      <CardContent className='flex flex-1 flex-col justify-between gap-3'>
        <div className='text-2xl font-medium'>
          {formatQuota(plan.price_per_month)}
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
          <li className='flex items-center gap-2'>
            <Rocket className='size-3.5' />
            {t('Group')}: <GroupChip group={plan.group_name} />
          </li>
        </ul>
        <Button
          className='w-full'
          disabled={disabled}
          onClick={() => onSubscribe(plan.id)}
        >
          {active ? t('Current plan') : t('Subscribe')}
        </Button>
        {!affordable && !active ? (
          <p className='text-muted-foreground text-xs'>
            {t('Top up your paid balance to subscribe')}
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
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  plan: AccelPlan | null
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
          price_per_month: plan.price_per_month,
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
              placeholder='accel'
            />
          </div>
          <div className='grid grid-cols-2 gap-3'>
            <div className='grid gap-1.5'>
              <Label>{t('Monthly fee (quota)')}</Label>
              <Input
                type='number'
                value={form.price_per_month}
                onChange={(event) =>
                  patch({ price_per_month: Number(event.target.value) || 0 })
                }
              />
            </div>
            <div className='grid gap-1.5'>
              <Label>{t('Concurrency (0 = unlimited)')}</Label>
              <Input
                type='number'
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
            disabled={mutation.isPending || !form.name || !form.group_name}
            onClick={() => mutation.mutate()}
          >
            {t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function AdminPlansSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<AccelPlan | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)

  const { data, isLoading } = useQuery({
    queryKey: ['accel-admin-plans'],
    queryFn: listAccelPlans,
  })

  const toggleMutation = useMutation({
    mutationFn: (plan: AccelPlan) =>
      updateAccelPlan(plan.id, {
        name: plan.name,
        group_name: plan.group_name,
        price_per_month: plan.price_per_month,
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
                {formatQuota(plan.price_per_month)} / 30 {t('days')}
                {' · '}
                {plan.concurrency > 0
                  ? t('{{count}} concurrent requests', { count: plan.concurrency })
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
      />
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

export function AccelPage() {
  const { t } = useTranslation()
  const isAdmin = useIsAdmin()
  const queryClient = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: ['accel'],
    queryFn: getAccelOverview,
    refetchInterval: 30000,
  })

  const subscribeMutation = useMutation({
    mutationFn: subscribeAccel,
    onSuccess: () => {
      toast.success(t('Subscribed'))
      void queryClient.invalidateQueries({ queryKey: ['accel'] })
    },
    onError: (error: unknown) => {
      const message = error instanceof Error ? error.message : String(error)
      toast.error(message)
    },
  })

  const subscription = data?.subscription ?? null
  const paidBalance = data?.paid_balance ?? 0
  const plans = data?.plans ?? []
  const lockedGroup = subscription ? subscription.group_name : plans[0]?.group_name

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Acceleration')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          {isLoading ? <Skeleton className='h-32 w-full' /> : null}
          {!isLoading ? (
            <SubscriptionCard
              subscription={subscription}
              paidBalance={paidBalance}
              userGroup={data?.user_group ?? ''}
            />
          ) : null}

          {plans.length > 0 ? (
            <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3'>
              {plans.map((plan) => (
                <PlanCard
                  key={plan.id}
                  plan={plan}
                  paidBalance={paidBalance}
                  active={
                    subscription?.active === true &&
                    subscription.group_name === plan.group_name
                  }
                  pending={subscribeMutation.isPending}
                  onSubscribe={(planId) => subscribeMutation.mutate(planId)}
                />
              ))}
            </div>
          ) : null}

          {lockedGroup ? <UsageGuide group={lockedGroup} /> : null}
          {isAdmin ? <AdminPlansSection /> : null}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
