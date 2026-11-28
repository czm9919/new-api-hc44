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
 * 加速接入地址卡片（hc52，hc55 补 OpenAI 口径）。
 *
 * 加速链接 = 套了 CDN 的门牌：同一个站、同一套渠道、同一计费、同一调度，
 * 用户只要把 base_url 换掉即可，令牌和模型都不用动。
 *
 * 防截图传播：域名明文显示（守卫会拦无订阅请求，域名本身不是凭证），
 * 个人水印前缀打码——前缀才是「一人一份」的凭证，只在点击复制时拿全量。
 */
import { Copy, Zap } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import type { AccelIngress } from './api'

/**
 * 把地址里的水印前缀打码：`https://a.com/a3f2b91c/v1` → `https://a.com/a3f2•••• /v1`。
 * 前缀不一定是最后一段（OpenAI 口径后面还有 /v1），所以按段找 8 位 hex。
 */
export function maskedBaseUrl(baseUrl: string): string {
  return baseUrl
    .split('/')
    .map((seg) =>
      /^[0-9a-f]{8}$/i.test(seg) ? seg.slice(0, 4) + '••••' : seg
    )
    .join('/')
}

function UrlRow({
  label,
  hint,
  value,
}: {
  label: string
  hint?: string
  value: string
}) {
  const { t } = useTranslation()
  const { copyToClipboard } = useCopyToClipboard()
  return (
    <div className='space-y-1'>
      <div className='text-muted-foreground text-xs font-medium'>{label}</div>
      <div className='flex flex-wrap items-center gap-2'>
        {/* 手机宽度下地址必然超过一行：让它换行而不是横向滚动，
            否则用户得左右拖才能看全域名。 */}
        <code className='bg-muted max-w-full min-w-0 rounded px-2 py-1 text-sm break-all'>
          {maskedBaseUrl(value)}
        </code>
        <Button
          size='sm'
          variant='outline'
          onClick={() => void copyToClipboard(value)}
        >
          <Copy className='mr-1 size-3.5' />
          {t('Copy')}
        </Button>
      </div>
      {hint ? <p className='text-muted-foreground text-xs'>{hint}</p> : null}
    </div>
  )
}

export function IngressCard({ ingress }: { ingress: AccelIngress }) {
  const { t } = useTranslation()
  // 未订阅：域名不下发（locked），只提示订阅后可用。
  if (ingress.locked && !ingress.host) {
    return (
      <Card>
        <CardHeader className='gap-1'>
          <CardTitle className='flex items-center gap-2 text-base'>
            <Zap className='size-4' />
            {t('Accelerated entry point')}
          </CardTitle>
          <CardDescription>
            {t(
              'Subscribe to an acceleration plan to unlock the accelerated entry point.'
            )}
          </CardDescription>
        </CardHeader>
      </Card>
    )
  }
  if (!ingress.host) {
    return null
  }
  const openaiBase = ingress.openai_base_url || `${ingress.base_url}/v1`
  const maskHint = t(
    'The masked part is your personal access prefix — click Copy to get the full address.'
  )
  return (
    <Card>
      <CardHeader className='gap-1'>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Zap className='size-4' />
          {t('Accelerated entry point')}
        </CardTitle>
        <CardDescription>
          {t(
            'Use this domain instead of the default one to get inbound acceleration. It is the same API, only the host changes.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <UrlRow
          label={t('OpenAI-compatible Base URL (recommended)')}
          hint={maskHint}
          value={openaiBase}
        />
        <UrlRow
          label={t('Site root (for clients that append /v1 themselves)')}
          value={ingress.base_url}
        />
        <pre className='bg-muted text-muted-foreground overflow-x-auto rounded p-3 text-xs'>
          {ingress.example}
        </pre>
      </CardContent>
    </Card>
  )
}
