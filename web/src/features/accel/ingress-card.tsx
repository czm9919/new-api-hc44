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
 * 加速接入地址卡片（hc52）：加速页与 API Keys 页共用。
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

export function maskedBaseUrl(baseUrl: string): string {
  const idx = baseUrl.lastIndexOf('/')
  const scheme = 'https://'
  if (idx <= scheme.length) return baseUrl
  const prefix = baseUrl.slice(idx + 1)
  if (prefix.length < 8) return baseUrl
  return baseUrl.slice(0, idx + 1) + prefix.slice(0, 4) + '••••'
}

export function IngressCard({ ingress }: { ingress: AccelIngress }) {
  const { t } = useTranslation()
  const { copyToClipboard } = useCopyToClipboard()
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
      <CardContent className='space-y-3'>
        <div className='flex flex-wrap items-center gap-2'>
          <code className='bg-muted rounded px-2 py-1 text-sm'>
            {maskedBaseUrl(ingress.base_url)}
          </code>
          <Button
            size='sm'
            variant='outline'
            onClick={() => void copyToClipboard(ingress.base_url)}
          >
            <Copy className='mr-1 size-3.5' />
            {t('Copy')}
          </Button>
        </div>
        <pre className='bg-muted text-muted-foreground overflow-x-auto rounded p-3 text-xs'>
          {ingress.example}
        </pre>
      </CardContent>
    </Card>
  )
}
