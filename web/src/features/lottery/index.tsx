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
 * 大转盘抽奖（/lottery，v1.1.0 M2）。
 *
 * 页面三块：
 *  1. 转盘：扇区来自后端奖池，抽奖结果按 prize_index 定位指针
 *  2. QQ 绑定卡：面板只发一次性码，用户把码发给配套 AstrBot 机器人；
 *     未绑定时抽奖按钮禁用（后端也会 403，前端只是提前说清楚）
 *  3. 记录 + 管理端奖池配置（仅管理员可见）
 */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Check,
  Coins,
  Copy,
  Dices,
  Eye,
  EyeOff,
  KeyRound,
  Plus,
  RefreshCw,
  RotateCcw,
  RotateCw,
  ShieldCheck,
  Smartphone,
  Trash2,
  Unlink,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

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
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { useIsAdmin } from '@/hooks/use-admin'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { formatTimestampToDate } from '@/lib/format'

import {
  clearAdminQQToken,
  createQQBindCode,
  extractSettingsFieldErrors,
  getAdminLotterySettings,
  getAdminQQSettings,
  getLottery,
  getLotteryRecords,
  getQQBindStatus,
  resetAdminLotterySettings,
  rotateAdminQQToken,
  saveAdminLotterySettings,
  saveAdminQQSettings,
  spinLottery,
  unbindQQ,
  type LotteryPrize,
  type LotterySettings,
  type LotterySpinResult,
} from './api'

/** 扇区配色：冷暖交替，保证相邻扇区视觉可分。 */
const SEGMENT_COLORS = [
  '#f97316',
  '#facc15',
  '#22c55e',
  '#0ea5e9',
  '#8b5cf6',
  '#ec4899',
  '#14b8a6',
  '#f43f5e',
  '#84cc16',
  '#6366f1',
  '#fb923c',
  '#06b6d4',
]

function buildConicGradient(prizes: LotteryPrize[]): string {
  if (prizes.length === 0) return 'conic-gradient(#e5e7eb 0deg 360deg)'
  const step = 360 / prizes.length
  const stops = prizes.map((_, index) => {
    const color = SEGMENT_COLORS[index % SEGMENT_COLORS.length]
    const start = index * step
    const end = start + step
    return `${color} ${start}deg ${end}deg`
  })
  return `conic-gradient(${stops.join(', ')})`
}

/** 单个扇区文字：旋转到扇区中线，从圆心向外排布。 */
function WheelLabel(props: {
  prize: LotteryPrize
  index: number
  total: number
}) {
  const { prize, index, total } = props
  const step = 360 / total
  const angle = index * step + step / 2
  return (
    <div
      className='pointer-events-none absolute inset-0 flex items-start justify-center'
      style={{ transform: `rotate(${angle}deg)` }}
    >
      <span
        className='mt-3 max-w-[42%] text-center text-[11px] leading-tight font-semibold text-white drop-shadow sm:text-xs'
        style={{ transform: `rotate(0deg)` }}
      >
        {prize.name}
      </span>
    </div>
  )
}

function Wheel(props: {
  prizes: LotteryPrize[]
  rotation: number
  spinning: boolean
}) {
  const gradient = useMemo(
    () => buildConicGradient(props.prizes),
    [props.prizes]
  )
  return (
    <div className='relative mx-auto aspect-square w-full max-w-[320px]'>
      {/* 指针 */}
      <div className='absolute top-0 left-1/2 z-20 -translate-x-1/2 -translate-y-1'>
        <div className='h-0 w-0 border-x-[10px] border-t-[18px] border-x-transparent border-t-red-600 drop-shadow' />
      </div>
      <div
        className='absolute inset-0 rounded-full border-4 border-amber-500 shadow-inner'
        style={{
          background: gradient,
          transform: `rotate(${props.rotation}deg)`,
          transition: props.spinning
            ? 'transform 3.2s cubic-bezier(0.17, 0.67, 0.2, 1)'
            : 'none',
        }}
      >
        {props.prizes.map((prize, index) => (
          <WheelLabel
            key={prize.id}
            prize={prize}
            index={index}
            total={props.prizes.length}
          />
        ))}
      </div>
      {/* 中心轴 */}
      <div className='absolute top-1/2 left-1/2 z-10 flex h-12 w-12 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full border-2 border-amber-500 bg-white text-amber-600 shadow'>
        <Dices className='h-5 w-5' />
      </div>
    </div>
  )
}

function QQBindCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [code, setCode] = useState('')
  const [copied, setCopied] = useState(false)

  const statusQuery = useQuery({
    queryKey: ['qq-bind-status'],
    queryFn: getQQBindStatus,
  })

  const codeMutation = useMutation({
    mutationFn: createQQBindCode,
    onSuccess: (data) => {
      setCode(data.code)
      setCopied(false)
      void queryClient.invalidateQueries({ queryKey: ['qq-bind-status'] })
    },
  })

  const unbindMutation = useMutation({
    mutationFn: unbindQQ,
    onSuccess: () => {
      setCode('')
      void queryClient.invalidateQueries({ queryKey: ['qq-bind-status'] })
      void queryClient.invalidateQueries({ queryKey: ['lottery-status'] })
    },
  })

  const status = statusQuery.data
  const errorMessage = codeMutation.error
    ? (codeMutation.error as Error).message
    : ''

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Smartphone className='h-4 w-4' />
          {t('QQ binding')}
        </CardTitle>
        <CardDescription>
          {t('Bind QQ to unlock the wheel and free models')}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-3'>
        {statusQuery.isLoading ? <Skeleton className='h-10 w-full' /> : null}
        {status?.bound ? (
          <div className='space-y-3'>
            <div className='flex items-center gap-2'>
              <Badge variant='secondary' className='font-mono'>
                {status.qq}
              </Badge>
              <span className='text-muted-foreground text-xs'>
                {t('Bound QQ')}
              </span>
            </div>
            <Button
              variant='outline'
              size='sm'
              onClick={() => unbindMutation.mutate()}
              disabled={unbindMutation.isPending}
            >
              <Unlink className='mr-1 h-4 w-4' />
              {t('Unbind')}
            </Button>
          </div>
        ) : (
          <div className='space-y-3'>
            <Button
              size='sm'
              onClick={() => codeMutation.mutate()}
              disabled={codeMutation.isPending || !status?.agent_enabled}
            >
              <ShieldCheck className='mr-1 h-4 w-4' />
              {t('Generate bind code')}
            </Button>
            {!status?.agent_enabled && !statusQuery.isLoading ? (
              <p className='text-muted-foreground text-xs'>
                {t('QQ bot is not configured yet.')}
              </p>
            ) : null}
            {code ? (
              <div className='space-y-2 rounded-lg border border-dashed p-3'>
                <p className='font-mono text-2xl font-bold tracking-widest'>
                  {code}
                </p>
                <p className='text-muted-foreground text-xs'>
                  {t('Send this code to the bot and it expires in 10 minutes.')}
                </p>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => {
                    void navigator.clipboard?.writeText(code)
                    setCopied(true)
                  }}
                >
                  <Copy className='mr-1 h-4 w-4' />
                  {copied ? t('Copied') : t('Copy code')}
                </Button>
              </div>
            ) : null}
            {errorMessage ? (
              <p className='text-destructive text-xs'>{errorMessage}</p>
            ) : null}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

/** 奖池草稿的本地校验：与后端 ValidateLotterySettings 同一套规则，先在前端说清楚。 */
const PRIZE_NAME_MAX = 24
const PRIZE_MAX = 12
const PRIZE_WEIGHT_MIN = 1
const PRIZE_WEIGHT_MAX = 1000
const PRIZE_QUOTA_MAX = 1000000
const DAILY_SPINS_MAX = 50
// 与后端 service.LotteryMaxPaidPrice / LotteryMaxPaidDailyLimit 保持一致。
const PAID_PRICE_MAX = 1000000
const PAID_DAILY_LIMIT_MAX = 200

function validatePrizeDraft(
  prizes: LotteryPrize[],
  dailySpins: number,
  paidEnabled: boolean,
  paidPrice: number,
  paidDailyLimit: number,
  t: (key: string, options?: Record<string, unknown>) => string
): Record<string, string> {
  const errors: Record<string, string> = {}
  if (
    !Number.isFinite(dailySpins) ||
    dailySpins < 0 ||
    dailySpins > DAILY_SPINS_MAX
  ) {
    errors.daily_spins = t('Daily spins must be between 0 and {{max}}', {
      max: DAILY_SPINS_MAX,
    })
  }
  // 付费抽奖：关闭时允许留 0，开启时价格与次数都必须 > 0，否则用户点了也抽不了。
  if (
    !Number.isFinite(paidPrice) ||
    paidPrice < 0 ||
    paidPrice > PAID_PRICE_MAX
  ) {
    errors.paid_price = t('Paid price must be between 0 and {{max}}', {
      max: PAID_PRICE_MAX,
    })
  } else if (paidEnabled && paidPrice <= 0) {
    errors.paid_price = t('Paid price must be positive when paid spins are on')
  }
  if (
    !Number.isFinite(paidDailyLimit) ||
    paidDailyLimit < 0 ||
    paidDailyLimit > PAID_DAILY_LIMIT_MAX
  ) {
    errors.paid_daily_limit = t(
      'Paid daily limit must be between 0 and {{max}}',
      { max: PAID_DAILY_LIMIT_MAX }
    )
  } else if (paidEnabled && paidDailyLimit <= 0) {
    errors.paid_daily_limit = t(
      'Paid daily limit must be positive when paid spins are on'
    )
  }
  if (prizes.length === 0) {
    errors.prizes = t('Prize pool needs at least one prize')
  } else if (prizes.length > PRIZE_MAX) {
    errors.prizes = t('Prize pool allows at most {{max}} prizes', {
      max: PRIZE_MAX,
    })
  }

  const seen = new Set<string>()
  prizes.forEach((prize, index) => {
    const field = (suffix: string) => `prizes[${index}].${suffix}`
    const name = prize.name.trim()
    if (!name) {
      errors[field('name')] = t('Prize name is required')
    } else if ([...name].length > PRIZE_NAME_MAX) {
      errors[field('name')] = t(
        'Prize name must be at most {{max}} characters',
        { max: PRIZE_NAME_MAX }
      )
    }
    if (prize.type === 'quota') {
      if (!Number.isFinite(prize.quota) || prize.quota <= 0) {
        errors[field('quota')] = t('Quota prize needs a positive quota')
      } else if (prize.quota > PRIZE_QUOTA_MAX) {
        errors[field('quota')] = t('Prize quota must be at most {{max}}', {
          max: PRIZE_QUOTA_MAX,
        })
      }
    }
    if (
      !Number.isFinite(prize.weight) ||
      prize.weight < PRIZE_WEIGHT_MIN ||
      prize.weight > PRIZE_WEIGHT_MAX
    ) {
      errors[field('weight')] = t(
        'Weight must be between {{min}} and {{max}}',
        {
          min: PRIZE_WEIGHT_MIN,
          max: PRIZE_WEIGHT_MAX,
        }
      )
    }
    if (!prize.id) {
      errors[field('id')] = t('Prize id is required')
    } else if (seen.has(prize.id)) {
      errors[field('id')] = t('Prize id must be unique')
    }
    seen.add(prize.id)
  })

  const totalWeight = prizes.reduce(
    (sum, prize) => sum + (prize.weight > 0 ? prize.weight : 0),
    0
  )
  if (prizes.length > 0 && totalWeight <= 0) {
    errors.prizes = t('At least one prize needs a positive weight')
  }
  return errors
}

function newPrizeDraft(): LotteryPrize {
  const suffix =
    typeof crypto !== 'undefined' && 'randomUUID' in crypto
      ? crypto.randomUUID().slice(0, 8)
      : Math.random().toString(36).slice(2, 10)
  return {
    id: `prize-${suffix}`,
    name: '',
    type: 'quota',
    quota: 500,
    weight: 1,
  }
}

/** 转盘 / 抽奖设置（管理端）：开关、每日次数、奖池表格、机器人接入。 */
function AdminSettingsCard(props: { settings: LotterySettings }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [enabled, setEnabled] = useState(props.settings.enabled)
  const [requireQQ, setRequireQQ] = useState(props.settings.require_qq)
  const [dailySpins, setDailySpins] = useState(props.settings.daily_spins)
  const [paidEnabled, setPaidEnabled] = useState(props.settings.paid_enabled)
  const [paidPrice, setPaidPrice] = useState(props.settings.paid_price)
  const [paidDailyLimit, setPaidDailyLimit] = useState(
    props.settings.paid_daily_limit
  )
  const [prizes, setPrizes] = useState<LotteryPrize[]>(props.settings.prizes)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState('')
  const [confirmReset, setConfirmReset] = useState(false)

  // 保存成功 / 切换页面后后端的配置会变，草稿跟着回填，避免显示过期值。
  useEffect(() => {
    setEnabled(props.settings.enabled)
    setRequireQQ(props.settings.require_qq)
    setDailySpins(props.settings.daily_spins)
    setPaidEnabled(props.settings.paid_enabled)
    setPaidPrice(props.settings.paid_price)
    setPaidDailyLimit(props.settings.paid_daily_limit)
    setPrizes(props.settings.prizes)
  }, [props.settings])

  const localErrors = useMemo(
    () =>
      validatePrizeDraft(
        prizes,
        dailySpins,
        paidEnabled,
        paidPrice,
        paidDailyLimit,
        t
      ),
    [prizes, dailySpins, paidEnabled, paidPrice, paidDailyLimit, t]
  )
  const totalWeight = prizes.reduce(
    (sum, prize) => sum + (prize.weight > 0 ? prize.weight : 0),
    0
  )

  const applySaved = (saved: LotterySettings) => {
    setFieldErrors({})
    setFormError('')
    setEnabled(saved.enabled)
    setRequireQQ(saved.require_qq)
    setDailySpins(saved.daily_spins)
    setPaidEnabled(saved.paid_enabled)
    setPaidPrice(saved.paid_price)
    setPaidDailyLimit(saved.paid_daily_limit)
    setPrizes(saved.prizes)
    void queryClient.invalidateQueries({ queryKey: ['lottery-status'] })
    void queryClient.invalidateQueries({ queryKey: ['admin-lottery-settings'] })
  }

  const saveMutation = useMutation({
    mutationFn: saveAdminLotterySettings,
    onSuccess: (saved) => {
      applySaved(saved)
      toast.success(t('Lottery settings saved'))
    },
    onError: (error) => {
      const fields = extractSettingsFieldErrors(error)
      if (fields.length > 0) {
        setFieldErrors(
          Object.fromEntries(fields.map((item) => [item.field, item.message]))
        )
      }
      setFormError((error as Error).message)
    },
  })

  const resetMutation = useMutation({
    mutationFn: resetAdminLotterySettings,
    onSuccess: (saved) => {
      applySaved(saved)
      setConfirmReset(false)
      toast.success(t('Restored default prize pool'))
    },
    onError: (error) => setFormError((error as Error).message),
  })

  function updatePrize(index: number, patch: Partial<LotteryPrize>) {
    setPrizes((current) =>
      current.map((prize, i) => (i === index ? { ...prize, ...patch } : prize))
    )
  }

  function submit() {
    setFormError('')
    if (Object.keys(localErrors).length > 0) {
      setFieldErrors(localErrors)
      toast.error(t('Please fix the highlighted fields first'))
      return
    }
    setFieldErrors({})
    saveMutation.mutate({
      enabled,
      daily_spins: dailySpins,
      require_qq: requireQQ,
      paid_enabled: paidEnabled,
      paid_price: paidPrice,
      paid_daily_limit: paidDailyLimit,
      prizes: prizes.map((prize) => ({
        ...prize,
        name: prize.name.trim(),
        quota: prize.type === 'quota' ? prize.quota : 0,
      })),
    })
  }

  const errorOf = (field: string) => fieldErrors[field]

  return (
    <Card>
      <CardHeader>
        <CardTitle className='text-base'>{t('Lottery settings')}</CardTitle>
        <CardDescription>
          {t('Changes apply to all users immediately.')}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='flex flex-wrap items-center gap-x-6 gap-y-3 text-sm'>
          <label className='flex items-center gap-2'>
            <Switch
              checked={enabled}
              onCheckedChange={(checked) => setEnabled(checked)}
              size='sm'
              aria-label={t('Lottery enabled')}
            />
            {t('Lottery enabled')}
          </label>
          <label className='flex items-center gap-2'>
            <Switch
              checked={requireQQ}
              onCheckedChange={(checked) => setRequireQQ(checked)}
              size='sm'
              aria-label={t('Require QQ binding')}
            />
            {t('Require QQ binding')}
          </label>
          <label className='flex items-center gap-2'>
            {t('Daily spins')}
            <Input
              type='number'
              min={0}
              max={DAILY_SPINS_MAX}
              value={dailySpins}
              onChange={(event) => setDailySpins(Number(event.target.value))}
              className='w-20'
              aria-label={t('Daily spins')}
              aria-invalid={Boolean(errorOf('daily_spins'))}
            />
            <span className='text-muted-foreground text-xs'>
              {t('per user per day, up to {{max}}', { max: DAILY_SPINS_MAX })}
            </span>
          </label>
        </div>
        {errorOf('daily_spins') ? (
          <p className='text-destructive text-xs'>{errorOf('daily_spins')}</p>
        ) : null}

        <div className='space-y-2 border-t pt-4'>
          <div className='flex flex-wrap items-center gap-x-6 gap-y-3 text-sm'>
            <label className='flex items-center gap-2'>
              <Switch
                checked={paidEnabled}
                onCheckedChange={(checked) => setPaidEnabled(checked)}
                size='sm'
                aria-label={t('Paid spins enabled')}
              />
              {t('Paid spins enabled')}
            </label>
            <label className='flex items-center gap-2'>
              {t('Paid price')}
              <Input
                type='number'
                min={0}
                max={PAID_PRICE_MAX}
                value={paidPrice}
                onChange={(event) => setPaidPrice(Number(event.target.value))}
                className='w-28'
                aria-label={t('Paid price')}
                aria-invalid={Boolean(errorOf('paid_price'))}
              />
              <span className='text-muted-foreground text-xs'>
                {t('quota per paid spin')}
              </span>
            </label>
            <label className='flex items-center gap-2'>
              {t('Paid spins per day')}
              <Input
                type='number'
                min={0}
                max={PAID_DAILY_LIMIT_MAX}
                value={paidDailyLimit}
                onChange={(event) =>
                  setPaidDailyLimit(Number(event.target.value))
                }
                className='w-20'
                aria-label={t('Paid spins per day')}
                aria-invalid={Boolean(errorOf('paid_daily_limit'))}
              />
              <span className='text-muted-foreground text-xs'>
                {t('per user per day, up to {{max}}', {
                  max: PAID_DAILY_LIMIT_MAX,
                })}
              </span>
            </label>
          </div>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Paid spins only spend the paid balance, never the free quota. Users without enough balance are rejected instead of going negative.'
            )}
          </p>
          {paidEnabled && paidPrice > 0 && paidDailyLimit > 0 ? (
            <p className='text-muted-foreground text-xs'>
              {t('Max paid spend per user per day: {{amount}}', {
                amount: paidPrice * paidDailyLimit,
              })}
            </p>
          ) : null}
          {errorOf('paid_price') ? (
            <p className='text-destructive text-xs'>{errorOf('paid_price')}</p>
          ) : null}
          {errorOf('paid_daily_limit') ? (
            <p className='text-destructive text-xs'>
              {errorOf('paid_daily_limit')}
            </p>
          ) : null}
        </div>

        <div className='space-y-2'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <div className='text-sm font-medium'>
              {t('Prize pool')}
              <span className='text-muted-foreground ml-2 text-xs'>
                {prizes.length}/{PRIZE_MAX} · {t('total weight')} {totalWeight}
              </span>
            </div>
            <div className='flex items-center gap-2'>
              <Button
                size='sm'
                variant='outline'
                disabled={prizes.length >= PRIZE_MAX}
                onClick={() =>
                  setPrizes((current) => [...current, newPrizeDraft()])
                }
              >
                <Plus className='mr-1 h-3.5 w-3.5' />
                {t('Add prize')}
              </Button>
              {confirmReset ? (
                <>
                  <Button
                    size='sm'
                    variant='destructive'
                    disabled={resetMutation.isPending}
                    onClick={() => resetMutation.mutate()}
                  >
                    {t('Confirm restore')}
                  </Button>
                  <Button
                    size='sm'
                    variant='ghost'
                    onClick={() => setConfirmReset(false)}
                  >
                    {t('Cancel')}
                  </Button>
                </>
              ) : (
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() => setConfirmReset(true)}
                >
                  <RotateCcw className='mr-1 h-3.5 w-3.5' />
                  {t('Restore defaults')}
                </Button>
              )}
            </div>
          </div>

          <div className='overflow-x-auto'>
            <div className='min-w-[640px] space-y-2'>
              <div className='text-muted-foreground grid grid-cols-[minmax(0,2fr)_110px_110px_100px_70px_36px] gap-2 px-1 text-xs'>
                <span>{t('Prize name')}</span>
                <span>{t('Type')}</span>
                <span>{t('Quota')}</span>
                <span>{t('Weight')}</span>
                <span>{t('Chance')}</span>
                <span />
              </div>
              {prizes.map((prize, index) => {
                const quotaDisabled = prize.type !== 'quota'
                const chance =
                  totalWeight > 0 && prize.weight > 0
                    ? ((prize.weight / totalWeight) * 100).toFixed(1)
                    : '0.0'
                return (
                  <div key={prize.id} className='space-y-1'>
                    <div className='grid grid-cols-[minmax(0,2fr)_110px_110px_100px_70px_36px] items-center gap-2'>
                      <Input
                        value={prize.name}
                        placeholder={t('e.g. 500 quota')}
                        onChange={(event) =>
                          updatePrize(index, { name: event.target.value })
                        }
                        aria-label={`${t('Prize name')} ${index + 1}`}
                        aria-invalid={Boolean(errorOf(`prizes[${index}].name`))}
                      />
                      <NativeSelect
                        value={prize.type}
                        onChange={(event) =>
                          updatePrize(index, {
                            type: event.target.value as LotteryPrize['type'],
                          })
                        }
                        aria-label={`${t('Type')} ${index + 1}`}
                      >
                        <option value='quota'>{t('Quota prize')}</option>
                        <option value='none'>{t('Thanks only')}</option>
                      </NativeSelect>
                      <Input
                        type='number'
                        min={0}
                        max={PRIZE_QUOTA_MAX}
                        value={quotaDisabled ? 0 : prize.quota}
                        disabled={quotaDisabled}
                        onChange={(event) =>
                          updatePrize(index, {
                            quota: Number(event.target.value),
                          })
                        }
                        aria-label={`${t('Quota')} ${index + 1}`}
                        aria-invalid={Boolean(
                          errorOf(`prizes[${index}].quota`)
                        )}
                      />
                      <Input
                        type='number'
                        min={PRIZE_WEIGHT_MIN}
                        max={PRIZE_WEIGHT_MAX}
                        value={prize.weight}
                        onChange={(event) =>
                          updatePrize(index, {
                            weight: Number(event.target.value),
                          })
                        }
                        aria-label={`${t('Weight')} ${index + 1}`}
                        aria-invalid={Boolean(
                          errorOf(`prizes[${index}].weight`)
                        )}
                      />
                      <span className='text-muted-foreground text-xs tabular-nums'>
                        {chance}%
                      </span>
                      <Button
                        size='icon'
                        variant='ghost'
                        aria-label={`${t('Remove prize')} ${index + 1}`}
                        disabled={prizes.length <= 1}
                        onClick={() =>
                          setPrizes((current) =>
                            current.filter((_, i) => i !== index)
                          )
                        }
                      >
                        <Trash2 className='h-4 w-4' />
                      </Button>
                    </div>
                    {[
                      errorOf(`prizes[${index}].name`),
                      errorOf(`prizes[${index}].type`),
                      errorOf(`prizes[${index}].quota`),
                      errorOf(`prizes[${index}].weight`),
                      errorOf(`prizes[${index}].id`),
                    ]
                      .filter(Boolean)
                      .map((message) => (
                        <p
                          key={message}
                          className='text-destructive pl-1 text-xs'
                        >
                          {message}
                        </p>
                      ))}
                  </div>
                )
              })}
            </div>
          </div>
          {errorOf('prizes') ? (
            <p className='text-destructive text-xs'>{errorOf('prizes')}</p>
          ) : null}
        </div>

        {formError ? (
          <p className='text-destructive text-xs'>{formError}</p>
        ) : null}

        <div className='flex items-center gap-2'>
          <Button
            size='sm'
            onClick={submit}
            disabled={saveMutation.isPending}
            aria-label={t('Save lottery settings')}
          >
            {saveMutation.isPending ? t('Saving') : t('Save')}
          </Button>
          {Object.keys(localErrors).length > 0 ? (
            <span className='text-destructive text-xs'>
              {t('{{n}} field(s) need attention', {
                n: Object.keys(localErrors).length,
              })}
            </span>
          ) : null}
        </div>

        <div className='space-y-2 border-t pt-4'>
          <p className='text-sm font-medium'>{t('QQ bot integration')}</p>
          <QQAgentSettingsForm />
        </div>
      </CardContent>
    </Card>
  )
}

