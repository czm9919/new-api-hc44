/*
 * 号池功能的共享 UI 组件：表单字段、结果面板、供应商选择器。
 * 单独成文件是为了避免 index.tsx ↔ admin-panel.tsx 的循环依赖。
 */
import type { ReactNode } from 'react'
import { Copy, CheckCircle2, XCircle, RefreshCw, ExternalLink } from 'lucide-react'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

import type { KeyPoolProvider, KeyPoolResult } from './api'
import { PROVIDER_LABEL, fmtQuota, providerLabel, providerSignup } from './utils'

// ============ 通用小组件 ============

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className='space-y-1.5'>
      <Label className='text-sm font-medium'>{label}</Label>
      {children}
      {hint && <p className='text-xs text-muted-foreground'>{hint}</p>}
    </div>
  )
}

// ============ 结果面板 ============

export function ResultPanel({ result, onDone }: { result: KeyPoolResult; onDone?: () => void }) {
  const tokens =
    result.tokens ??
    (result.token_keys ?? []).map((k) => ({
      key: k,
      remain_quota: 0,
      daily_gift_quota: 0,
      free_quota: 0,
      paid_quota: 0,
    }))
  const nothingCreated = (result.valid_count ?? 0) === 0 && (result.tokens_created ?? 0) === 0
  // 渠道实际绑定的模型：渠道模型是精确匹配，这里把结果显式展示出来，方便确认号池真的可用。
  const boundModels = (result.models ?? '').split(',').filter(Boolean)

  // 全部密钥验活失败：不展示「创建成功」，改为醒目失败提示，避免误导。
  if (nothingCreated) {
    return (
      <Card className='mt-4 border-destructive/40'>
        <CardHeader>
          <CardTitle className='flex items-center gap-2 text-base text-destructive'>
            <XCircle className='h-5 w-5' /> 未生成号池
          </CardTitle>
          <CardDescription>{result.message || '没有通过验活的有效密钥。'}</CardDescription>
        </CardHeader>
        <CardContent className='space-y-4'>
          {(result.invalid_keys ?? []).length > 0 && (
            <div className='rounded-lg border border-destructive/30 bg-destructive/5 p-3'>
              <div className='mb-1 flex items-center gap-1.5 text-sm font-medium text-destructive'>
                <XCircle className='h-4 w-4' /> 以下密钥未通过验活（已脱敏）
              </div>
              <div className='flex flex-wrap gap-2'>
                {(result.invalid_keys ?? []).map((k) => (
                  <code key={k} className='rounded bg-background px-2 py-1 text-xs'>
                    {k}
                  </code>
                ))}
              </div>
            </div>
          )}
          <p className='text-xs text-muted-foreground'>
            请确认密钥是否正确、是否已过期，或前往供应商控制台检查密钥权限后重试。
          </p>
          {onDone && (
            <Button variant='outline' onClick={onDone}>
              <RefreshCw className='mr-1 h-4 w-4' /> 返回重新填写
            </Button>
          )}
        </CardContent>
      </Card>
    )
  }

  return (
    <Card className='mt-4 border-emerald-500/30'>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <CheckCircle2 className='h-5 w-5 text-emerald-600' /> 号池创建成功
        </CardTitle>
        <CardDescription>{result.message}</CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
          <div className='rounded-lg border bg-muted/40 p-3'>
            <div className='text-xs text-muted-foreground'>号池渠道</div>
            <div className='mt-1 font-semibold'>{result.pool_channel_name || '—'}</div>
          </div>
          <div className='rounded-lg border bg-muted/40 p-3'>
            <div className='text-xs text-muted-foreground'>有效密钥</div>
            <div className='mt-1 font-semibold text-emerald-600'>{result.valid_count}</div>
          </div>
          <div className='rounded-lg border bg-muted/40 p-3'>
            <div className='text-xs text-muted-foreground'>无效密钥</div>
            {/* invalid count */}
            <div className='mt-1 font-semibold text-destructive'>{(result.invalid_keys ?? []).length}</div>
          </div>
          <div className='rounded-lg border bg-muted/40 p-3'>
            <div className='text-xs text-muted-foreground'>生成 Token</div>
            <div className='mt-1 font-semibold'>{result.tokens_created}</div>
          </div>
        </div>

        {boundModels.length > 0 && (
          <div className='rounded-lg border bg-muted/40 p-3'>
            <div className='mb-1 text-xs text-muted-foreground'>渠道已绑定模型（{boundModels.length} 个）</div>
            <div className='break-all text-xs'>
              {boundModels.slice(0, 24).join('、')}
              {boundModels.length > 24 ? ` 等 ${boundModels.length} 个` : ''}
            </div>
          </div>
        )}

        {(result.invalid_keys ?? []).length > 0 && (
          <div className='rounded-lg border border-destructive/30 bg-destructive/5 p-3'>
            <div className='mb-1 flex items-center gap-1.5 text-sm font-medium text-destructive'>
              <XCircle className='h-4 w-4' /> 以下密钥未通过验活（已跳过，密钥已脱敏）
            </div>
            <div className='flex flex-wrap gap-2'>
              {(result.invalid_keys ?? []).map((k) => (
                <code key={k} className='rounded bg-background px-2 py-1 text-xs'>
                  {k}
                </code>
              ))}
            </div>
          </div>
        )}

        {tokens.length > 0 ? (
          <div>
            <div className='mb-2 text-sm font-medium'>生成的号池 Token（请复制保存，仅展示一次）</div>
            <ul className='space-y-2'>
              {tokens.map((tk) => (
                <li key={tk.key} className='rounded-lg border p-3'>
                  <div className='flex items-center gap-2'>
                    <code className='min-w-0 flex-1 break-all rounded bg-muted px-2 py-1.5 text-xs'>{tk.key}</code>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={() => {
                        navigator.clipboard?.writeText(tk.key)
                        toast.success('已复制 Token')
                      }}
                    >
                      <Copy className='mr-1 h-3.5 w-3.5' /> 复制
                    </Button>
                  </div>
                  <div className='mt-2 flex flex-wrap gap-x-6 gap-y-1 text-xs text-muted-foreground'>
                    <span className='text-emerald-600 dark:text-emerald-400'>
                      免费余额：<b className='tabular-nums'>{fmtQuota(tk.free_quota ?? 0)}</b>
                    </span>
                    <span>
                      付费余额：<b className='tabular-nums text-foreground'>{fmtQuota((tk.remain_quota ?? 0) - (tk.free_quota ?? 0))}</b>
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          </div>
        ) : (
          <div className='rounded-lg border bg-muted/40 px-3 py-2.5 text-sm text-muted-foreground'>
            本次未新增 Token：密钥已并入号池提升容量，你原有的号池额度继续有效。
          </div>
        )}

        {onDone && (
          <Button variant='outline' onClick={onDone}>
            <RefreshCw className='mr-1 h-4 w-4' /> 再创建一批
          </Button>
        )}
      </CardContent>
    </Card>
  )
}

// 各供应商对应的号池渠道名称
const POOL_CHANNEL_NAMES: Record<string, string> = {
  nvidia: '英伟达号池',
  sensenova: '商汤号池',
}

// 供应商下拉 + 一键跳转注册
export function ProviderField({
  providers,
  value,
  onChange,
}: {
  providers: KeyPoolProvider[]
  value: string
  onChange: (v: string) => void
}) {
  const list = providers.length
    ? providers
    : Object.keys(PROVIDER_LABEL).map((id) => ({ id, label: providerLabel(id) }))
  const current = list.find((p) => p.id === value)
  const poolName = POOL_CHANNEL_NAMES[value] ?? '号池'
  return (
    <div className='space-y-1.5'>
      <div className='flex items-center justify-between'>
        <Label className='text-sm font-medium'>上游供应商</Label>
        <a
          href={providerSignup(value)}
          target='_blank'
          rel='noreferrer'
          className='inline-flex items-center gap-1 text-xs text-primary hover:underline'
        >
          <ExternalLink className='h-3 w-3' /> 一键跳转注册 / 申请密钥
        </a>
      </div>
      <Select value={value} onValueChange={(v) => v && onChange(v)}>
        <SelectTrigger>
          <SelectValue>{current?.label ?? providerLabel(value)}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          {list.map((p) => (
            <SelectItem key={p.id} value={p.id}>
              {p.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {current && (
        <p className='text-xs text-muted-foreground'>
          贡献的密钥会逐个验活，有效密钥自动合并进「{current.label}」对应的号池渠道（{poolName}）。
        </p>
      )}
    </div>
  )
}

