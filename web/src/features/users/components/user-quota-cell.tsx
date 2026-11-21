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
import { useTranslation } from 'react-i18next'

import { QuotaDetailsPopover } from '@/components/quota-details-popover'
import { StatusBadge } from '@/components/status-badge'
import { formatQuotaWithCurrency, getCurrencyDisplay } from '@/lib/currency'
import { cn } from '@/lib/utils'
import { useSystemConfigStore } from '@/stores/system-config-store'

type UserQuotaCellProps = {
  remaining: number
  used: number
  /** 免费额度（签到等赠送），用于把总余额拆成「付费余额 / 免费额度」 */
  freeQuota?: number
  /**
   * 是否拆分展示。由调用方决定而不是读全局配置：同一套配置在不同位置
   * 可能不生效（例如用户管理列表还要看 showInUserList）。
   */
  split?: boolean
  /** 拆分时是否隐藏免费额度那一行 */
  hideFree?: boolean
  /** 付费余额自定义显示名；空串表示用默认翻译文案 */
  paidLabel?: string
  /** 免费额度自定义显示名；空串表示用默认翻译文案 */
  freeLabel?: string
}

type QuotaCellContentProps = {
  hasQuota: boolean
  split: boolean
  showFreeRow: boolean
  paidLabel: string
  freeLabel: string
  paid: number
  free: number
  remaining: number
  used: number
  formattedPaid: string
  formattedFree: string
  formattedRemaining: string
  formattedUsed: string
  usedLabel: string
  noQuotaLabel: string
}

/** 表格单元格正文：无额度 / 拆分三行 / 合并单值，三种形态各自独立，避免嵌套三元。 */
function QuotaCellContent(props: QuotaCellContentProps) {
  if (!props.hasQuota) {
    return (
      <StatusBadge
        label={props.noQuotaLabel}
        variant='neutral'
        copyable={false}
        className='-ml-1.5 font-normal'
      />
    )
  }

  if (!props.split) {
    return (
      <span className='grid min-w-0 grid-cols-1 gap-y-1 text-sm tabular-nums'>
        <span
          className={cn(
            props.remaining < 0 && 'text-destructive',
            props.remaining === 0 && 'text-muted-foreground'
          )}
        >
          {props.formattedRemaining}
        </span>
        <span
          data-table-text='secondary'
          className='text-muted-foreground flex items-baseline gap-1 text-xs font-normal'
        >
          <span>{props.usedLabel}</span>
          <span>{props.formattedUsed}</span>
        </span>
      </span>
    )
  }

  return (
    <span className='grid min-w-0 grid-cols-[auto_1fr] items-baseline gap-x-2 gap-y-1 text-sm tabular-nums'>
      <span className='text-muted-foreground text-xs font-normal'>
        {props.paidLabel}
      </span>
      <span
        className={cn(
          'justify-self-end',
          props.paid < 0 && 'text-destructive',
          props.paid === 0 && 'text-muted-foreground'
        )}
      >
        {props.formattedPaid}
      </span>
      {props.showFreeRow && (
        <>
          <span className='text-muted-foreground text-xs font-normal'>
            {props.freeLabel}
          </span>
          <span className='text-muted-foreground justify-self-end'>
            {props.formattedFree}
          </span>
        </>
      )}
      <span
        data-table-text='secondary'
        className='text-muted-foreground text-xs font-normal'
      >
        {props.usedLabel}
      </span>
      <span
        data-table-text='secondary'
        className='text-muted-foreground justify-self-end text-xs font-normal'
      >
        {props.formattedUsed}
      </span>
    </span>
  )
}

export function UserQuotaCell(props: UserQuotaCellProps) {
  const { t } = useTranslation()
  useSystemConfigStore((state) => state.config.currency)

  const { meta: currency } = getCurrencyDisplay()
  const quotaUnit = currency.kind === 'tokens' ? t('Tokens') : currency.symbol
  const hasQuota = props.remaining !== 0 || props.used !== 0
  const formattedUsed = formatQuotaWithCurrency(props.used, {
    showSymbol: false,
  })

  const freeQuota = Math.max(0, props.freeQuota ?? 0)
  const split = props.split === true
  // 付费余额 = 总余额 - 免费额度。总余额为正时不允许出现负的付费余额（免费额度
  // 理论上不会超过总额，超了说明数据不一致，把付费显示成 0 更不容易误导）；
  // 总余额本身为负（透支）时保留负号，让管理员一眼看到异常。
  let paidQuota = props.remaining - freeQuota
  if (props.remaining >= 0 && paidQuota < 0) paidQuota = 0

  const formattedRemaining = formatQuotaWithCurrency(props.remaining, {
    showSymbol: false,
  })
  const formattedPaid = formatQuotaWithCurrency(paidQuota, {
    showSymbol: false,
  })
  const formattedFree = formatQuotaWithCurrency(freeQuota, {
    showSymbol: false,
  })

  const paidLabel = props.paidLabel?.trim() || t('Paid balance')
  const freeLabel = props.freeLabel?.trim() || t('Free quota')
  // 表格里只为「真的存在免费额度」的用户单独占一行：绝大多数用户 free_quota 为 0，
  // 给所有人加一行「免费额度 0」纯属噪音。隐藏与否只影响表格，弹层始终列全。
  const showFreeRow = split && props.hideFree !== true && freeQuota > 0

  const details = split
    ? [
        { label: paidLabel, value: formattedPaid },
        { label: freeLabel, value: formattedFree },
        { label: t('Total Used'), value: formattedUsed },
      ]
    : [
        { label: t('Available Balance'), value: formattedRemaining },
        { label: t('Total Used'), value: formattedUsed },
      ]

  const labelParts = split
    ? [
        `${paidLabel} ${formattedPaid}`,
        ...(showFreeRow ? [`${freeLabel} ${formattedFree}`] : []),
        `${t('Used amount')} ${formattedUsed}`,
      ]
    : [
        `${t('Available Balance')} ${formattedRemaining}`,
        `${t('Used amount')} ${formattedUsed}`,
      ]

  return (
    <QuotaDetailsPopover
      title={`${t('Quota')} (${quotaUnit})`}
      triggerLabel={hasQuota ? labelParts.join('; ') : t('No Quota')}
      details={details}
    >
      <QuotaCellContent
        hasQuota={hasQuota}
        split={split}
        showFreeRow={showFreeRow}
        paidLabel={paidLabel}
        freeLabel={freeLabel}
        paid={paidQuota}
        free={freeQuota}
        remaining={props.remaining}
        used={props.used}
        formattedPaid={formattedPaid}
        formattedFree={formattedFree}
        formattedRemaining={formattedRemaining}
        formattedUsed={formattedUsed}
        usedLabel={t('Used amount')}
        noQuotaLabel={t('No Quota')}
      />
    </QuotaDetailsPopover>
  )
}
