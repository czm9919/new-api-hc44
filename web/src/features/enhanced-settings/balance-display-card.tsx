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
 * 余额显示配置卡片（管理员全局生效，hc23）。
 *
 * 用户管理列表原本只有一个「可用余额」合计值（= user.quota），而 quota 里其实混着
 * 签到赠送的免费额度（user.free_quota）。管理员要判断某个用户还剩多少真金白银，
 * 就得自己减一遍。这里把「付费余额 / 免费额度」的拆分展示做成可配置项：
 *  - 总开关：关掉后回到单个合计值（升级前的观感）；
 *  - 自定义显示名：不同站点对这两笔钱的叫法不一样（充值余额 / 赠送额度 等）；
 *  - 单独隐藏免费额度：只想看付费余额时不用关掉整个拆分；
 *  - 是否在用户管理列表生效：列表行高敏感，允许和别处区别对待。
 *
 * 配置持久化在 option `BalanceDisplaySetting`（JSON），由 useBalanceDisplay 读取。
 * 与下方号池设置走的是不同接口、不同保存按钮，互不影响。
 */
import { useCallback, useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { RotateCcw, Wallet } from 'lucide-react'

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
import {
  parseBalanceDisplay,
  serializeBalanceDisplay,
  type BalanceDisplayConfig,
} from '@/hooks/use-balance-display'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import { optionValueOf } from './option-utils'
import { describeSaveFailure } from './save-error'

export const BALANCE_DISPLAY_OPTION_KEY = 'BalanceDisplaySetting'

function SettingSwitch(props: {
  label: string
  description: string
  checked: boolean
  disabled?: boolean
  onChange: (next: boolean) => void
}) {
  return (
    <div className='flex items-start justify-between gap-4 rounded-lg border p-3'>
      <div className='space-y-0.5'>
        <Label className='text-sm font-medium'>{props.label}</Label>
        <p className='text-xs text-muted-foreground'>{props.description}</p>
      </div>
      <Switch
        checked={props.checked}
        disabled={props.disabled}
        onCheckedChange={props.onChange}
      />
    </div>
  )
}

/** 预览文案：按当前草稿模拟用户管理列表余额列的展示效果。 */
function buildPreview(config: BalanceDisplayConfig): string {
  const paid = config.paidLabel.trim() || '付费余额'
  const free = config.freeLabel.trim() || '免费额度'
  if (!config.splitEnabled || !config.showInUserList) {
    return '可用余额 0.0038；已用 0.0022'
  }
  if (config.hideFree) {
    return `${paid} 0.0038；已用 0.0022`
  }
  return `${paid} 0.0038；${free} 0.0000；已用 0.0022`
}

export function BalanceDisplayCard() {
  const queryClient = useQueryClient()
  const [config, setConfig] = useState<BalanceDisplayConfig | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const res = await api.get('/api/option/')
      // 后端返回 [{key,value}] 数组，之前按 map 取 key 永远是 undefined，
      // 于是界面一直显示默认配置，看着像「保存没生效」。
      setConfig(parseBalanceDisplay(optionValueOf(res.data, BALANCE_DISPLAY_OPTION_KEY)))
    } catch {
      setConfig(parseBalanceDisplay(''))
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const patch = (next: Partial<BalanceDisplayConfig>) => {
    setConfig((prev) => (prev ? { ...prev, ...next } : prev))
  }

  const save = async () => {
    if (!config) return
    setSaving(true)
    try {
      const res = await api.put('/api/option/', {
        key: BALANCE_DISPLAY_OPTION_KEY,
        value: serializeBalanceDisplay(config),
      })
      requireServerSuccess(res.data)
      // 用户管理列表读的是 /api/status 里的配置快照，失效后才会重新拉取
      queryClient.invalidateQueries({ queryKey: ['status'] })
      try {
        window.localStorage.removeItem('status')
      } catch {
        /* localStorage 可能被禁用 */
      }
      toast.success('余额显示配置已保存')
    } catch (error) {
      toast.error(describeSaveFailure(error, '余额显示配置保存失败'))
    } finally {
      setSaving(false)
    }
  }

  const reset = () => {
    setConfig(parseBalanceDisplay(''))
    toast.success('已恢复默认显示（记得点保存）')
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Wallet className='h-4 w-4' /> 余额显示
        </CardTitle>
        <CardDescription>
          用户管理列表里把「可用余额」拆成付费余额与免费额度：付费余额 =
          总余额 − 免费额度（签到等赠送的部分），两者都从同一个余额池里扣。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        {!config ? (
          <Skeleton className='h-40 w-full' />
        ) : (
          <>
            <SettingSwitch
              label='拆分显示付费余额与免费额度'
              description='关闭后回到单个「可用余额」合计值，下面的名称与开关都不再生效。'
              checked={config.splitEnabled}
              onChange={(v) => patch({ splitEnabled: v })}
            />

            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='space-y-1.5'>
                <Label className='text-sm font-medium'>付费余额显示名</Label>
                <Input
                  value={config.paidLabel}
                  disabled={!config.splitEnabled}
                  placeholder='默认：付费余额'
                  onChange={(e) => patch({ paidLabel: e.target.value })}
                />
              </div>
              <div className='space-y-1.5'>
                <Label className='text-sm font-medium'>免费额度显示名</Label>
                <Input
                  value={config.freeLabel}
                  disabled={!config.splitEnabled}
                  placeholder='默认：免费额度'
                  onChange={(e) => patch({ freeLabel: e.target.value })}
                />
              </div>
            </div>
            <p className='text-xs text-muted-foreground'>
              留空使用默认文案（随界面语言切换）；填了内容则按填写的字面显示。
            </p>

            <SettingSwitch
              label='在「用户管理」列表拆分显示'
              description='关闭后该列表仍显示合计余额，其它位置不受影响。'
              checked={config.showInUserList}
              disabled={!config.splitEnabled}
              onChange={(v) => patch({ showInUserList: v })}
            />

            <SettingSwitch
              label='隐藏免费额度'
              description='只展示付费余额和已用额度；免费额度仍计入总额，只是不单独显示。'
              checked={config.hideFree}
              disabled={!config.splitEnabled || !config.showInUserList}
              onChange={(v) => patch({ hideFree: v })}
            />

            <div className='rounded-lg border bg-muted/40 px-3 py-2 text-xs text-muted-foreground'>
              效果预览（用户管理列表余额列）：{buildPreview(config)}
            </div>

            <div className='flex flex-col-reverse gap-2 border-t pt-4 sm:flex-row sm:justify-end'>
              <Button variant='outline' onClick={reset} disabled={saving}>
                <RotateCcw className='mr-1 h-4 w-4' /> 恢复默认
              </Button>
              <Button onClick={save} disabled={saving}>
                {saving ? '保存中…' : '保存余额显示配置'}
              </Button>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