/** 机器人接入：令牌轮换/撤销 + 免费模型门禁 + 概览指标。 */
function QQAgentSettingsForm() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { copyToClipboard } = useCopyToClipboard({ notify: false })
  const settingsQuery = useQuery({
    queryKey: ['admin-qq-settings'],
    queryFn: getAdminQQSettings,
  })
  const [tokenInput, setTokenInput] = useState('')
  const [revealToken, setRevealToken] = useState(false)
  const [requireForFree, setRequireForFree] = useState(true)
  const [issuedToken, setIssuedToken] = useState('')
  const [copied, setCopied] = useState(false)
  const [confirmClear, setConfirmClear] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')

  const config = settingsQuery.data

  useEffect(() => {
    if (config) setRequireForFree(config.require_for_free)
  }, [config])

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['admin-qq-settings'] })
    void queryClient.invalidateQueries({ queryKey: ['qq-bind-status'] })
  }

  const saveMutation = useMutation({
    mutationFn: saveAdminQQSettings,
    onSuccess: (next) => {
      setTokenInput('')
      setErrorMessage('')
      setRequireForFree(next.require_for_free)
      setIssuedToken('')
      refresh()
      toast.success(t('Bot integration settings saved'))
    },
    onError: (error) => {
      const fields = extractSettingsFieldErrors(error)
      setErrorMessage(
        fields.length > 0 ? fields[0].message : (error as Error).message
      )
    },
  })

  const rotateMutation = useMutation({
    mutationFn: rotateAdminQQToken,
    onSuccess: (result) => {
      setIssuedToken(result.agent_token)
      setCopied(false)
      setTokenInput('')
      setErrorMessage('')
      refresh()
      toast.success(t('New agent token generated'))
    },
    onError: (error) => setErrorMessage((error as Error).message),
  })

  const clearMutation = useMutation({
    mutationFn: clearAdminQQToken,
    onSuccess: () => {
      setConfirmClear(false)
      setIssuedToken('')
      setErrorMessage('')
      refresh()
      toast.success(t('Agent token revoked'))
    },
    onError: (error) => setErrorMessage((error as Error).message),
  })

  const busy =
    saveMutation.isPending ||
    rotateMutation.isPending ||
    clearMutation.isPending

  return (
    <div className='space-y-3 text-sm'>
      <div className='flex flex-wrap items-center gap-2'>
        <Badge variant={config?.agent_configured ? 'default' : 'outline'}>
          {config?.agent_configured
            ? `${t('Configured')} ${config.agent_token_hint}`
            : t('Not configured')}
        </Badge>
        <Badge variant='outline'>
          {t('Bound users')}: {config?.bound_users ?? 0}
        </Badge>
        <Badge variant='outline'>
          {t('Pending bind codes')}: {config?.active_bind_codes ?? 0}
        </Badge>
      </div>

      <label className='flex items-center gap-2'>
        <Switch
          checked={requireForFree}
          onCheckedChange={(checked) => setRequireForFree(checked)}
          size='sm'
          aria-label={t('Require QQ binding for free models')}
        />
        {t('Require QQ binding for free models')}
      </label>

      <div className='flex flex-wrap items-center gap-2'>
        <div className='relative'>
          <Input
            type={revealToken ? 'text' : 'password'}
            value={tokenInput}
            placeholder={
              config?.agent_configured
                ? t('Leave blank to keep the current token')
                : t('Agent service token')
            }
            onChange={(event) => setTokenInput(event.target.value)}
            className='w-[280px] pr-9 font-mono text-xs'
            aria-label={t('Agent service token')}
          />
          <button
            type='button'
            className='text-muted-foreground hover:text-foreground absolute top-1/2 right-2 -translate-y-1/2'
            aria-label={revealToken ? t('Hide token') : t('Show token')}
            onClick={() => setRevealToken((current) => !current)}
          >
            {revealToken ? (
              <EyeOff className='h-4 w-4' />
            ) : (
              <Eye className='h-4 w-4' />
            )}
          </button>
        </div>
        <Button
          size='sm'
          disabled={busy}
          aria-label={t('Save bot settings')}
          onClick={() => {
            setErrorMessage('')
            saveMutation.mutate({
              agent_token: tokenInput.trim() || undefined,
              require_for_free: requireForFree,
            })
          }}
        >
          {saveMutation.isPending ? t('Saving') : t('Save')}
        </Button>
        <Button
          size='sm'
          variant='outline'
          disabled={busy}
          onClick={() => rotateMutation.mutate()}
        >
          <RotateCw className='mr-1 h-3.5 w-3.5' />
          {t('Generate random token')}
        </Button>
        {confirmClear ? (
          <>
            <Button
              size='sm'
              variant='destructive'
              disabled={busy}
              onClick={() => clearMutation.mutate()}
            >
              {t('Confirm revoke')}
            </Button>
            <Button
              size='sm'
              variant='ghost'
              onClick={() => setConfirmClear(false)}
            >
              {t('Cancel')}
            </Button>
          </>
        ) : (
          <Button
            size='sm'
            variant='ghost'
            disabled={busy || !config?.agent_configured}
            onClick={() => setConfirmClear(true)}
          >
            <KeyRound className='mr-1 h-3.5 w-3.5' />
            {t('Revoke token')}
          </Button>
        )}
      </div>

      {issuedToken ? (
        <div className='space-y-2 rounded-md border border-amber-400/60 bg-amber-50 p-3 text-xs dark:bg-amber-950/30'>
          <p className='font-medium'>{t('New token (shown only once)')}</p>
          <div className='flex flex-wrap items-center gap-2'>
            <code className='bg-background/70 flex-1 rounded px-2 py-1 font-mono break-all'>
              {issuedToken}
            </code>
            <Button
              size='sm'
              variant='outline'
              onClick={() => {
                void copyToClipboard(issuedToken).then((ok) => {
                  setCopied(ok)
                  if (ok) toast.success(t('Copied to clipboard'))
                })
              }}
            >
              {copied ? (
                <Check className='mr-1 h-3.5 w-3.5' />
              ) : (
                <Copy className='mr-1 h-3.5 w-3.5' />
              )}
              {copied ? t('Copied') : t('Copy')}
            </Button>
          </div>
          <p className='text-muted-foreground'>
            {t(
              'Paste it into the AstrBot plugin config — leaving this page hides it forever.'
            )}
          </p>
        </div>
      ) : null}

      {errorMessage ? (
        <p className='text-destructive text-xs'>{errorMessage}</p>
      ) : null}

      <p className='text-muted-foreground text-xs'>
        {t(
          'Token must be 16–128 visible ASCII characters. The bot sends it as X-QQ-Agent-Token.'
        )}
      </p>
    </div>
  )
}

