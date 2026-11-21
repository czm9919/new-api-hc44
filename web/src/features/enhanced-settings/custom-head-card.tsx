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
 * 自定义 Head 代码（管理员全局生效，hc24）。
 *
 * 解决的是「平台要求把一段标签放在 </head> 之前，但后台没地方填」的问题：
 *  - 「系统设置 → 站点 → 首页内容」(HomePageContent) 渲染的是页面正文区的
 *    Markdown，写 <meta>/<script> 既进不了 <head> 也不会执行，所以怎么填都没用；
 *  - 上游只支持 UMAMI / GOOGLE_ANALYTICS 两个环境变量，改配置还得重启。
 *
 * 本卡片把片段存进 option `CustomHeadHTML`，服务端在返回 index.html 时注入到
 * 最后一个 </head> 之前，改完刷新页面即生效（详见 common/custom_head.go）。
 */
import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import { ExternalLink, RotateCcw, Tags } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import { optionValueOf } from './option-utils'
import { describeSaveFailure } from './save-error'

export const CUSTOM_HEAD_OPTION_KEY = 'CustomHeadHTML'

/** 与后端 common.ValidateCustomHeadHTML 保持一致的前置校验，省一次往返。 */
function validateDraft(value: string): string | null {
  if (value.length > 64 * 1024) {
    return '内容过长（上限 64KB），请精简后再保存'
  }
  const lower = value.toLowerCase()
  if (lower.includes('<html')) {
    return '只填要插入 <head> 的片段，不要贴整个 <html> 文档'
  }
  if (lower.includes('</head>')) {
    return '片段会自动插入到 </head> 之前，不用自己再写 </head>'
  }
  return null
}

export function CustomHeadCard() {
  const [draft, setDraft] = useState<string | null>(null)
  const [saved, setSaved] = useState('')
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const res = await api.get('/api/option/')
      // 后端返回的是 [{key,value}] 数组，按 key 取要用 optionValueOf 归一化
      const normalized = optionValueOf(res.data, CUSTOM_HEAD_OPTION_KEY)
      setDraft(normalized)
      setSaved(normalized.trim())
    } catch {
      setDraft('')
      setSaved('')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const save = async (nextValue?: string) => {
    if (draft === null) return
    const value = nextValue ?? draft
    const error = validateDraft(value)
    if (error) {
      toast.error(error)
      return
    }
    setSaving(true)
    try {
      const res = await api.put('/api/option/', {
        key: CUSTOM_HEAD_OPTION_KEY,
        value,
      })
      // 业务失败走 200 + success:false，requireServerSuccess 会带上服务端文案抛出
      requireServerSuccess(res.data)
      setDraft(value)
      setSaved(value.trim())
      toast.success(
        value.trim()
          ? '已保存，刷新前台页面即可看到效果'
          : '已清空自定义 Head 代码'
      )
    } catch (error) {
      toast.error(describeSaveFailure(error, '自定义 Head 代码保存失败'))
    } finally {
      setSaving(false)
    }
  }

  const dirty = draft !== null && draft.trim() !== saved

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Tags className='h-4 w-4' /> 自定义 Head 代码
        </CardTitle>
        <CardDescription>
          内容会原样插入到前台首页 HTML 的 <code>&lt;/head&gt;</code>{' '}
          之前，对所有页面生效。
          适合站长平台验证标签、统计脚本、第三方客服/挂件这类必须放在 head
          里的代码。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-3'>
        {draft === null ? (
          <Skeleton className='h-40 w-full' />
        ) : (
          <>
            <div className='bg-muted/40 text-muted-foreground rounded-lg border px-3 py-2 text-xs'>
              注意：「系统设置 → 站点 →
              首页内容」渲染的是页面正文（Markdown），把
              <code className='mx-1'>&lt;meta&gt;</code>/
              <code className='mx-1'>&lt;script&gt;</code>
              写在那里不会进 head、也不会执行，这类需求请统一填在这里。
            </div>

            <div className='space-y-1.5'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <Label className='text-sm font-medium'>head 片段</Label>
                <span className='text-muted-foreground text-xs'>
                  {saved ? '当前已配置' : '当前未配置'} · {draft.length} 字符
                </span>
              </div>
              <Textarea
                className='min-h-40 font-mono text-xs'
                spellCheck={false}
                placeholder='<meta name="google-site-verification" content="..." />'
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
              />
              <p className='text-muted-foreground text-xs'>
                支持多行、多条标签。保存后前台刷新即生效；写错的代码会直接影响页面，
                建议先在空白环境验证。
              </p>
            </div>

            <div className='flex flex-col-reverse gap-2 border-t pt-4 sm:flex-row sm:items-center sm:justify-between'>
              <Button
                variant='ghost'
                onClick={() => window.open('/', '_blank', 'noopener')}
              >
                <ExternalLink className='mr-1 h-4 w-4' /> 打开前台查看
              </Button>
              <div className='flex flex-col-reverse gap-2 sm:flex-row'>
                <Button
                  variant='outline'
                  disabled={saving || !saved}
                  onClick={() => void save('')}
                >
                  <RotateCcw className='mr-1 h-4 w-4' /> 清空
                </Button>
                <Button onClick={() => void save()} disabled={saving || !dirty}>
                  {saving ? '保存中…' : '保存 Head 代码'}
                </Button>
              </div>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
