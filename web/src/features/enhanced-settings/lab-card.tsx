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
import { FlaskConical, RotateCcw } from 'lucide-react'
/*
 * 自研实验室配置卡片（/lab 展示页的数据源，hc28）。
 *
 * /lab 是「纯展示 + 求赞助」的公开页，与主站路由/计费/模型列表完全隔离：
 * 这里填的内容只影响展示，不会让该模型进入任何业务链路。
 *
 * 配置持久化在 option `LabSetting`（JSON），公开接口 /api/lab/public/config
 * 原样下发；推理状态可选地经 /api/lab/public/status 服务端代理轮询
 * status_url，避开 CORS（status_url 允许内网地址，因为推理服务常与主站同机）。
 */
import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'

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
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import { optionValueOf } from './option-utils'
import { describeSaveFailure } from './save-error'

export const LAB_OPTION_KEY = 'LabSetting'

interface LabCardConfig {
  enabled: boolean
  modelName: string
  tagline: string
  intro: string
  params: string
  hardware: string
  contextTokens: string
  speedTps: string
  statusUrl: string
  demoUrl: string
  demoEmbed: boolean
  timelineText: string
  sponsorTarget: string
  sponsorRaised: string
  sponsorCurrency: string
  sponsorNote: string
  methodsText: string
  acknowledgementsText: string
}

const DEFAULT_CONFIG: LabCardConfig = {
  enabled: false,
  modelName: '',
  tagline: '',
  intro: '',
  params: '',
  hardware: '',
  contextTokens: '',
  speedTps: '',
  statusUrl: '',
  demoUrl: '',
  demoEmbed: false,
  timelineText: '',
  sponsorTarget: '',
  sponsorRaised: '',
  sponsorCurrency: 'CNY',
  sponsorNote: '',
  methodsText: '',
  acknowledgementsText: '',
}

/** 时间线每行格式：日期 | 标题 | 描述（日期与描述可省略）。 */
function parseTimeline(text: string) {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const [date = '', title = '', detail = ''] = line.split('|')
      return {
        date: date.trim() || undefined,
        title: title.trim(),
        detail: detail.trim() || undefined,
      }
    })
    .filter((entry) => entry.title)
}

/** 赞助方式每行格式：名称 | 链接 | 二维码图片地址（链接与二维码可省略）。 */
function parseMethods(text: string) {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const [name = '', url = '', qrcode = ''] = line.split('|')
      return {
        name: name.trim(),
        url: url.trim() || undefined,
        qrcode_url: qrcode.trim() || undefined,
      }
    })
    .filter((method) => method.name)
}

function parseConfig(raw: string): LabCardConfig {
  if (!raw) {
    return { ...DEFAULT_CONFIG }
  }
  try {
    const data = JSON.parse(raw) as Record<string, unknown>
    const sponsor = (data.sponsor || {}) as Record<string, unknown>
    const methods = Array.isArray(sponsor.methods) ? sponsor.methods : []
    const timeline = Array.isArray(data.timeline) ? data.timeline : []
    const acks = Array.isArray(data.acknowledgements)
      ? data.acknowledgements
      : []
    return {
      enabled: data.enabled !== false,
      modelName: String(data.model_name || ''),
      tagline: String(data.tagline || ''),
      intro: String(data.intro || ''),
      params: String(data.params || ''),
      hardware: String(data.hardware || ''),
      contextTokens: data.context_tokens ? String(data.context_tokens) : '',
      speedTps: data.speed_tps ? String(data.speed_tps) : '',
      statusUrl: String(data.status_url || ''),
      demoUrl: String(data.demo_url || ''),
      demoEmbed: data.demo_embed === true,
      timelineText: timeline
        .map((entry) => {
          const e = entry as Record<string, unknown>
          return [e.date, e.title, e.detail]
            .map((v) => String(v || ''))
            .join(' | ')
        })
        .join('\n'),
      sponsorTarget: sponsor.target ? String(sponsor.target) : '',
      sponsorRaised: sponsor.raised ? String(sponsor.raised) : '',
      sponsorCurrency: String(sponsor.currency || 'CNY'),
      sponsorNote: String(sponsor.note || ''),
      methodsText: methods
        .map((method) => {
          const m = method as Record<string, unknown>
          return [m.name, m.url, m.qrcode_url]
            .map((v) => String(v || ''))
            .join(' | ')
        })
        .join('\n'),
      acknowledgementsText: acks.map((v) => String(v)).join('\n'),
    }
  } catch {
    return { ...DEFAULT_CONFIG }
  }
}

