/*
 * 号池 (Key Pool) 功能：把用户给的商汤/英伟达上游 key 验活后合并进「英伟达号池 / 商汤号池」渠道，
 * 再生成一批固定额度的 token（号池）发给用户。
 *
 * 页面结构（Tab 化，避免再单开一个「号池页面」）：
 *   - 我的号池：用户视角的按供应商订阅卡片（每日额度、今日剩余、下次重置）。
 *   - 贡献密钥：用户自助贡献上游密钥，换取号池每日额度。
 *   - 号池运营：仅管理员可见。概览、生成号池、渠道管理、Token 管理、号池设置。
 */
import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { KeyRound, Plus, Info, ShieldAlert, MailCheck } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { SectionPageLayout } from '@/components/layout'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'

import { useIsAdmin } from '@/hooks/use-admin'

import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  contributeKeyPool,
  getKeyPoolProviders,
  getUserKeyPoolAccounts,
  getUserKeyPoolCards,
  getUserKeyPoolSettings,
  type KeyPoolAccountRow,
  type KeyPoolCard,
  type KeyPoolProvider,
  type KeyPoolResult,
  type UserKeyPoolSettings,
} from './api'
import { KeyPoolAdminPanel } from './admin-panel'
import { Field, ProviderField, ResultPanel } from './shared'
import { fmtQuota, fmtTime } from './utils'

// 订阅状态标签（拆成独立组件以避免嵌套三元）。
function PoolStatusBadge({ card }: { card: KeyPoolCard }) {
  if (card.alive) return <Badge variant='secondary'>正常</Badge>
  if (card.daily_quota > 0) return <Badge variant='warning'>今日额度已用完</Badge>
  return <Badge variant='outline'>未开通每日额度</Badge>
}

// ============ 我的号池订阅卡片 ============

function SubscriptionCards() {
  const [cards, setCards] = useState<KeyPoolCard[]>([])
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    getUserKeyPoolCards()
      .then(setCards)
      .catch(() => setCards([]))
      .finally(() => setLoading(false))
      .catch(() => {})
  }, [])
  if (loading) return <Skeleton className='h-24 w-full' />
  if (cards.length === 0) {
    return (
      <Card>
        <CardContent className='py-8 text-center text-sm text-muted-foreground'>
          还没有号池订阅。贡献一个上游密钥后，这里会显示你按供应商区分的每日额度与重置时间。
        </CardContent>
      </Card>
    )
  }
  return (
    <div className='grid gap-3 sm:grid-cols-2'>
      {cards.map((c) => {
        const used = c.daily_quota > 0 ? c.daily_quota - c.remain_quota : 0
        const pct =
          c.daily_quota > 0
            ? Math.max(0, Math.min(100, Math.round((c.remain_quota / c.daily_quota) * 100)))
            : 0
        return (
          <Card key={c.provider}>
            <CardHeader className='pb-2'>
              <CardTitle className='flex items-center justify-between text-base'>
                <span>{c.provider_label}</span>
                <PoolStatusBadge card={c} />
              </CardTitle>
            </CardHeader>
            <CardContent className='space-y-1.5 text-sm'>
              {c.daily_quota > 0 && (
                <div className='space-y-1'>
                  <div className='h-2 w-full overflow-hidden rounded-full bg-muted'>
                    <div
                      className='h-full rounded-full bg-emerald-500 transition-all'
                      style={{ width: `${pct}%` }}
                    />
                  </div>
                  <div className='text-xs text-muted-foreground'>
                    今日剩余 {fmtQuota(c.remain_quota)} / {fmtQuota(c.daily_quota)}
                    （已用 {fmtQuota(used)}）
                  </div>
                </div>
              )}
              <div className='flex justify-between'>
                <span className='text-muted-foreground'>每日额度</span>
                <b className='tabular-nums'>{fmtQuota(c.daily_quota)}</b>
              </div>
              <div className='flex justify-between'>
                <span className='text-muted-foreground'>今日剩余</span>
                <b className='tabular-nums'>{fmtQuota(c.remain_quota)}</b>
              </div>
              <div className='flex justify-between'>
                <span className='text-muted-foreground'>其中免费余额</span>
                <b className='tabular-nums text-emerald-600 dark:text-emerald-400'>
                  {fmtQuota(c.free_quota ?? 0)}
                </b>
              </div>
              <div className='flex justify-between'>
                <span className='text-muted-foreground'>其中付费余额</span>
                <b className='tabular-nums'>{fmtQuota(c.paid_quota ?? 0)}</b>
              </div>
              <div className='flex justify-between'>
                <span className='text-muted-foreground'>下次重置（北京时间 00:00）</span>
                <span className='tabular-nums'>{fmtTime(c.next_reset)}</span>
              </div>
            </CardContent>
          </Card>
        )
      })}
    </div>
  )
}

