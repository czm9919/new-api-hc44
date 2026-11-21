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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import {
  act,
  cleanup,
  render,
  screen,
  within,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import {
  DEFAULT_BALANCE_DISPLAY,
  serializeBalanceDisplay,
  type BalanceDisplayConfig,
} from '@/hooks/use-balance-display'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { formatTimestampToDate } from '@/lib/format'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import type { User } from '../../types'
import { useUsersColumns } from '../users-columns'
import { UsersProvider } from '../users-provider'
import { UsersTable } from '../users-table'

const clients: QueryClient[] = []
const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

type QuotaTableProps = {
  remaining: number
  used: number
  freeQuota?: number
}

function QuotaTable(props: QuotaTableProps) {
  const columns = useUsersColumns().filter((column) =>
    ['quota', 'used_quota'].includes(
      column.id ?? ('accessorKey' in column ? String(column.accessorKey) : '')
    )
  )
  const table = useReactTable({
    columns,
    data: [
      {
        id: 1,
        username: 'test',
        display_name: '',
        role: 1,
        status: 1,
        quota: props.remaining,
        free_quota: props.freeQuota ?? 0,
        used_quota: props.used,
        request_count: 0,
        group: 'default',
      } as User,
    ],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <table>
      <thead>
        {table.getHeaderGroups().map((group) => (
          <tr key={group.id}>
            {group.headers.map((header) => (
              <th key={header.id} data-sortable={header.column.getCanSort()}>
                {flexRender(
                  header.column.columnDef.header,
                  header.getContext()
                )}
              </th>
            ))}
          </tr>
        ))}
      </thead>
      <tbody>
        {table.getRowModel().rows.map((row) => (
          <tr key={row.id}>
            {row.getVisibleCells().map((cell) => (
              <td key={cell.id}>
                {flexRender(cell.column.columnDef.cell, cell.getContext())}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  )
}

/**
 * 余额显示配置来自 /api/status。这里直接把配置塞进 query 缓存，
 * 既不触发真实请求，也能按用例切换拆分/隐藏等分支。
 */
function makeClient(display?: BalanceDisplayConfig): QueryClient {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(STATUS_QUERY_KEY, {
    BalanceDisplay: serializeBalanceDisplay(display ?? DEFAULT_BALANCE_DISPLAY),
  })
  clients.push(client)
  return client
}

function renderQuotaTable(
  props: QuotaTableProps,
  display?: BalanceDisplayConfig
) {
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={makeClient(display)}>
        <QuotaTable {...props} />
      </QueryClientProvider>
    </I18nextProvider>
  )
}

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
})

it('shows balance above secondary usage text and opens quota details on click', async () => {
  renderQuotaTable({ remaining: 1900, used: 1100 })
  expect(
    screen.getByRole('columnheader', { name: 'Available Balance ($)' })
  ).toHaveAttribute('data-sortable', 'true')
  expect(screen.getAllByRole('columnheader')).toHaveLength(1)
  const cells = screen.getAllByRole('cell')
  expect(within(cells[0]).getByText('0.0038')).toBeInTheDocument()
  expect(cells).toHaveLength(1)
  expect(
    within(cells[0]).queryByText('Available Balance')
  ).not.toBeInTheDocument()
  expect(within(cells[0]).getByText('Paid balance')).toBeInTheDocument()
  expect(within(cells[0]).getByText('0.0022')).toBeInTheDocument()
  expect(screen.getByText('0.0038').parentElement).toHaveClass(
    'grid-cols-[auto_1fr]'
  )
  expect(screen.getByText('Used amount')).toHaveAttribute(
    'data-table-text',
    'secondary'
  )
  expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  expect(screen.queryByText('0.006')).not.toBeInTheDocument()
  const trigger = screen.getByRole('button', {
    name: 'Paid balance 0.0038; Used amount 0.0022',
  })
  await userEvent.click(trigger)
  const detail = await screen.findByRole('dialog', { name: 'Quota ($)' })
  expect(within(detail).getByText('Paid balance')).toBeInTheDocument()
  expect(within(detail).getByText('Free quota')).toBeInTheDocument()
  expect(within(detail).getByText('Total Used')).toBeInTheDocument()
  expect(within(detail).getByText('0.0038')).toBeInTheDocument()
  expect(within(detail).getByText('0.0022')).toBeInTheDocument()
  expect(within(detail).queryByRole('progressbar')).not.toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(trigger).toHaveFocus()
})

it.each([0, 500000])(
  'shows usage for a zero balance only when used quota is nonzero (used=%s)',
  async (used) => {
    renderQuotaTable({ remaining: 0, used })
    if (used === 0) {
      expect(screen.getAllByRole('cell')[0]).toHaveTextContent(/^No Quota$/)
      expect(screen.queryByText('Used amount')).not.toBeInTheDocument()
      await userEvent.click(screen.getByRole('button', { name: 'No Quota' }))
      const detail = await screen.findByRole('dialog', { name: 'Quota ($)' })
      // 付费余额、免费额度、已用三个 0
      expect(within(detail).getAllByText('0')).toHaveLength(3)
      return
    }
    expect(screen.queryByText('No Quota')).not.toBeInTheDocument()
    expect(
      within(screen.getAllByRole('cell')[0]).getByText('0')
    ).toBeInTheDocument()
    expect(
      within(screen.getAllByRole('cell')[0]).getByText('1')
    ).toBeInTheDocument()
  }
)

it('preserves negative balances in details opened with the keyboard', async () => {
  renderQuotaTable({ remaining: -500000, used: 1000000 })
  expect(screen.getByText('-1')).toHaveClass('text-destructive')
  expect(screen.getByText('2')).toBeInTheDocument()
  await userEvent.tab()
  await userEvent.keyboard('{Enter}')
  const detail = await screen.findByRole('dialog')
  expect(within(detail).getByText('-1')).toBeInTheDocument()
  expect(within(detail).getByText('2')).toBeInTheDocument()
})

it('shows the custom symbol only in the column header', () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      quotaDisplayType: 'CUSTOM',
      customCurrencySymbol: '🐱',
      customCurrencyExchangeRate: 1,
    },
  })
  renderQuotaTable({ remaining: 1900, used: 1100 })
  expect(
    screen.getByRole('columnheader', { name: 'Available Balance (🐱)' })
  ).toBeInTheDocument()
  for (const cell of screen.getAllByRole('cell')) {
    expect(cell).not.toHaveTextContent('🐱')
  }
  expect(
    within(screen.getAllByRole('cell')[0]).getByText('0.0038')
  ).toBeInTheDocument()
  expect(
    within(screen.getAllByRole('cell')[0]).getByText('0.0022')
  ).toBeInTheDocument()
})

function UsersPage() {
  return (
    <TooltipProvider>
      <UsersProvider>
        <UsersTable />
      </UsersProvider>
    </TooltipProvider>
  )
}

async function renderUsersList(emptyInvitation = false) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 100 })
  const usersResponse = {
    data: {
      success: true,
      data: {
        items: [
          {
            id: 2,
            username: 'long-user-name-for-table-layout',
            display_name: 'A display name',
            role: 1,
            status: 1,
            quota: 1900,
            used_quota: 1100,
            request_count: 0,
            created_at: Math.floor(Date.now() / 1000) - 86400,
            last_login_at: Math.floor(Date.now() / 1000) - 20,
            group: 'default',
            aff_count: emptyInvitation ? 0 : 2,
            aff_history_quota: emptyInvitation ? 0 : 500000,
            inviter_id: emptyInvitation ? 0 : 42,
          },
        ],
        total: 1,
      },
    },
  }
  const get = vi
    .spyOn(api, 'get')
    .mockImplementation(async (url) =>
      url === '/api/group/'
        ? { data: { success: true, data: ['default'] } }
        : usersResponse
    )
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const users = createRoute({
    getParentRoute: () => auth,
    path: 'users/',
    component: UsersPage,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([users])]),
    history: createMemoryHistory({ initialEntries: ['/users/'] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  await router.load()
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>
  )
  await screen.findByText('long-user-name-for-table-layout')
  return get
}

