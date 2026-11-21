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
import { useEffect, useRef } from 'react'

import { loadExternalScript } from '@/lib/external-script'

declare global {
  interface Window {
    turnstile?: {
      render: (element: HTMLElement, options: Record<string, unknown>) => void
    }
  }
}

interface TurnstileProps {
  siteKey: string
  onVerify: (token: string) => void
  onExpire?: () => void
  className?: string
}

export function Turnstile({
  siteKey,
  onVerify,
  onExpire,
  className,
}: TurnstileProps) {
  const ref = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    const render = () => {
      if (!ref.current || !window.turnstile) return
      try {
        window.turnstile.render(ref.current, {
          sitekey: siteKey,
          callback: (token: string) => onVerify(token),
          'error-callback': () => onExpire?.(),
          'expired-callback': () => onExpire?.(),
        })
      } catch {
        /* empty */
      }
    }

    if (window.turnstile) {
      render()
      return
    }
    // B140：第三方脚本统一从这里加载，强制 https 并按登记的哈希启用 SRI。
    const script = loadExternalScript({
      id: 'cf-turnstile',
      src: 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit',
    })
    script.addEventListener('load', render)
    return () => script.removeEventListener('load', render)
  }, [siteKey, onVerify, onExpire])

  return <div ref={ref} className={className} />
}