function serializeConfig(config: LabCardConfig): string {
  const toNumber = (value: string) => {
    const parsed = Number(value)
    return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined
  }
  const payload: Record<string, unknown> = {
    enabled: config.enabled,
    model_name: config.modelName.trim(),
  }
  if (config.tagline.trim()) payload.tagline = config.tagline.trim()
  if (config.intro.trim()) payload.intro = config.intro.trim()
  if (config.params.trim()) payload.params = config.params.trim()
  if (config.hardware.trim()) payload.hardware = config.hardware.trim()
  const contextTokens = toNumber(config.contextTokens)
  if (contextTokens) payload.context_tokens = contextTokens
  const speedTps = toNumber(config.speedTps)
  if (speedTps) payload.speed_tps = speedTps
  if (config.statusUrl.trim()) payload.status_url = config.statusUrl.trim()
  if (config.demoUrl.trim()) payload.demo_url = config.demoUrl.trim()
  if (config.demoEmbed) payload.demo_embed = true
  const timeline = parseTimeline(config.timelineText)
  if (timeline.length > 0) payload.timeline = timeline
  const target = toNumber(config.sponsorTarget)
  const raised = toNumber(config.sponsorRaised)
  const methods = parseMethods(config.methodsText)
  if (target || raised || methods.length > 0 || config.sponsorNote.trim()) {
    const sponsor: Record<string, unknown> = {
      currency: config.sponsorCurrency.trim() || 'CNY',
    }
    if (target) sponsor.target = target
    if (raised) sponsor.raised = raised
    if (config.sponsorNote.trim()) sponsor.note = config.sponsorNote.trim()
    if (methods.length > 0) sponsor.methods = methods
    payload.sponsor = sponsor
  }
  const acknowledgements = config.acknowledgementsText
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
  if (acknowledgements.length > 0) payload.acknowledgements = acknowledgements
  return JSON.stringify(payload)
}

function Field(props: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <div className='space-y-1.5'>
      <Label className='text-sm font-medium'>{props.label}</Label>
      {props.children}
      {props.hint ? (
        <p className='text-muted-foreground text-xs'>{props.hint}</p>
      ) : null}
    </div>
  )
}

