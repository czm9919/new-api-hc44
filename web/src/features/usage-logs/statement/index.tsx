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
import { ChevronLeft, ChevronRight, Download, FileBarChart } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'

import { getMonthlyStatement, type MonthlyStatementData } from './api'

const localCurrentMonth = () => {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

const shiftMonth = (month: string, delta: number) => {
  const [year, mon] = month.split('-').map(Number)
  if (!year || !mon) return month
  const date = new Date(year, mon - 1 + delta, 1)
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}`
}

const isValidMonth = (value: string) => /^\d{4}-(0[1-9]|1[0-2])$/.test(value)

function SummaryCard(props: {
  title: string
  primary: string
  secondary?: string
}) {
  return (
    <Card data-card-hover='false'>
      <CardContent className='space-y-1 p-4'>
        <p className='text-muted-foreground text-xs'>{props.title}</p>
        <p className='text-xl font-semibold tabular-nums'>{props.primary}</p>
        {props.secondary && (
          <p className='text-muted-foreground text-xs tabular-nums'>
            {props.secondary}
          </p>
        )}
      </CardContent>
    </Card>
  )
}

function downloadStatementCsv(
  stmt: MonthlyStatementData,
  t: (key: string) => string
) {
  const lines: string[] = [
    `${t('Monthly Statement')},${stmt.month}`,
    `${t('Recharge')},${stmt.recharge_quota}`,
    `${t('Consumption')},${stmt.consume_quota}`,
    `${t('Refund')},${stmt.refund_quota}`,
    `${t('Order Count')},${stmt.recharge_orders}`,
    `${t('Recharge')} money,${stmt.recharge_money.toFixed(2)}`,
    `${t('Pending Orders')},${stmt.pending_orders}`,
    `${t('Pending Amount')},${stmt.pending_money.toFixed(2)}`,
    `${t('Pending Recharge Quota')},${stmt.pending_recharge_quota}`,
    '',
    t('Daily Breakdown'),
    `date,${t('Recharge')},${t('Consumption')},${t('Refund')}`,
    ...stmt.days.map((day) =>
      [day.date, day.recharge_quota, day.consume_quota, day.refund_quota].join(
        ','
      )
    ),
  ]
  // BOM 让 Excel 正确识别 UTF-8 中文表头。
  const blob = new Blob([`\ufeff${lines.join('\n')}`], {
    type: 'text/csv;charset=utf-8',
  })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = `statement-${stmt.month}.csv`
  anchor.click()
  URL.revokeObjectURL(url)
}

export function MonthlyStatementPage() {
  const { t } = useTranslation()
  const [month, setMonth] = useState(localCurrentMonth)

  const statementQuery = useQuery({
    queryKey: ['monthly-statement', month],
    enabled: isValidMonth(month),
    queryFn: () => getMonthlyStatement(month),
  })

  const statement = statementQuery.data
  const hasData =
    !!statement && (statement.days.length > 0 || statement.recharge_orders > 0)

  const columns = useMemo(
    () => [
      {
        id: 'date',
        header: t('Date'),
        cell: (row: { date: string }) => (
          <span className='tabular-nums'>{row.date}</span>
        ),
      },
      {
        id: 'recharge',
        header: t('Recharge'),
        cell: (row: { recharge_quota: number }) => (
          <span className='tabular-nums'>
            {formatQuota(row.recharge_quota)}
          </span>
        ),
      },
      {
        id: 'consume',
        header: t('Consumption'),
        cell: (row: { consume_quota: number }) => (
          <span className='tabular-nums'>{formatQuota(row.consume_quota)}</span>
        ),
      },
      {
        id: 'refund',
        header: t('Refund'),
        cell: (row: { refund_quota: number }) => (
          <span className='tabular-nums'>{formatQuota(row.refund_quota)}</span>
        ),
      },
    ],
    [t]
  )

  let body: ReactNode
  if (statementQuery.isLoading) {
    body = (
      <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4'>
        {['a', 'b', 'c', 'd'].map((key) => (
          <Skeleton key={key} className='h-24 w-full' />
        ))}
      </div>
    )
  } else if (statementQuery.isError) {
    body = (
      <p className='text-destructive text-sm'>{statementQuery.error.message}</p>
    )
  } else if (!statement) {
    body = null
  } else {
    body = (
      <>
        <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4'>
          <SummaryCard
            title={t('Recharge')}
            primary={formatQuota(statement.recharge_quota)}
            secondary={`${t('Order Count')} ${statement.recharge_orders} · ${statement.recharge_money.toFixed(2)}`}
          />
          <SummaryCard
            title={t('Consumption')}
            primary={formatQuota(statement.consume_quota)}
          />
          <SummaryCard
            title={t('Refund')}
            primary={formatQuota(statement.refund_quota)}
          />
          <SummaryCard
            title={t('Pending Orders')}
            primary={String(statement.pending_orders)}
            secondary={`${t('Pending Amount')} ${statement.pending_money.toFixed(2)} · ${t('Pending Recharge Quota')} ${formatQuota(statement.pending_recharge_quota)}`}
          />
        </div>
        <section className='space-y-2'>
          <h3 className='text-sm font-medium'>{t('Daily Breakdown')}</h3>
          {statement.days.length === 0 ? (
            <p className='text-muted-foreground py-4 text-center text-sm'>
              {t('No statement data for this month')}
            </p>
          ) : (
            <StaticDataTable
              data={statement.days}
              getRowKey={(row) => row.date}
              columns={columns}
            />
          )}
        </section>
      </>
    )
  }

  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>
        <span className='flex items-center gap-2'>
          <FileBarChart className='text-primary h-5 w-5' />
          {t('Monthly Statement')}
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='flex items-center gap-1.5'>
          <Button
            type='button'
            variant='outline'
            size='icon-sm'
            aria-label={t('Previous month')}
            onClick={() => setMonth((prev) => shiftMonth(prev, -1))}
          >
            <ChevronLeft />
          </Button>
          <Input
            type='month'
            value={month}
            aria-label={t('Select month')}
            className='w-[150px]'
            onChange={(event) => {
              const value = event.target.value
              if (isValidMonth(value)) setMonth(value)
            }}
          />
          <Button
            type='button'
            variant='outline'
            size='icon-sm'
            aria-label={t('Next month')}
            onClick={() => setMonth((prev) => shiftMonth(prev, 1))}
          >
            <ChevronRight />
          </Button>
          {statement && (
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => downloadStatementCsv(statement, t)}
              disabled={!hasData}
            >
              <Download data-icon='inline-start' />
              {t('Export CSV')}
            </Button>
          )}
        </div>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex h-full min-h-0 flex-col gap-4 overflow-y-auto'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Monthly billing statement with recharge, consumption, refund and pending orders'
            )}
          </p>
          {body}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