// ============ 用户端：自助贡献 ============

function ContributeForm({
  providers,
  settings,
  cards,
  result,
  onResultChange,
}: {
  providers: KeyPoolProvider[]
  settings: UserKeyPoolSettings | null
  cards: KeyPoolCard[]
  result: KeyPoolResult | null
  onResultChange: (r: KeyPoolResult | null) => void
}) {
  const [provider, setProvider] = useState('sensenova')
  const [account, setAccount] = useState('')
  const [keys, setKeys] = useState('')
  const [tokenCount, setTokenCount] = useState('1')
  const [loading, setLoading] = useState(false)
  const [myAccounts, setMyAccounts] = useState<KeyPoolAccountRow[]>([])

  const maxTokens = settings?.max_tokens_per_submit ?? 10
  const allowed = settings?.allow_contribute !== false
  // 该供应商是否已有「活跃订阅卡片」：若有，本次贡献仅增加号池容量，不重复发放每日额度
  const alreadySubscribed = cards.some(
    (c) => c.provider === provider && c.daily_quota > 0
  )
  // 该供应商下已经贡献过的账号（用于提示「同账号不能重复贡献」）
  const providerAccounts = useMemo(
    () => myAccounts.filter((a) => a.provider === provider),
    [myAccounts, provider]
  )

  useEffect(() => {
    getUserKeyPoolAccounts(provider)
      .then(setMyAccounts)
      .catch(() => setMyAccounts([]))
  }, [provider])

  const onSubmit = async () => {
    if (!account.trim()) {
      toast.error('请填写上游账号标识（账号邮箱 / 用户名）')
      return
    }
    if (!keys.trim()) {
      toast.error('请至少粘贴一个上游密钥')
      return
    }
    setLoading(true)
    try {
      const res = await contributeKeyPool({
        provider,
        keys,
        account: account.trim(),
        token_count: Number(tokenCount),
      })
      onResultChange(res)
      if (alreadySubscribed) {
        toast.success('密钥已并入号池，该供应商每日额度保持不变')
      } else {
        toast.success('号池已生成')
      }
      getUserKeyPoolAccounts(provider)
        .then(setMyAccounts)
        .catch(() => {})
    } catch (error) {
      toast.error(getServerErrorMessage(error, '贡献密钥失败'))
    } finally {
      setLoading(false)
    }
  }

  if (!allowed) {
    return (
      <Card>
        <CardContent className='flex items-start gap-2 py-8 text-sm text-muted-foreground'>
          <ShieldAlert className='mt-0.5 h-4 w-4 shrink-0' />
          <span>
            平台已关闭用户自助贡献入口，号池只由运营在后台统一发放。
            如需开通，请联系管理员。
          </span>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className='space-y-4'>
      {result ? (
        <ResultPanel result={result} onDone={() => onResultChange(null)} />
      ) : (
        <Card>
          <CardHeader>
            <CardTitle>贡献并生成号池</CardTitle>
            <CardDescription>
              没有密钥？点击供应商右侧「一键跳转注册 / 申请密钥」获取。
              系统会自动验活并跳过无效密钥。
            </CardDescription>
          </CardHeader>
          <CardContent className='space-y-4'>
            <div className='grid grid-cols-1 gap-4 sm:grid-cols-2'>
              <ProviderField
                providers={providers}
                value={provider}
                onChange={setProvider}
              />
              <Field
                label='Token 数量'
                hint={`本次生成的号池 Token 个数，单次最多 ${maxTokens} 个`}
              >
                <Input
                  value={tokenCount}
                  onChange={(e) => setTokenCount(e.target.value)}
                  type='number'
                  min={1}
                  max={maxTokens}
                />
              </Field>
            </div>

            <Field
              label='上游账号标识'
              hint='该密钥所属的上游账号（注册邮箱 / 用户名）。同一账号只能贡献一把密钥：同账号下新建的第二把 key 不会增加上游真实额度，系统会直接拦截。'
            >
              <Input
                value={account}
                onChange={(e) => setAccount(e.target.value)}
                placeholder='例如：zhangsan@gmail.com'
              />
            </Field>

            {providerAccounts.length > 0 && (
              <div className='rounded-lg border bg-muted/40 px-3 py-2.5 text-xs'>
                <div className='mb-1.5 flex items-center gap-1.5 font-medium'>
                  <MailCheck className='h-3.5 w-3.5' />
                  该供应商下你已贡献的账号
                </div>
                <ul className='space-y-1 text-muted-foreground'>
                  {providerAccounts.map((a) => (
                    <li key={`${a.account_key}-${a.key_mask}`} className='flex justify-between gap-3'>
                      <span className='truncate'>{a.account || '（未登记账号）'}</span>
                      <span className='shrink-0 font-mono'>{a.key_mask}</span>
                    </li>
                  ))}
                </ul>
                <p className='mt-1.5 text-muted-foreground'>
                  同一账号已经被占用，请改用其他账号的密钥；同一把密钥重复贡献也会被拦截。
                </p>
              </div>
            )}

            <div className='rounded-lg border bg-muted/40 px-3 py-2.5 text-xs text-muted-foreground'>
              号池额度由平台统一发放：每个供应商每日赠送额度
              <b className='mx-1 tabular-nums'>
                {fmtQuota(settings?.default_daily_gift_quota ?? 0)}
              </b>
              ，每日 00:00（北京时间）自动重置。你贡献的密钥越多，号池可承载的并发越高。
            </div>

            {alreadySubscribed && (
              <div className='flex items-start gap-2 rounded-md bg-blue-500/10 px-3 py-2.5 text-sm text-blue-600 dark:text-blue-400'>
                <Info className='mt-0.5 h-4 w-4 shrink-0' />
                <span>
                  你已开通该供应商的每日额度，本次贡献的密钥只会并入号池
                  <strong>增加容量</strong>，不会重复发放每日赠送额度（同供应商多 key
                  不叠加）。
                </span>
              </div>
            )}

            <Field
              label='上游密钥（每行一个）'
              hint='系统会逐个验活；仅有效密钥合并进号池渠道，无效密钥会被跳过'
            >
              <Textarea
                rows={6}
                value={keys}
                onChange={(e) => setKeys(e.target.value)}
                placeholder={'sk-xxxx\nsk-yyyy'}
              />
            </Field>
            <Button onClick={onSubmit} disabled={loading}>
              <Plus className='mr-1 h-4 w-4' /> {loading ? '处理中…' : '贡献并生成号池'}
            </Button>
          </CardContent>
        </Card>
      )}
    </div>
  )
}

export function ContributeKey() {
  const isAdmin = useIsAdmin()
  const [providers, setProviders] = useState<KeyPoolProvider[]>([])
  const [cards, setCards] = useState<KeyPoolCard[]>([])
  const [settings, setSettings] = useState<UserKeyPoolSettings | null>(null)
  const [result, setResult] = useState<KeyPoolResult | null>(null)

  useEffect(() => {
    getKeyPoolProviders()
      .then((p) => setProviders(p))
      .catch(() => {})
    getUserKeyPoolCards()
      .then(setCards)
      .catch(() => setCards([]))
    getUserKeyPoolSettings()
      .then(setSettings)
      .catch(() => setSettings(null))
  }, [])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <span className='flex items-center gap-2'>
          <KeyRound className='h-5 w-5 text-primary' />
          号池
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto max-w-4xl space-y-5 pb-4'>
          <Tabs defaultValue='mine'>
            <TabsList>
              <TabsTrigger value='mine'>我的号池</TabsTrigger>
              <TabsTrigger value='contribute'>贡献密钥</TabsTrigger>
              {isAdmin && <TabsTrigger value='admin'>号池运营</TabsTrigger>}
            </TabsList>

            <TabsContent value='mine' className='mt-4 space-y-4'>
              <p className='text-sm text-muted-foreground'>
                这里是你按供应商划分的号池订阅：每日额度由平台统一发放，每日 00:00
                （北京时间）自动重置，额度用尽后次日恢复。
              </p>
              <SubscriptionCards />
            </TabsContent>

            <TabsContent value='contribute' className='mt-4'>
              <p className='mb-4 text-sm text-muted-foreground'>
                提交你的商汤 / 英伟达密钥，系统验活后自动加入对应号池渠道，并为你开通号池每日额度
                （额度与重置由平台统一管理）。
              </p>
              <ContributeForm
                providers={providers}
                settings={settings}
                cards={cards}
                result={result}
                onResultChange={setResult}
              />
            </TabsContent>

            {isAdmin && (
              <TabsContent value='admin' className='mt-4'>
                <KeyPoolAdminPanel />
              </TabsContent>
            )}
          </Tabs>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