it('sends balance sorting to the server and keeps invitation details on two lines', async () => {
  const get = await renderUsersList()
  expect(
    screen.getByRole('columnheader', { name: 'User Group' })
  ).toBeInTheDocument()
  await userEvent.click(
    screen.getByRole('button', { name: 'Available Balance ($)' })
  )
  await userEvent.click(screen.getByRole('menuitem', { name: 'Desc' }))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/user/', {
      params: expect.objectContaining({ sort_by: 'quota', sort_order: 'desc' }),
    })
  )
  expect(
    screen.queryByRole('button', { name: 'Total Used' })
  ).not.toBeInTheDocument()
  expect(screen.getByText('Inviter ID: 42')).toBeInTheDocument()
  expect(screen.getByText(/Invited 2 users · Earnings:/)).toBeInTheDocument()
})

it('shows balance above usage on mobile cards in Chinese', async () => {
  const originalMatchMedia = window.matchMedia
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...originalMatchMedia(query),
    matches: query.includes('max-width'),
  }))
  i18n.addResourceBundle('zh', 'translation', zh.translation)
  await i18n.changeLanguage('zh')
  try {
    await renderUsersList()
    expect(screen.getByText('可用余额 ($)')).toBeInTheDocument()
    expect(screen.getByText('已用')).toBeInTheDocument()
    expect(screen.getByText('付费余额')).toBeInTheDocument()
    expect(screen.getByText('0.0038')).toBeInTheDocument()
    expect(screen.getByText('0.0022')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    await userEvent.click(
      screen.getByRole('button', { name: /付费余额 0.0038/ })
    )
    const detail = await screen.findByRole('dialog', { name: '额度 ($)' })
    expect(within(detail).getByText('累计已用')).toBeInTheDocument()
    expect(within(detail).getByText('0.0022')).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')
  } finally {
    await i18n.changeLanguage('en')
  }
})

