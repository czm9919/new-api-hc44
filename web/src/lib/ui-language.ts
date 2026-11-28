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

/**
 * Header the web console sends so the server renders `message` in the language
 * the user is actually looking at.
 *
 * The dictionaries in this app key messages by their English source text, so
 * the console can re-render a response itself — but only when the backend sent
 * a `message_key`. Plenty of responses (middleware rejections, dynamic detail,
 * third-party callers of the same API) carry plain text with no key, and those
 * follow the language stated on the request. Without this header that language
 * would be the browser's, not the page's, so a user who switched to English
 * would still get Chinese toasts (and vice versa).
 */
export const UI_LANGUAGE_HEADER = 'X-UI-Language'

/** Language codes the console can switch to, mapped to what the server parses. */
const SERVER_LANGUAGE_CODES: Record<string, string> = {
  zhCN: 'zh-CN',
  zhTW: 'zh-TW',
  en: 'en',
  fr: 'fr',
  ru: 'ru',
  ja: 'ja',
  vi: 'vi',
}

/** The active interface language as the server should read it, or ''. */
export function currentUiLanguage(): string {
  const language = i18next.resolvedLanguage || i18next.language
  if (!language) return ''
  return SERVER_LANGUAGE_CODES[language] ?? language
}

/** Headers to merge into an outgoing console request. */
export function uiLanguageHeaders(): Record<string, string> {
  const language = currentUiLanguage()
  return language ? { [UI_LANGUAGE_HEADER]: language } : {}
}
