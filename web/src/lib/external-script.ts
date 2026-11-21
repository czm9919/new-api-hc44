// 外部脚本完整性（v1.1.0 M1 · B140 · SRI）
//
// 页面上真正跨域加载的脚本只有三类：Cloudflare Turnstile、Telegram 登录挂件、
// 以及服务端注入的统计脚本。它们都来自第三方域名，一旦 CDN 被投毒，注入的
// 代码就能在已登录用户的会话里执行。SRI（Subresource Integrity）让浏览器在
// 响应内容与配置的 sha384 摘要不一致时直接拒载，这是纯同源部署之外唯一能
// 挡住这类风险的机制。
//
// 为什么默认哈希是空的：Turnstile 与 Telegram 挂件的响应体是按租户/版本动态
// 生成的，官方不承诺固定内容，写死一个哈希会让登录在升级当天就挂掉。因此
// 这里提供一个统一的挂载点——运营方在 EXTERNAL_SCRIPT_INTEGRITY 里登记
// `sha384-...` 后立即生效，未登记的来源只做协议强制（必须 https）与
// crossorigin=anonymous（避免把本站 Cookie 随请求带给第三方）。

const EXTERNAL_SCRIPT_INTEGRITY: Record<string, string> = {
  // 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit':
  //   'sha384-…',
  // 'https://telegram.org/js/telegram-widget.js?22': 'sha384-…',
}

interface ExternalScriptOptions {
  id: string
  src: string
  /** 额外属性（data-* 等），保持与调用方既有行为一致。 */
  attributes?: Record<string, string>
}

export function externalScriptIntegrity(src: string): string | undefined {
  return EXTERNAL_SCRIPT_INTEGRITY[src]
}

/**
 * Apply crossorigin/SRI to a script element the caller builds itself — used by
 * widgets that must be inserted at a specific position in the DOM (the
 * Telegram login iframe renders where its script sits).
 */
export function applyExternalScriptIntegrity(script: HTMLScriptElement, src: string): void {
  const url = new URL(src, window.location.href)
  if (url.origin === window.location.origin) return
  const integrity = externalScriptIntegrity(src)
  if (!integrity) return
  // SRI only applies in CORS fetch mode, so crossorigin must be set together
  // with the hash — and only then: forcing CORS mode on a host that does not
  // send Access-Control-Allow-Origin would break the widget outright.
  script.crossOrigin = 'anonymous'
  script.integrity = integrity
}

/**
 * Load a third-party script exactly once per page, enforcing https for
 * cross-origin sources and applying SRI whenever a hash is registered.
 */
export function loadExternalScript(options: ExternalScriptOptions): HTMLScriptElement {
  const { id, src, attributes } = options
  const existing = document.querySelector(`script[id="${CSS.escape(id)}"]`)
  if (existing instanceof HTMLScriptElement) return existing

  const url = new URL(src, window.location.href)
  if (url.origin !== window.location.origin && url.protocol !== 'https:') {
    throw new Error(`Refusing to load cross-origin script over ${url.protocol}: ${src}`)
  }

  const script = document.createElement('script')
  script.id = id
  script.src = src
  script.async = true
  applyExternalScriptIntegrity(script, src)
  for (const [name, value] of Object.entries(attributes ?? {})) {
    script.setAttribute(name, value)
  }
  document.head.appendChild(script)
  return script
}