it('combines creation and last login into one column with full dates visible directly', async () => {
  vi.spyOn(Date, 'now').mockReturnValue(Date.UTC(2026, 8, 8, 12))
  await renderUsersList(true)
  expect(screen.getByRole('columnheader', { name: /Time/ })).toBeInTheDocument()
  expect(
    screen.queryByRole('columnheader', { name: /Last Login/ })
  ).not.toBeInTheDocument()
  const row = screen.getByRole('row', {
    name: /long-user-name-for-table-layout/,
  })
  expect(within(row).getByText('—')).toBeInTheDocument()
  const times = within(row).getAllByRole('time')
  expect(times).toHaveLength(2)
  expect(times[0]).toHaveTextContent(
    formatTimestampToDate(Math.floor(Date.now() / 1000) - 86400)
  )
  expect(times[1]).toHaveTextContent(
    formatTimestampToDate(Math.floor(Date.now() / 1000) - 20)
  )
  expect(within(row).queryByText('Just now')).not.toBeInTheDocument()
  expect(within(row).getByText('Created')).toBeInTheDocument()
  expect(within(row).getByText('Last Login')).toBeInTheDocument()
  expect(screen.queryByText('No Inviter')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'View' }))
  await userEvent.click(screen.getByRole('menuitemcheckbox', { name: 'Time' }))
  expect(
    screen.queryByRole('columnheader', { name: /Time/ })
  ).not.toBeInTheDocument()
})

it('updates the header unit and converted amounts together when currency settings change', () => {
  renderQuotaTable({ remaining: 500000, used: 1000000 })
  act(() =>
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        quotaDisplayType: 'CNY',
        usdExchangeRate: 7,
      },
    })
  )
  expect(
    screen.getByRole('columnheader', { name: 'Available Balance (¥)' })
  ).toBeInTheDocument()
  expect(
    within(screen.getAllByRole('cell')[0]).getByText('7')
  ).toBeInTheDocument()
  expect(
    within(screen.getAllByRole('cell')[0]).getByText('14')
  ).toBeInTheDocument()
  for (const cell of screen.getAllByRole('cell')) {
    expect(cell).not.toHaveTextContent('¥')
  }
})