export function LabCard() {
  const [config, setConfig] = useState<LabCardConfig | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const res = await api.get('/api/option/')
      setConfig(parseConfig(optionValueOf(res.data, LAB_OPTION_KEY)))
    } catch {
      setConfig({ ...DEFAULT_CONFIG })
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const patch = (next: Partial<LabCardConfig>) => {
    setConfig((prev) => (prev ? { ...prev, ...next } : prev))
  }

  const save = async () => {
    if (!config) return
    if (config.enabled && !config.modelName.trim()) {
      toast.error('启用前请先填写模型名称')
      return
    }
    setSaving(true)
    try {
      const res = await api.put('/api/option/', {
        key: LAB_OPTION_KEY,
        value: serializeConfig(config),
      })
      requireServerSuccess(res.data)
      toast.success('自研实验室配置已保存')
    } catch (error) {
      toast.error(describeSaveFailure(error, '自研实验室配置保存失败'))
    } finally {
      setSaving(false)
    }
  }

  const reset = () => {
    setConfig({ ...DEFAULT_CONFIG })
    toast.success('已清空表单（记得点保存）')
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <FlaskConical className='h-4 w-4' /> 自研实验室
        </CardTitle>
        <CardDescription>
          公开展示页 /lab 的内容来源（纯展示 + 求赞助）。该页面与主站完全隔离：
          不进模型列表、不参与计费、任何业务请求都不会路由到自研模型。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        {!config ? (
          <Skeleton className='h-72 w-full' />
        ) : (
          <>
            <div className='flex items-start justify-between gap-4 rounded-lg border p-3'>
              <div className='space-y-0.5'>
                <Label className='text-sm font-medium'>开放展示页</Label>
                <p className='text-muted-foreground text-xs'>
                  关闭后 /lab 显示「暂未开放」，已填内容保留不丢失。
                </p>
              </div>
              <Switch
                checked={config.enabled}
                onCheckedChange={(v) => patch({ enabled: v })}
              />
            </div>

            <div className='grid gap-3 sm:grid-cols-2'>
              <Field label='模型名称'>
                <Input
                  value={config.modelName}
                  placeholder='例如：Delta-7B'
                  onChange={(e) => patch({ modelName: e.target.value })}
                />
              </Field>
              <Field label='一句话介绍'>
                <Input
                  value={config.tagline}
                  placeholder='例如：从零手写的对话模型'
                  onChange={(e) => patch({ tagline: e.target.value })}
                />
              </Field>
              <Field label='参数量'>
                <Input
                  value={config.params}
                  placeholder='例如：7B'
                  onChange={(e) => patch({ params: e.target.value })}
                />
              </Field>
              <Field label='硬件'>
                <Input
                  value={config.hardware}
                  placeholder='例如：2×RTX 4090 自家服务器'
                  onChange={(e) => patch({ hardware: e.target.value })}
                />
              </Field>
              <Field label='上下文长度（tokens）'>
                <Input
                  value={config.contextTokens}
                  inputMode='numeric'
                  placeholder='例如：4096'
                  onChange={(e) => patch({ contextTokens: e.target.value })}
                />
              </Field>
              <Field label='生成速度（tok/s）'>
                <Input
                  value={config.speedTps}
                  inputMode='numeric'
                  placeholder='例如：18'
                  onChange={(e) => patch({ speedTps: e.target.value })}
                />
              </Field>
            </div>

            <Field label='项目介绍（长文案）'>
              <Textarea
                value={config.intro}
                rows={4}
                placeholder='讲清楚「纯自研、没有任何底座」这件事：从 tokenizer 开始训、用了多少数据、跑了多久……'
                onChange={(e) => patch({ intro: e.target.value })}
              />
            </Field>

            <Field
              label='训练时间线'
              hint='每行一条，格式：日期 | 标题 | 描述（日期与描述可省略，用竖线分隔）'
            >
              <Textarea
                value={config.timelineText}
                rows={5}
                placeholder={
                  '2026-03 | 第一个 checkpoint | 学会了标点符号\n2026-05 | 能写完整句子 |'
                }
                onChange={(e) => patch({ timelineText: e.target.value })}
              />
            </Field>

            <Field
              label='状态端点（可选）'
              hint='推理服务器的状态 JSON 地址，服务端代理轮询（可填内网地址，避开 CORS）。建议返回 gpu/util/temp/uptime/tokens_total 等字段。'
            >
              <Input
                value={config.statusUrl}
                placeholder='例如：http://127.0.0.1:8100/status'
                onChange={(e) => patch({ statusUrl: e.target.value })}
              />
            </Field>

            <div className='grid gap-3 sm:grid-cols-2'>
              <Field label='演示地址（可选）'>
                <Input
                  value={config.demoUrl}
                  placeholder='https://...'
                  onChange={(e) => patch({ demoUrl: e.target.value })}
                />
              </Field>
              <div className='flex items-end justify-between gap-4 rounded-lg border p-3'>
                <div className='space-y-0.5'>
                  <Label className='text-sm font-medium'>页面内嵌演示</Label>
                  <p className='text-muted-foreground text-xs'>
                    开启后用 iframe 内嵌演示地址（沙箱隔离）。
                  </p>
                </div>
                <Switch
                  checked={config.demoEmbed}
                  onCheckedChange={(v) => patch({ demoEmbed: v })}
                />
              </div>
            </div>

            <div className='space-y-3 rounded-lg border p-4'>
              <p className='text-sm font-medium'>赞助</p>
              <div className='grid gap-3 sm:grid-cols-3'>
                <Field label='目标金额'>
                  <Input
                    value={config.sponsorTarget}
                    inputMode='numeric'
                    placeholder='例如：5000'
                    onChange={(e) => patch({ sponsorTarget: e.target.value })}
                  />
                </Field>
                <Field label='已筹金额'>
                  <Input
                    value={config.sponsorRaised}
                    inputMode='numeric'
                    placeholder='例如：2150'
                    onChange={(e) => patch({ sponsorRaised: e.target.value })}
                  />
                </Field>
                <Field label='币种'>
                  <Input
                    value={config.sponsorCurrency}
                    placeholder='CNY'
                    onChange={(e) => patch({ sponsorCurrency: e.target.value })}
                  />
                </Field>
              </div>
              <Field label='赞助说明'>
                <Textarea
                  value={config.sponsorNote}
                  rows={2}
                  placeholder='例如：10 元 = 1 度电 ≈ 3 万 token 训练量；资金去向每月公示。'
                  onChange={(e) => patch({ sponsorNote: e.target.value })}
                />
              </Field>
              <Field
                label='赞助方式'
                hint='每行一种，格式：名称 | 链接 | 二维码图片地址（链接与二维码可省略）'
              >
                <Textarea
                  value={config.methodsText}
                  rows={3}
                  placeholder={
                    '微信收款 |  | https://example.com/qr.png\n爱发电 | https://afdian.net/@xxx |'
                  }
                  onChange={(e) => patch({ methodsText: e.target.value })}
                />
              </Field>
            </div>

            <Field label='致谢名单' hint='每行一个名字，按顺序展示。'>
              <Textarea
                value={config.acknowledgementsText}
                rows={3}
                placeholder={'@someone\n@another'}
                onChange={(e) =>
                  patch({ acknowledgementsText: e.target.value })
                }
              />
            </Field>

            <div className='flex flex-col-reverse gap-2 border-t pt-4 sm:flex-row sm:justify-end'>
              <Button variant='outline' onClick={reset} disabled={saving}>
                <RotateCcw className='mr-1 h-4 w-4' /> 清空表单
              </Button>
              <Button onClick={save} disabled={saving}>
                {saving ? '保存中…' : '保存自研实验室配置'}
              </Button>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
