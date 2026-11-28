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
import i18next from 'i18next'
import { afterEach, describe, expect, test } from 'vitest'

import {
  currentUiLanguage,
  uiLanguageHeaders,
  UI_LANGUAGE_HEADER,
} from '@/lib/ui-language'

const originalLanguage = i18next.language

afterEach(async () => {
  await i18next.changeLanguage(originalLanguage)
})

describe('ui language header', () => {
  test('reports the active interface language in the codes the server parses', async () => {
    await i18next.changeLanguage('zhCN')
    expect(currentUiLanguage()).toBe('zh-CN')

    await i18next.changeLanguage('zhTW')
    expect(currentUiLanguage()).toBe('zh-TW')

    await i18next.changeLanguage('en')
    expect(currentUiLanguage()).toBe('en')
  })

  test('keeps the languages the backend dictionary does not have', async () => {
    await i18next.changeLanguage('fr')
    expect(uiLanguageHeaders()).toEqual({ [UI_LANGUAGE_HEADER]: 'fr' })
  })

  test('sends nothing when no language is active', () => {
    const language = i18next.language
    const resolved = i18next.resolvedLanguage
    i18next.language = ''
    i18next.resolvedLanguage = ''
    expect(uiLanguageHeaders()).toEqual({})
    i18next.language = language
    i18next.resolvedLanguage = resolved
  })
})
