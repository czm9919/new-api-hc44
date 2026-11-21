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
 * 保存失败文案（hc25）。
 *
 * 之前各处统一写成 (error as { response?: { data?: { message?: string } } })
 * ?.response?.data?.message —— 但业务失败是 createServerError 抛出的普通 Error
 * （真正的报文挂在 cause 上，不在 response 里），这个表达式永远拿不到东西，
 * 界面上就只剩一句干巴巴的「保存失败」，管理员没法判断到底哪一项不合法。
 *
 * getServerErrorMessage 会把 axios error、业务报文、Error.cause 都翻一遍，
 * 顺带把 message_key 做 i18n，所以统一走它。
 */
import { getServerErrorMessage } from '@/lib/server-error-message'

export function describeSaveFailure(error: unknown, fallback: string): string {
  const message = getServerErrorMessage(error, fallback)
  return message.trim() ? message : fallback
}
