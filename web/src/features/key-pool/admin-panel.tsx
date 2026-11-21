/*
 * 号池运营面板（管理员可见）：概览统计、号池渠道管理、号池 Token 管理。
 *
 * 这一块补的是「号池只能生成、不能管」的缺口：
 *  - 渠道：一眼看清几把密钥、几把被停用、绑了多少模型、关联多少 Token；可逐把启停与一键验活；
 *  - Token：看清发给了谁、每日额度、今天还剩多少；可手动把额度补回每日上限；
 *  - 设置：每日额度、用户自助贡献开关、模型自动绑定开关。
 */
import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  KeyRound,
  RefreshCw,
  RotateCcw,
  Wrench,
} from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  getKeyPoolChannelKeys,
  getKeyPoolChannels,
  getKeyPoolOverview,
  getKeyPoolSettings,
  getKeyPoolTokens,
  resetKeyPoolTokenQuota,
  setKeyPoolKeyStatus,
  testKeyPoolChannel,
  updateKeyPoolSettings,
  type KeyPoolChannelInfo,
  type KeyPoolChannelTestResult,
  type KeyPoolKeyInfo,
  type KeyPoolOverview,
  type KeyPoolResult,
  type KeyPoolSettings,
  type KeyPoolTokenRow,
} from './api'
import { KeyPoolGeneratorForm } from './generator-form'

function fmtNum(n?: number) {
  if (n == null) return '-'
  return n.toLocaleString('zh-CN')
}

// ============================ 总览 ============================

function OverviewCards({ data }: { data: KeyPoolOverview | null }) {
  if (!data) {
    return (
      <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className='h-20 w-full' />
        ))}
      </div>
    )
  }
  const items = [
    { label: '号池渠道', value: data.channel_count, hint: '按供应商各一个' },
    {
      label: '上游密钥',
      value: data.enabled_keys,
      hint: `共 ${data.total_keys} 把，已停用 ${data.disabled_keys} 把`,
    },
    {
      label: '号池 Token',
      value: data.token_count,
      hint: `存活 ${data.alive_tokens} / 已归零 ${data.dead_tokens}`,
    },
    {
      label: '剩余额度',
      value: data.token_remain_quota,
      hint: `每日额度合计 ${fmtNum(data.token_daily_quota)}`,
    },
  ]
  return (
    <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
      {items.map((it) => (
        <div key={it.label} className='rounded-lg border bg-muted/40 p-3'>
          <div className='text-xs text-muted-foreground'>{it.label}</div>
          <div className='mt-1 text-lg font-semibold tabular-nums'>
            {fmtNum(it.value)}
          </div>
          <div className='mt-0.5 truncate text-xs text-muted-foreground'>
            {it.hint}
          </div>
        </div>
      ))}
    </div>
  )
}

// ============================ 设置 ============================

