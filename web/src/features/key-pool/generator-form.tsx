/*
 * 「生成号池」表单（纯内容，不含页面布局）。
 * 嵌在号池运营面板里使用；结果状态由调用方持有，切换 Tab 时不会丢失。
 */
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Plus } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { generateKeyPool, getKeyPoolProviders, type KeyPoolProvider, type KeyPoolResult } from './api'
import { Field, ProviderField, ResultPanel } from './shared'

// ============ 管理端：批量生成 ============

/**
 * 「生成号池」表单（纯内容，不含页面布局）。
 * 嵌在号池运营面板里使用，不再作为独立页面。
 */
export function KeyPoolGeneratorForm({
  result,
  onResultChange,
  onDone,
}: {
  result: KeyPoolResult | null
  onResultChange: (r: KeyPoolResult | null) => void
  onDone?: () => void
}) {
  const [providers, setProviders] = useState<KeyPoolProvider[]>([])
  const [provider, setProvider] = useState('sensenova')
  const [keys, setKeys] = useState('')
  const [userId, setUserId] = useState('1')
  const [tokenCount, setTokenCount] = useState('1')
  const [quota, setQuota] = useState('500000')
  const [dailyGift, setDailyGift] = useState('50000')
  const [models, setModels] = useState('*')
  const [group, setGroup] = useState('default')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    getKeyPoolProviders()
      .then((p) => setProviders(p))
      .catch(() => {})
  }, [])

  const onSubmit = async () => {
    if (!keys.trim()) {
      toast.error('请至少粘贴一个上游密钥')
      return
    }
    setLoading(true)
    try {
      const res = await generateKeyPool({
        provider,
        keys,
        user_id: Number(userId),
        token_count: Number(tokenCount),
        quota_per_token: Number(quota),
        daily_gift_quota: Number(dailyGift),
        models,
        group,
      })
      onResultChange(res)
      onDone?.()
      toast.success('号池已生成')
    } catch (error) {
      toast.error(getServerErrorMessage(error, '号池生成失败'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Plus className='h-4 w-4' /> 生成号池
        </CardTitle>
        <CardDescription>
          粘贴上游密钥，系统验活后合并进对应供应商的号池渠道，并为指定用户生成固定额度的号池 Token。
          支持商汤 / 英伟达等上游；每行一个密钥，无效密钥会被自动跳过。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        {result && (
          <ResultPanel result={result} onDone={() => onResultChange(null)} />
        )}
        {!result && (
          <>
            <div className='grid grid-cols-1 gap-4 sm:grid-cols-2'>
              <ProviderField providers={providers} value={provider} onChange={setProvider} />
              <Field label='目标用户 ID' hint='号池 Token 归属的用户'>
                <Input value={userId} onChange={(e) => setUserId(e.target.value)} type='number' />
              </Field>
              <Field label='Token 数量' hint='本次生成的号池 Token 个数'>
                <Input value={tokenCount} onChange={(e) => setTokenCount(e.target.value)} type='number' />
              </Field>
              <Field label='每个 Token 固定额度' hint='运营侧发放的固定余额，用户侧不可自助设置'>
                <Input value={quota} onChange={(e) => setQuota(e.target.value)} type='number' />
              </Field>
              <Field label='每个 Token 每日赠送额度' hint='每日 00:00（北京时间）重置回该值；0 表示不赠送'>
                <Input value={dailyGift} onChange={(e) => setDailyGift(e.target.value)} type='number' />
              </Field>
              <Field label='模型' hint='留空或填 * 表示自动从该供应商拉取模型列表；渠道模型是精确匹配，没有 * 通配'>
                <Input value={models} onChange={(e) => setModels(e.target.value)} placeholder='*' />
              </Field>
              <Field label='分组' hint='Token 与渠道共用，用于路由'>
                <Input value={group} onChange={(e) => setGroup(e.target.value)} />
              </Field>
            </div>
            <Field label='上游密钥（每行一个）' hint='系统会逐个验活；仅有效密钥合并进号池渠道'>
              <Textarea rows={6} value={keys} onChange={(e) => setKeys(e.target.value)} placeholder={'sk-xxxx\nsk-yyyy'} />
            </Field>
            <Button onClick={onSubmit} disabled={loading}>
              <Plus className='mr-1 h-4 w-4' /> {loading ? '生成中…' : '生成号池'}
            </Button>
          </>
        )}
      </CardContent>
    </Card>
  )
}