export function LotteryPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isAdmin = useIsAdmin()

  const statusQuery = useQuery({
    queryKey: ['lottery-status'],
    queryFn: getLottery,
  })
  const recordsQuery = useQuery({
    queryKey: ['lottery-records'],
    queryFn: () => getLotteryRecords(20),
  })
  const adminSettingsQuery = useQuery({
    queryKey: ['admin-lottery-settings'],
    queryFn: getAdminLotterySettings,
    enabled: isAdmin,
  })

  const [rotation, setRotation] = useState(0)
  const [spinning, setSpinning] = useState(false)
  // 记住本次点的是免费还是付费按钮：转的是同一个转盘，但按钮文案与结果提示要分清。
  const [spinPaid, setSpinPaid] = useState(false)
  const [result, setResult] = useState<LotterySpinResult | null>(null)
  const [spinError, setSpinError] = useState('')

  const spinMutation = useMutation({
    mutationFn: (paid: boolean) => spinLottery(paid),
    onSuccess: (data) => {
      setSpinError('')
      const total = statusQuery.data?.settings.prizes.length ?? 0
      if (total > 0) {
        const step = 360 / total
        // 指针在正上方：把命中扇区的中线转到 0 度，再多转 5 圈。
        const target = 360 * 5 - (data.prize_index * step + step / 2)
        setRotation((prev) => prev + target)
      }
      window.setTimeout(() => {
        setSpinning(false)
        setResult(data)
        void queryClient.invalidateQueries({ queryKey: ['lottery-status'] })
        void queryClient.invalidateQueries({ queryKey: ['lottery-records'] })
      }, 3300)
    },
    onError: (error) => {
      setSpinning(false)
      setSpinError((error as Error).message)
    },
  })

  // 结果横幅展示 6 秒后自动收起，避免一直占着屏幕。
  useEffect(() => {
    if (!result) return
    const timer = window.setTimeout(() => setResult(null), 6000)
    return () => window.clearTimeout(timer)
  }, [result])

  if (statusQuery.isLoading) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Lucky Wheel')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <Skeleton className='h-[360px] w-full' />
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  const status = statusQuery.data
  const settings = status?.settings
  const prizes = settings?.prizes ?? []
  // 站点可以把「抽奖要求 QQ」关掉：那时未绑定用户也应该能抽。
  const qqRequired = settings?.require_qq !== false
  const qqBlocked = qqRequired && !status?.qq_bound
  const freeRemaining = status?.spins_remaining ?? 0
  const paidRemaining = status?.paid_spins_remaining ?? 0
  const paidEnabled = settings?.paid_enabled === true
  const paidPrice = settings?.paid_price ?? 0
  // 只有后端明确说「抽得起」才允许点：前端不再自己算余额，避免和后端口径不一致。
  const canSpinFree =
    Boolean(settings?.enabled) && freeRemaining > 0 && !qqBlocked && !spinning
  const canSpinPaid =
    Boolean(settings?.enabled) &&
    paidEnabled &&
    (status?.paid_affordable ?? false) &&
    paidRemaining > 0 &&
    !qqBlocked &&
    !spinning
  // 免费用尽后的提示：要区分「付费次数也没了」和「付费余额不够」，否则用户以为还能花钱抽。
  let noSpinHint = ''
  if (freeRemaining === 0 && !canSpinPaid) {
    if (!paidEnabled) {
      noSpinHint = t('No spins left today. Come back tomorrow.')
    } else if (paidRemaining === 0) {
      noSpinHint = t('No free or paid spins left today. Come back tomorrow.')
    } else {
      noSpinHint = t('No free spins left. Paid balance is not enough.')
    }
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Lucky Wheel')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Badge variant='outline'>
          {t('Spins left today')}: {freeRemaining}
        </Badge>
        {paidEnabled ? (
          <Badge variant='outline'>
            {t('Paid spins left')}: {paidRemaining}
          </Badge>
        ) : null}
        {paidEnabled ? (
          <Badge variant='outline'>
            {t('Paid balance')}: {status?.paid_quota ?? 0}
          </Badge>
        ) : null}
        <Button
          variant='outline'
          size='sm'
          onClick={() => {
            void statusQuery.refetch()
            void recordsQuery.refetch()
          }}
        >
          <RefreshCw className='h-4 w-4' />
        </Button>
      </SectionPageLayout.Actions>

      <SectionPageLayout.Content>
        <div className='grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]'>
          <Card>
            <CardHeader>
              <CardTitle className='text-base'>{t('Spin the wheel')}</CardTitle>
              <CardDescription>
                {settings?.enabled
                  ? t('Each spin awards free quota instantly.')
                  : t('Wheel disabled')}
              </CardDescription>
            </CardHeader>
            <CardContent className='space-y-4'>
              <Wheel prizes={prizes} rotation={rotation} spinning={spinning} />
              <div className='flex flex-col items-center gap-2'>
                <div className='flex flex-wrap items-center justify-center gap-2'>
                  <Button
                    size='lg'
                    disabled={!canSpinFree}
                    onClick={() => {
                      setResult(null)
                      setSpinPaid(false)
                      setSpinning(true)
                      spinMutation.mutate(false)
                    }}
                  >
                    <Dices className='mr-2 h-5 w-5' />
                    {spinning && !spinPaid ? t('Spinning…') : t('Spin')}
                  </Button>
                  {paidEnabled ? (
                    <Button
                      size='lg'
                      variant='outline'
                      disabled={!canSpinPaid}
                      onClick={() => {
                        setResult(null)
                        setSpinPaid(true)
                        setSpinning(true)
                        spinMutation.mutate(true)
                      }}
                    >
                      <Coins className='mr-2 h-5 w-5' />
                      {spinning && spinPaid
                        ? t('Spinning…')
                        : t('Spin for {{price}}', { price: paidPrice })}
                    </Button>
                  ) : null}
                </div>
                {qqBlocked ? (
                  <p className='text-muted-foreground text-xs'>
                    {t('Bind a QQ account first to spin.')}
                  </p>
                ) : null}
                {!qqBlocked && !spinning && noSpinHint ? (
                  <p className='text-muted-foreground text-xs'>{noSpinHint}</p>
                ) : null}
                {spinError ? (
                  <p className='text-destructive text-xs'>{spinError}</p>
                ) : null}
                {result ? (
                  <div className='rounded-lg border border-amber-400/60 bg-amber-50 px-4 py-3 text-center dark:bg-amber-950/30'>
                    <p className='text-sm font-semibold'>
                      {result.quota_awarded > 0
                        ? t('You won {{prize}}', { prize: result.prize.name })
                        : t('Better luck next time')}
                    </p>
                    {result.quota_awarded > 0 ? (
                      <p className='text-muted-foreground mt-1 flex items-center justify-center gap-1 text-xs'>
                        <Coins className='h-3 w-3' />+{result.quota_awarded}
                      </p>
                    ) : null}
                    {result.quota_cost > 0 ? (
                      <p className='text-muted-foreground mt-1 text-xs'>
                        {t('This spin cost {{cost}} quota', {
                          cost: result.quota_cost,
                        })}
                      </p>
                    ) : null}
                  </div>
                ) : null}
              </div>
            </CardContent>
          </Card>

          <div className='space-y-4'>
            <QQBindCard />
            <Card>
              <CardHeader>
                <CardTitle className='text-base'>{t('Prize pool')}</CardTitle>
              </CardHeader>
              <CardContent className='space-y-1 text-sm'>
                {prizes.map((prize) => (
                  <div
                    key={prize.id}
                    className='flex items-center justify-between'
                  >
                    <span className='truncate'>{prize.name}</span>
                    <span className='text-muted-foreground text-xs'>
                      {prize.type === 'quota'
                        ? `${prize.quota} · ${prize.weight}`
                        : `- · ${prize.weight}`}
                    </span>
                  </div>
                ))}
              </CardContent>
            </Card>
          </div>
        </div>

        <Card>
          <CardHeader>
            <CardTitle className='text-base'>{t('Recent spins')}</CardTitle>
          </CardHeader>
          <CardContent>
            {recordsQuery.isLoading ? (
              <Skeleton className='h-20 w-full' />
            ) : null}
            {!recordsQuery.isLoading &&
            (recordsQuery.data?.items.length ?? 0) === 0 ? (
              <p className='text-muted-foreground text-sm'>
                {t('No spins yet.')}
              </p>
            ) : null}
            <div className='space-y-2'>
              {(recordsQuery.data?.items ?? []).map((record) => (
                <div
                  key={record.id}
                  className='flex items-center justify-between border-b pb-2 text-sm last:border-0 last:pb-0'
                >
                  <span className='flex min-w-0 items-center gap-2'>
                    <span className='truncate'>{record.prize_name}</span>
                    {record.paid ? (
                      <Badge variant='outline' className='shrink-0 text-[10px]'>
                        {t('Paid')}
                      </Badge>
                    ) : null}
                  </span>
                  <span className='text-muted-foreground ml-3 shrink-0 text-xs'>
                    {record.quota_cost > 0 ? `-${record.quota_cost} · ` : ''}
                    {record.quota_awarded > 0
                      ? `+${record.quota_awarded}`
                      : '—'}{' '}
                    · {formatTimestampToDate(record.created_at)}
                  </span>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>

        {isAdmin && adminSettingsQuery.data ? (
          <AdminSettingsCard settings={adminSettingsQuery.data} />
        ) : null}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
