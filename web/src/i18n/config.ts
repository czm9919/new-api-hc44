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
import i18n, { type BackendModule, type ReadCallback } from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import {
  convertDetectedLanguage,
  type InterfaceLanguageCode,
} from './languages'

/**
 * Locale bundles are large (each one is roughly 0.6 MB of raw JSON, ~4.7 MB
 * combined), so they are loaded on demand instead of being bundled into the
 * main chunk. The active language (and its fallback) is fetched before the
 * first React render; switching languages later reuses the same cached loads.
 */
type LocaleTranslation = Record<string, string>
type LocaleModule = { default: { translation: LocaleTranslation } }

const LOCALE_LOADERS: Record<
  InterfaceLanguageCode,
  () => Promise<LocaleModule>
> = {
  en: () => import('./locales/en.json'),
  zhCN: () => import('./locales/zh.json'),
  fr: () => import('./locales/fr.json'),
  ru: () => import('./locales/ru.json'),
  ja: () => import('./locales/ja.json'),
  vi: () => import('./locales/vi.json'),
  zhTW: () => import('./locales/zh-TW.json'),
}

const loadedTranslations = new Map<string, Promise<LocaleTranslation>>()

/** Load (and memoize) the `translation` namespace for one language code. */
function loadLocaleTranslation(code: string): Promise<LocaleTranslation> {
  const cached = loadedTranslations.get(code)
  if (cached) return cached

  const loader = LOCALE_LOADERS[code as InterfaceLanguageCode]
  const pending = loader
    ? loader().then((mod) => mod.default.translation)
    : Promise.reject(new Error(`Unsupported interface language: ${code}`))
  // Drop failed loads from the cache so a later attempt can retry.
  pending.catch(() => {
    loadedTranslations.delete(code)
  })
  loadedTranslations.set(code, pending)
  return pending
}

/**
 * i18next backend plugin backed by `LOCALE_LOADERS`. The `read` callback must
 * resolve with the namespace data (the `translation` object), not the whole
 * JSON module; passing `false` as data on failure tells i18next not to retry.
 */
const lazyLocaleBackend: BackendModule = {
  type: 'backend',
  init() {
    /* no-op: loaders are module-scoped */
  },
  async read(
    language: string,
    _namespace: string,
    callback: ReadCallback
  ): Promise<void> {
    try {
      callback(null, await loadLocaleTranslation(language))
    } catch (error) {
      callback(error instanceof Error ? error : new Error(String(error)), false)
    }
  },
}

i18n.use(LanguageDetector)
i18n.use(initReactI18next)
i18n.use(lazyLocaleBackend)

const initPromise = i18n.init({
  // 站点面向中文用户：读者主动选了英文时整站英文，其余情况一律简体中文。
  // 缺键回退也落在中文上，避免「界面中文、提示英文」。繁体先落简体再落英文。
  fallbackLng: (code: string) =>
    code === 'en' ? ['en'] : ['zhCN', 'en'],
  supportedLngs: ['en', 'zhCN', 'fr', 'ru', 'ja', 'vi', 'zhTW'],
  load: 'currentOnly',
  partialBundledLanguages: true,
  nsSeparator: false, // Allow literal colons in keys (e.g., URLs, labels)
  debug: import.meta.env.DEV,
  interpolation: {
    escapeValue: false, // not needed for react as it escapes by default
  },
  detection: {
    // 只读用户主动保存的选择，不跟随浏览器语言——英文系统首次访问同样是
    // 中文界面，用户仍可在设置里切到 English 并记住。
    order: ['localStorage'],
    caches: ['localStorage'],
    // Browsers report `zh-CN`/`zh-TW`/`zh`; map them onto our `zhCN`/`zhTW`
    // codes (non-Chinese codes pass through for normal supportedLngs matching).
    convertDetectedLanguage,
  },
  react: {
    // Components render with fallback text until bundles arrive instead of
    // suspending (there is no Suspense boundary above the app tree).
    useSuspense: false,
  },
})

/**
 * Ensure the current (or given) language bundle is fully loaded. Called before
 * the first React render so the UI never flashes raw translation keys. Always
 * resolves: a failed load still lets the app mount (i18next falls back).
 */
export async function preloadLocales(language?: string): Promise<void> {
  await initPromise
  const target = language ?? i18n.language ?? 'zhCN'
  await i18n.loadLanguages([target])
}

export default i18n