function SettingsCard() {
  const [settings, setSettings] = useState<KeyPoolSettings | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    getKeyPoolSettings()
      .then(setSettings)
      .catch(() => setSettings(null))
  }, [])

  if (!settings) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className='text-base'>号池设置</CardTitle>
        </CardHeader>
        <CardContent>
          <Skeleton className='h-24 w-full' />
        </CardContent>
      </Card>
    )
  }

  const patch = (next: Partial<KeyPoolSettings>) =>
    setSettings({ ...settings, ...next })

  const save = async () => {
    setSaving(true)
    try {
      const saved = await updateKeyPoolSettings(settings)
      setSettings(saved)
      toast.success('号池设置已保存')
    } catch (error) {
      toast.error(getServerErrorMessage(error, '保存失败'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Wrench className='h-4 w-4' /> 号池设置
        </CardTitle>
        <CardDescription>
          这些开关此前只能改数据库，现已可在后台直接设置。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='space-y-1.5'>
          <Label className='text-sm font-medium'>用户自助贡献的每日额度</Label>
          <Input
            type='number'
            min={0}
            value={settings.default_daily_gift_quota}
            onChange={(e) =>
              patch({ default_daily_gift_quota: Number(e.target.value) || 0 })
            }
          />
          <p className='text-xs text-muted-foreground'>
            用户贡献密钥后，每个号池 Token 每天赠送的额度；每日 00:00
            （北京时间）自动重置回该值。设为 0 则不赠送。
          </p>
        </div>

        <div className='space-y-1.5'>
          <Label className='text-sm font-medium'>
            用户单次贡献可生成的 Token 数上限
          </Label>
          <Input
            type='number'
            min={1}
            max={100}
            value={settings.user_max_tokens_per_submit}
            onChange={(e) =>
              patch({
                user_max_tokens_per_submit: Number(e.target.value) || 1,
              })
            }
          />
          <p className='text-xs text-muted-foreground'>
            防止用户一次自助签发大量 Token。运营侧生成号池不受此限制。
          </p>
        </div>

        <div className='flex items-start justify-between gap-4 rounded-lg border p-3'>
          <div className='space-y-0.5'>
            <Label className='text-sm font-medium'>开放用户自助贡献</Label>
            <p className='text-xs text-muted-foreground'>
              关闭后用户侧「贡献密钥」入口将不可用，号池只由运营在后台发放。
            </p>
          </div>
          <Switch
            checked={settings.allow_user_contribute}
            onCheckedChange={(v) => patch({ allow_user_contribute: v })}
          />
        </div>

        <div className='space-y-1.5'>
          <Label className='text-sm font-medium'>免费额度可用分组</Label>
          <Input
            value={settings.free_quota_allowed_groups}
            placeholder='留空表示所有分组都可用，例如：default,gy'
            onChange={(e) =>
              patch({ free_quota_allowed_groups: e.target.value })
            }
          />
          <p className='text-xs text-muted-foreground'>
            签到等奖励的免费额度只在列出的分组内消耗（逗号分隔分组名，如
            号池、公益模型组的分组 key）。白名单外的分组视为「付费分组」：
            只扣付费余额、付费不足则请求失败；号池发放的免费级 key
            也只能路由到白名单内的分组（期望分组在白名单外时自动改绑）。
          </p>
        </div>

        <div className='flex items-start justify-between gap-4 rounded-lg border p-3'>
          <div className='space-y-0.5'>
            <Label className='text-sm font-medium'>自动绑定上游模型列表</Label>
            <p className='text-xs text-muted-foreground'>
              开启时自动拉取上游 /v1/models 作为渠道可用模型。渠道模型是
              <strong>精确匹配</strong>（没有 * 通配），关闭后必须手填模型，
              否则任何模型都匹配不到。
            </p>
          </div>
          <Switch
            checked={settings.auto_bind_models}
            onCheckedChange={(v) => patch({ auto_bind_models: v })}
          />
        </div>

        <div className='flex items-start justify-between gap-4 rounded-lg border p-3'>
          <div className='space-y-0.5'>
            <Label className='text-sm font-medium'>单模型自动检查与自动下架</Label>
            <p className='text-xs text-muted-foreground'>
              开启后巡检逐个模型探测：上游明确告知「模型不存在/未授权」时自动下架该模型
              （不影响渠道和其他模型），恢复后自动重新上架。需同时开启「自动测试渠道」定时任务。
              注意：请求数等于模型数，NVIDIA 号池 85 个模型时每轮巡检约 85 个请求。
            </p>
          </div>
          <Switch
            checked={settings.model_auto_check_enabled}
            onCheckedChange={(v) => patch({ model_auto_check_enabled: v })}
          />
        </div>

        <Button onClick={save} disabled={saving}>
          {saving ? '保存中…' : '保存设置'}
        </Button>
      </CardContent>
    </Card>
  )
}

// ============================ 渠道管理 ============================

// 渠道健康度标签（拆成独立函数以避免嵌套三元）。
function channelHealth(channel: KeyPoolChannelInfo) {
  if (channel.key_count === 0) {
    return { text: '无可用密钥', variant: 'destructive' as const }
  }
  if (channel.disabled_keys > 0) {
    return {
      text: `${channel.enabled_keys}/${channel.key_count} 可用`,
      variant: 'secondary' as const,
    }
  }
  return { text: '全部可用', variant: 'default' as const }
}

function ChannelCard({
  channel,
  onChanged,
}: {
  channel: KeyPoolChannelInfo
  onChanged: () => void
}) {
  const [keys, setKeys] = useState<KeyPoolKeyInfo[] | null>(null)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<KeyPoolChannelTestResult | null>(null)

  const loadKeys = useCallback(async () => {
    try {
      setKeys(await getKeyPoolChannelKeys(channel.id))
    } catch {
      setKeys([])
    }
  }, [channel.id])

  useEffect(() => {
    loadKeys()
  }, [loadKeys])

  const toggle = async (index: number, enabled: boolean) => {
    try {
      await setKeyPoolKeyStatus(channel.id, index, enabled)
      toast.success(enabled ? '密钥已启用' : '密钥已停用')
      await loadKeys()
      onChanged()
    } catch (error) {
      toast.error(getServerErrorMessage(error, '操作失败'))
    }
  }

  const runTest = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const r = await testKeyPoolChannel(channel.id)
      setTestResult(r)
      if (r.invalid_count === 0) {
        toast.success(`全部 ${r.valid_count} 把密钥可用`)
      } else {
        toast.warning(`${r.invalid_count} 把密钥不可用，请停用它们`)
      }
      await loadKeys()
    } catch (error) {
      toast.error(getServerErrorMessage(error, '验活失败'))
    } finally {
      setTesting(false)
    }
  }

  const health = channelHealth(channel)

  return (
    <Card>
      <CardHeader>
        <div className='flex flex-wrap items-start justify-between gap-3'>
          <div>
            <CardTitle className='flex items-center gap-2 text-base'>
              <KeyRound className='h-4 w-4' /> {channel.name}
              <Badge variant={health.variant}>{health.text}</Badge>
              {channel.is_multi_key ? (
                <Badge variant='outline'>
                  多密钥 · {channel.multi_key_mode === 'random' ? '随机' : '轮询'}
                </Badge>
              ) : (
                <Badge variant='destructive'>单密钥</Badge>
              )}
            </CardTitle>
            <CardDescription className='mt-1 break-all'>
              {channel.provider_label} · {channel.base_url} · 分组 {channel.group || 'default'}
            </CardDescription>
          </div>
          <Button variant='outline' size='sm' onClick={runTest} disabled={testing}>
            <Activity className='mr-1 h-3.5 w-3.5' />
            {testing ? '验活中…' : '一键验活'}
          </Button>
        </div>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
          <div className='rounded-lg border bg-muted/40 p-3'>
            <div className='text-xs text-muted-foreground'>绑定模型</div>
            <div className='mt-1 font-semibold tabular-nums'>
              {channel.model_count} 个
            </div>
          </div>
          <div className='rounded-lg border bg-muted/40 p-3'>
            <div className='text-xs text-muted-foreground'>上游密钥</div>
            <div className='mt-1 font-semibold tabular-nums'>
              {channel.enabled_keys} / {channel.key_count}
            </div>
          </div>
          <div className='rounded-lg border bg-muted/40 p-3'>
            <div className='text-xs text-muted-foreground'>关联 Token</div>
            <div className='mt-1 font-semibold tabular-nums'>
              {channel.token_count} 个
            </div>
          </div>
          <div className='rounded-lg border bg-muted/40 p-3'>
            <div className='text-xs text-muted-foreground'>剩余额度</div>
            <div className='mt-1 font-semibold tabular-nums'>
              {fmtNum(channel.token_remain_quota)}
            </div>
          </div>
        </div>

        {channel.model_count === 0 && (
          <div className='flex items-start gap-2 rounded-md bg-amber-500/10 px-3 py-2.5 text-sm text-amber-600 dark:text-amber-400'>
            <AlertTriangle className='mt-0.5 h-4 w-4 shrink-0' />
            <span>
              该渠道没有绑定任何模型，而渠道模型是
              <strong>精确匹配</strong>（没有 * 通配），任何模型请求都会报「无可用渠道」。
              请在渠道管理里填入模型，或开启「自动绑定上游模型列表」后重新生成号池。
            </span>
          </div>
        )}

        {!channel.is_multi_key && channel.key_count > 1 && (
          <div className='flex items-start gap-2 rounded-md bg-destructive/10 px-3 py-2.5 text-sm text-destructive'>
            <AlertTriangle className='mt-0.5 h-4 w-4 shrink-0' />
            <span>
              渠道里有 {channel.key_count} 把密钥但未标记为多密钥渠道，
              整串密钥会被当成一把发给上游，请求必然全部鉴权失败。
              重启服务（启动迁移）可自动修复。
            </span>
          </div>
        )}

        {keys && keys.length > 0 && (
          <div className='space-y-2'>
            <div className='text-sm font-medium'>密钥明细（{keys.length} 把）</div>
            <div className='space-y-1.5'>
              {keys.map((k) => (
                <div
                  key={k.index}
                  className='flex items-center justify-between gap-3 rounded-lg border px-3 py-2'
                >
                  <div className='flex min-w-0 items-center gap-2'>
                    <span className='text-xs text-muted-foreground tabular-nums'>
                      #{k.index}
                    </span>
                    <code className='truncate rounded bg-muted px-2 py-0.5 text-xs'>
                      {k.key_mask}
                    </code>
                    {k.enabled ? (
                      <Badge variant='outline' className='text-emerald-600'>
                        可用
                      </Badge>
                    ) : (
                      <Badge variant='destructive'>
                        已停用{k.reason ? ` · ${k.reason}` : ''}
                      </Badge>
                    )}
                  </div>
                  <Button
                    variant={k.enabled ? 'outline' : 'secondary'}
                    size='sm'
                    onClick={() => toggle(k.index, !k.enabled)}
                  >
                    {k.enabled ? '停用' : '启用'}
                  </Button>
                </div>
              ))}
            </div>
          </div>
        )}

        {testResult && (
          <div className='space-y-2 rounded-lg border bg-muted/30 p-3'>
            <div className='flex items-center gap-2 text-sm font-medium'>
              {testResult.invalid_count === 0 ? (
                <CheckCircle2 className='h-4 w-4 text-emerald-600' />
              ) : (
                <AlertTriangle className='h-4 w-4 text-amber-600' />
              )}
              验活结果：{testResult.valid_count} 把可用 / {testResult.invalid_count}{' '}
              把不可用
            </div>
            <ul className='space-y-1'>
              {testResult.results.map((r) => (
                <li
                  key={r.index}
                  className='flex items-center justify-between gap-2 text-xs'
                >
                  <span className='text-muted-foreground tabular-nums'>
                    #{r.index}
                  </span>
                  <code className='rounded bg-muted px-1.5 py-0.5'>{r.key_mask}</code>
                  <span
                    className={
                      r.valid
                        ? 'text-emerald-600 dark:text-emerald-400'
                        : 'text-destructive'
                    }
                  >
                    {r.valid ? '可用' : r.error || '不可用'}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function ChannelsPanel({ refreshKey }: { refreshKey: number }) {
  const [channels, setChannels] = useState<KeyPoolChannelInfo[] | null>(null)

  useEffect(() => {
    getKeyPoolChannels()
      .then(setChannels)
      .catch(() => setChannels([]))
  }, [refreshKey])

  if (channels === null) {
    return <Skeleton className='h-40 w-full' />
  }
  if (channels.length === 0) {
    return (
      <Card>
        <CardContent className='py-8 text-center text-sm text-muted-foreground'>
          还没有号池渠道。用上方的「生成号池」粘贴上游密钥即可自动创建。
        </CardContent>
      </Card>
    )
  }
  return (
    <div className='space-y-4'>
      {channels.map((c) => (
        <ChannelCard key={c.id} channel={c} onChanged={() => getKeyPoolChannels().then(setChannels)} />
      ))}
    </div>
  )
}

// ============================ Token 管理 ============================

function TokensPanel() {
  const [rows, setRows] = useState<KeyPoolTokenRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize] = useState(20)
  const [keyword, setKeyword] = useState('')
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const r = await getKeyPoolTokens({ keyword, page, page_size: pageSize })
      setRows(r.items)
      setTotal(r.total)
    } catch {
      setRows([])
      setTotal(0)
    } finally {
      setLoading(false)
    }
  }, [keyword, page, pageSize])

  useEffect(() => {
    load()
  }, [load])

  const resetQuota = async (id: number) => {
    try {
      await resetKeyPoolTokenQuota(id)
      toast.success('额度已重置为每日上限')
      await load()
    } catch (error) {
      toast.error(getServerErrorMessage(error, '重置失败'))
    }
  }

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <Card>
      <CardHeader>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div>
            <CardTitle className='text-base'>号池 Token</CardTitle>
            <CardDescription>
              共 {total} 个。剩余额度为 0 表示上游已失效（每日重置时归零）。
            </CardDescription>
          </div>
          <div className='flex items-center gap-2'>
            <Input
              placeholder='搜索名称或密钥'
              value={keyword}
              onChange={(e) => {
                setKeyword(e.target.value)
                setPage(1)
              }}
              className='w-48'
            />
            <Button variant='outline' size='sm' onClick={load} disabled={loading}>
              <RefreshCw className={loading ? 'mr-1 h-3.5 w-3.5 animate-spin' : 'mr-1 h-3.5 w-3.5'} />
              刷新
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className='space-y-3'>
        {rows.length === 0 ? (
          <div className='py-8 text-center text-sm text-muted-foreground'>
            {loading ? '加载中…' : '暂无号池 Token'}
          </div>
        ) : (
          <div className='overflow-x-auto'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>名称</TableHead>
                  <TableHead>所属用户</TableHead>
                  <TableHead>密钥</TableHead>
                  <TableHead>供应商</TableHead>
                  <TableHead className='text-right'>每日额度</TableHead>
                  <TableHead className='text-right'>剩余</TableHead>
                  <TableHead className='text-right'>操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((t) => (
                  <TableRow key={t.id}>
                    <TableCell className='tabular-nums'>{t.id}</TableCell>
                    <TableCell className='max-w-40 truncate'>{t.name}</TableCell>
                    <TableCell className='max-w-32 truncate'>
                      {t.username || `用户 ${t.user_id}`}
                    </TableCell>
                    <TableCell>
                      <code className='rounded bg-muted px-1.5 py-0.5 text-xs'>
                        {t.key_mask}
                      </code>
                    </TableCell>
                    <TableCell>{t.provider_label || t.provider || '-'}</TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {fmtNum(t.daily_gift_quota)}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      <span
                        className={
                          t.remain_quota > 0
                            ? 'text-emerald-600 dark:text-emerald-400'
                            : 'text-destructive'
                        }
                      >
                        {fmtNum(t.remain_quota)}
                      </span>
                    </TableCell>
                    <TableCell className='text-right'>
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => resetQuota(t.id)}
                        disabled={t.daily_gift_quota <= 0}
                      >
                        <RotateCcw className='mr-1 h-3.5 w-3.5' />
                        补额度
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}

        {totalPages > 1 && (
          <div className='flex items-center justify-end gap-2'>
            <Button
              variant='outline'
              size='sm'
              disabled={page <= 1}
              onClick={() => setPage((p) => p - 1)}
            >
              上一页
            </Button>
            <span className='text-xs text-muted-foreground'>
              第 {page} / {totalPages} 页
            </span>
            <Button
              variant='outline'
              size='sm'
              disabled={page >= totalPages}
              onClick={() => setPage((p) => p + 1)}
            >
              下一页
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

// ============================ 面板容器 ============================

export function KeyPoolAdminPanel() {
  const [overview, setOverview] = useState<KeyPoolOverview | null>(null)
  const [refreshKey, setRefreshKey] = useState(0)
  // 生成结果提升到面板层：切换 Tab 时表单组件会被卸载，结果状态必须留在外面才不会丢。
  const [genResult, setGenResult] = useState<KeyPoolResult | null>(null)

  const refresh = useCallback(() => {
    getKeyPoolOverview()
      .then(setOverview)
      .catch(() => setOverview(null))
    setRefreshKey((k) => k + 1)
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  return (
    <div className='space-y-5'>
      <OverviewCards data={overview} />
      <KeyPoolGeneratorForm
        result={genResult}
        onResultChange={setGenResult}
        onDone={refresh}
      />
      <ChannelsPanel refreshKey={refreshKey} />
      <TokensPanel />
      <SettingsCard />
    </div>
  )
}
