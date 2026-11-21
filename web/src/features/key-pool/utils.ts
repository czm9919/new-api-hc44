/*
 * 号池功能的常量与格式化工具。
 * 单独成文件是为了让 shared.tsx 只导出组件（React Fast Refresh 要求）。
 */

// 各供应商的注册 / 申请密钥入口（一键跳转）。
export const PROVIDER_SIGNUP: Record<string, string> = {
  nvidia: 'https://build.nvidia.com/',
  // 商汤 Token Plan 控制台：注册后在此创建 sk- 开头密钥（旧 api.sensenova.cn 为
  // sensecore 兼容模式，密钥体系不同，已不再使用）。
  sensenova: 'https://platform.sensenova.cn/token-plan',
}

export const PROVIDER_LABEL: Record<string, string> = {
  nvidia: '英伟达 NVIDIA',
  sensenova: '商汤 SenseNova',
}

export const providerSignup = (id: string) =>
  PROVIDER_SIGNUP[id] ??
  'https://www.google.com/search?q=' + encodeURIComponent(id + ' API key')

export const providerLabel = (id: string) => PROVIDER_LABEL[id] ?? id

/** 时间戳（秒）格式化为本地时间字符串。 */
export function fmtTime(unixSec?: number) {
  if (!unixSec) return '-'
  const d = new Date(unixSec * 1000)
  if (Number.isNaN(d.getTime())) return '-'
  return d.toLocaleString('zh-CN', { hour12: false })
}

/** 额度数字千分位显示。 */
export function fmtQuota(n?: number) {
  if (n == null) return '-'
  return n.toLocaleString('zh-CN')
}
