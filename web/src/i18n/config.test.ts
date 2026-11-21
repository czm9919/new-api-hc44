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
import { beforeAll, describe, expect, it } from 'vitest'

import i18n, { preloadLocales } from './config'
import en from './locales/en.json'
import fr from './locales/fr.json'
import ja from './locales/ja.json'
import zh from './locales/zh.json'

type Bundle = Record<string, string>

const EN: Bundle = en.translation
const FR: Bundle = fr.translation
const JA: Bundle = ja.translation
const ZH: Bundle = zh.translation

/**
 * Only plain keys are usable with `i18n.t()`: the instance keeps i18next's
 * default `.` key separator, and keys with `{{...}}` would be interpolated.
 */
function isPlainKey(key: string): boolean {
  return !key.includes('.') && !key.includes(':') && !key.includes('{{')
}

/** A key translated into all four locales with four distinct texts. */
function findDistinctKey(): string {
  for (const key of Object.keys(EN)) {
    if (!isPlainKey(key)) continue
    if (new Set([EN[key], FR[key], JA[key], ZH[key]]).size === 4) return key
  }
  throw new Error('no translation key is distinct across en/fr/ja/zhCN')
}

const DISTINCT_KEY = findDistinctKey()

beforeAll(async () => {
  // Re-initialize the shared instance so the on-demand backend is exercised
  // from a clean slate: `test-setup.ts` installs an empty `en` bundle, and
  // i18next merges previous options into every later `init()` call.
  await i18n.init({
    lng: 'en',
    fallbackLng: (code: string) => (code === 'zhTW' ? ['zhCN', 'en'] : ['en']),
    supportedLngs: ['en', 'zhCN', 'fr', 'ru', 'ja', 'vi', 'zhTW'],
    load: 'currentOnly',
    partialBundledLanguages: true,
    nsSeparator: false,
    resources: {},
    react: { useSuspense: false },
  })
})

describe('lazy locale loading', () => {
  it('serves translations for each requested language', async () => {
    await preloadLocales('zhCN')
    expect(i18n.t(DISTINCT_KEY, { lng: 'zhCN' })).toBe(ZH[DISTINCT_KEY])

    await preloadLocales('ja')
    expect(i18n.t(DISTINCT_KEY, { lng: 'ja' })).toBe(JA[DISTINCT_KEY])

    await preloadLocales('en')
    expect(i18n.t(DISTINCT_KEY, { lng: 'en' })).toBe(EN[DISTINCT_KEY])
  })

  it('falls back to English for keys a locale does not translate', async () => {
    const missing = Object.keys(EN).find((key) => isPlainKey(key) && !FR[key])
    if (!missing) throw new Error('expected at least one en-only key')

    await preloadLocales('en')
    await preloadLocales('fr')

    expect(i18n.t(missing, { lng: 'fr' })).toBe(EN[missing])
  })

  it('applies a language switch once its bundle is loaded', async () => {
    await preloadLocales('ja')
    await i18n.changeLanguage('ja')

    expect(i18n.language).toBe('ja')
    expect(i18n.t(DISTINCT_KEY)).toBe(JA[DISTINCT_KEY])
  })

  it('resolves instead of rejecting for unsupported language codes', async () => {
    await expect(preloadLocales('klingon')).resolves.toBeUndefined()
  })
})