it('labels raw quota mode as tokens without introducing a currency symbol', () => {
  useSystemConfigStore.getState().setConfig({
    currency: { ...DEFAULT_CURRENCY_CONFIG, quotaDisplayType: 'TOKENS' },
  })
  renderQuotaTable({ remaining: 100, used: 200 })
  expect(
    screen.getByRole('columnheader', { name: 'Available Balance (Tokens)' })
  ).toBeInTheDocument()
  expect(
    within(screen.getAllByRole('cell')[0]).getByText('100')
  ).toBeInTheDocument()
  expect(
    within(screen.getAllByRole('cell')[0]).getByText('200')
  ).toBeInTheDocument()
})

it('splits the balance into paid and free rows when the user has free quota', () => {
  // 1900 总额里有 1000 是签到赠送 → 付费 900、免费 1000
  renderQuotaTable({ remaining: 1900, used: 1100, freeQuota: 1000 })
  const cell = screen.getAllByRole('cell')[0]
  expect(within(cell).getByText('Paid balance')).toBeInTheDocument()
  expect(within(cell).getByText('0.0018')).toBeInTheDocument()
  expect(within(cell).getByText('Free quota')).toBeInTheDocument()
  expect(within(cell).getByText('0.002')).toBeInTheDocument()
  expect(within(cell).getByText('0.0022')).toBeInTheDocument()
  expect(
    screen.getByRole('button', {
      name: 'Paid balance 0.0018; Free quota 0.002; Used amount 0.0022',
    })
  ).toBeInTheDocument()
})

it('hides the free row but keeps it in the details when hideFree is on', async () => {
  renderQuotaTable(
    { remaining: 1900, used: 1100, freeQuota: 1000 },
    {
      ...DEFAULT_BALANCE_DISPLAY,
      hideFree: true,
    }
  )
  const cell = screen.getAllByRole('cell')[0]
  expect(within(cell).getByText('Paid balance')).toBeInTheDocument()
  expect(within(cell).queryByText('Free quota')).not.toBeInTheDocument()
  await userEvent.click(
    screen.getByRole('button', {
      name: 'Paid balance 0.0018; Used amount 0.0022',
    })
  )
  const detail = await screen.findByRole('dialog', { name: 'Quota ($)' })
  expect(within(detail).getByText('Free quota')).toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
})

it('falls back to a single merged balance when splitting is off or not enabled for the list', () => {
  const merged = 'Available Balance 0.0038; Used amount 0.0022'
  const { unmount } = renderQuotaTable(
    { remaining: 1900, used: 1100, freeQuota: 1000 },
    { ...DEFAULT_BALANCE_DISPLAY, splitEnabled: false }
  )
  expect(screen.getByRole('button', { name: merged })).toBeInTheDocument()
  expect(screen.queryByText('Paid balance')).not.toBeInTheDocument()
  unmount()

  renderQuotaTable(
    { remaining: 1900, used: 1100, freeQuota: 1000 },
    {
      ...DEFAULT_BALANCE_DISPLAY,
      showInUserList: false,
    }
  )
  expect(screen.getByRole('button', { name: merged })).toBeInTheDocument()
  expect(screen.queryByText('Free quota')).not.toBeInTheDocument()
})

it('uses the admin configured labels verbatim', () => {
  renderQuotaTable(
    { remaining: 1900, used: 1100, freeQuota: 1000 },
    {
      ...DEFAULT_BALANCE_DISPLAY,
      paidLabel: '充值余额',
      freeLabel: '赠送额度',
    }
  )
  const cell = screen.getAllByRole('cell')[0]
  expect(within(cell).getByText('充值余额')).toBeInTheDocument()
  expect(within(cell).getByText('赠送额度')).toBeInTheDocument()
  expect(
    screen.getByRole('button', {
      name: '充值余额 0.0018; 赠送额度 0.002; Used amount 0.0022',
    })
  ).toBeInTheDocument()
})
