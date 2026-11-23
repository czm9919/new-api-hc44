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
 * 加速专线运营配置卡片（hc50 · P2）。
 *
 * 四项都在 `accel_setting` 命名空间下，走通用的 /api/option/ 接口按
 * `accel_setting.<字段>` 单键保存，后端 handleConfigUpdate 会实时写进
 * GlobalConfig，无需重启（也不用像号池那样单独开一组接口）。
 *
 * 为什么不把这些放到 /accel 页：加速页是**用户可见**的，把「槽位 TTL」
 * 这类内部参数摆在付费页上只会让人看不懂。运营配置统一归到扩展设置。
 */
import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Zap } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import { optionValueOf } from './option-utils'
import { describeSaveFailure } from './save-error'

export const ACCEL_OPTION_PREFIX = 'accel_setting.'

interface AccelForm {
  ingressHost: string
  slotTTLSeconds: string
  lineDisplayEnabled: boolean
  lineDegradedRate: string
  globalConcurrency: string
}

const EMPTY_FORM: AccelForm = {
  ingressHost: '',
  slotTTLSeconds: '600',
  lineDisplayEnabled: true,
  lineDegradedRate: '50',
  globalConcurrency: '0',
}

function parseForm(payload: unknown): AccelForm {
  const enabled = optionValueOf(payload, `${ACCEL_OPTION_PREFIX}line_display_enabled`)
  const ttl = optionValueOf(payload, `${ACCEL_OPTION_PREFIX}slot_ttl_seconds`)
  const rate = optionValueOf(payload, `${ACCEL_OPTION_PREFIX}line_degraded_rate`)
  const global = optionValueOf(payload, `${ACCEL_OPTION_PREFIX}global_concurrency`)
  return {
    ingressHost: optionValueOf(payload, `${ACCEL_OPTION_PREFIX}ingress_host`),
    slotTTLSeconds: ttl || EMPTY_FORM.slotTTLSeconds,
    // 缺省视为开启：新增配置项在未保存过时不应把已有功能关掉
    lineDisplayEnabled: enabled === '' ? true : enabled === 'true',
    lineDegradedRate: rate || EMPTY_FORM.lineDegradedRate,
    globalConcurrency: global || EMPTY_FORM.globalConcurrency,
  }
}

export function AccelLineCard() {
  const [form, setForm] = useState<AccelForm | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const res = await api.get('/api/option/')
      setForm(parseForm(res.data))
    } catch {
      setForm({ ...EMPTY_FORM })
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const patch = (next: Partial<AccelForm>) => {
    setForm((prev) => (prev ? { ...prev, ...next } : prev))
  }

  const save = async () => {
    if (!form) return
    const ttl = Number(form.slotTTLSeconds)
    if (!Number.isFinite(ttl) || ttl < 30 || ttl > 86400) {
      toast.error('槽位 TTL 需在 30 到 86400 秒之间')
      return
    }
    const rate = Number(form.lineDegradedRate)
    if (!Number.isFinite(rate) || rate <= 0 || rate > 100) {
      toast.error('异常线路阈值需在 1 到 100 之间')
      return
    }
    const global = Number(form.globalConcurrency)
    if (!Number.isFinite(global) || global < 0 || global > 100000) {
      toast.error('全域并发上限需在 0 到 100000 之间（0 表示不限）')
      return
    }
    setSaving(true)
    try {
      const entries: Array<[string, string]> = [
        ['ingress_host', form.ingressHost.trim()],
        ['slot_ttl_seconds', String(Math.round(ttl))],
        ['line_display_enabled', form.lineDisplayEnabled ? 'true' : 'false'],
        ['line_degraded_rate', String(Math.round(rate))],
        ['global_concurrency', String(Math.round(global))],
      ]
      // 一次 PUT 只能写一个键，顺序提交；任一失败即中断并提示，
      // 避免出现「域名写进去了但节奏参数没写」的半成品状态。
      for (const [key, value] of entries) {
        const res = await api.put('/api/option/', {
          key: `${ACCEL_OPTION_PREFIX}${key}`,
          value,
        })
        requireServerSuccess(res.data)
      }
      toast.success('加速专线配置已保存')
    } catch (error) {
      toast.error(describeSaveFailure(error, '加速专线配置保存失败'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Zap className='h-4 w-4' /> 加速专线
        </CardTitle>
          <CardDescription>
          加速链接是套了 CDN 的门牌：同一个站、同一套渠道、同一计费，只有入口域名不同。
          这里配置加速域名与滥用兜底，调度和渠道完全不受影响。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        {!form ? (
          <Skeleton className='h-40 w-full' />
        ) : (
          <>
            <div className='space-y-1.5'>
              <Label className='text-sm font-medium'>入站加速域名</Label>
              <Input
                value={form.ingressHost}
                placeholder='accel.example.com'
                onChange={(e) => patch({ ingressHost: e.target.value })}
              />
              <p className='text-muted-foreground text-xs'>
                域名要能直达本站并保留 Host 头（CDN 回源请保持原始 Host）。填好后加速页会向
                有订阅的用户下发带个人水印前缀的专属地址；留空则整块不展示。
              </p>
            </div>

            <div className='flex items-start justify-between gap-4 rounded-lg border p-3'>
              <div className='space-y-0.5'>
                <Label className='text-sm font-medium'>向用户展示线路状态</Label>
                <p className='text-muted-foreground text-xs'>
                  关闭后用户端不显示线路健康度，管理端仍可通过套餐行的「线路」查看。
                  刚重启、健康样本为空时建议先关掉再开。
                </p>
              </div>
              <Switch
                checked={form.lineDisplayEnabled}
                onCheckedChange={(v) => patch({ lineDisplayEnabled: v })}
              />
            </div>

            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='space-y-1.5'>
                <Label className='text-sm font-medium'>异常线路阈值（%）</Label>
                <Input
                  type='number'
                  min={1}
                  max={100}
                  value={form.lineDegradedRate}
                  onChange={(e) => patch({ lineDegradedRate: e.target.value })}
                />
                <p className='text-muted-foreground text-xs'>
                  样本充足且成功率低于该值时标红，默认 50。
                </p>
              </div>
              <div className='space-y-1.5'>
                <Label className='text-sm font-medium'>并发槽位 TTL（秒）</Label>
                <Input
                  type='number'
                  min={30}
                  max={86400}
                  value={form.slotTTLSeconds}
                  onChange={(e) => patch({ slotTTLSeconds: e.target.value })}
                />
                <p className='text-muted-foreground text-xs'>
                  仅 Redis 计数生效：进程被强杀时靠它自动回收槽位。配了
                  REDIS_CONN_STRING 才会跨实例共享并发上限。
                </p>
              </div>
            </div>

            <div className='space-y-1.5'>
              <Label className='text-sm font-medium'>全域并发上限（0 = 不限）</Label>
              <Input
                type='number'
                min={0}
                max={100000}
                value={form.globalConcurrency}
                onChange={(e) => patch({ globalConcurrency: e.target.value })}
              />
              <p className='text-muted-foreground text-xs'>
                所有加速用户共享的并发总量。域名一旦被传播出去，超出的请求会直接
                429 而不是打满上游——相当于给泄露风险买了个止损。
              </p>
            </div>

            <Button onClick={() => void save()} disabled={saving}>
              {saving ? '保存中...' : '保存加速设置'}
            </Button>
          </>
        )}
      </CardContent>
    </Card>
  )
}
