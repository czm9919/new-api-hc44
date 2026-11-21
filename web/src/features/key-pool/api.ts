/*
 * 号池（Key Pool）前后端接口与类型定义。
 *
 * 号池 = 把用户/运营提供的上游密钥（商汤、英伟达等）验活后合并进同一个「号池渠道」，
 * 对外发放一批带每日额度的 Token。渠道是精确匹配模型的（没有 "*" 通配），
 * 且多把密钥必须落库为多密钥渠道，否则整串密钥会被当成一把发给上游。
 */
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

// ============================ 基础类型 ============================

export interface KeyPoolProvider {
  id: string
  label: string
}

export interface KeyPoolResult {
  channels_created: number
  pool_channel_id: number
  pool_channel_name: string
  valid_count: number
  tokens_created: number
  models?: string
  token_keys: string[]
  tokens?: {
    key: string
    remain_quota: number
    daily_gift_quota: number
    free_quota?: number
    paid_quota?: number
  }[]
  invalid_keys: string[]
  message: string
}

/** 用户视角的「按供应商」号池订阅卡片。 */
export interface KeyPoolCard {
  provider: string
  provider_label: string
  daily_quota: number
  remain_quota: number
  free_quota: number
  paid_quota: number
  next_reset: number
  alive: boolean
}

// ============================ 后台管理类型 ============================

/** 号池的后台可配置项。 */
export interface KeyPoolSettings {
  /** 用户自助贡献时，每个号池 Token 的每日额度上限。 */
  default_daily_gift_quota: number
  /** 用户单次贡献可生成的 Token 数量上限。 */
  user_max_tokens_per_submit: number
  /** 是否开放用户自助贡献入口。 */
  allow_user_contribute: boolean
  /** 号池渠道是否自动从上游拉取模型列表。 */
  auto_bind_models: boolean
  /** 免费额度（签到奖励）可用分组白名单，逗号分隔；空表示不限制。 */
  free_quota_allowed_groups: string
  /**
   * 免费额度可用模型白名单，逗号分隔，支持 `prefix*` 通配；空表示不限制。
   * 与分组白名单是「同时满足」关系：分组合格但模型不在白名单内时仍扣付费余额。
   */
  free_quota_allowed_models: string
  /** 单模型自动检查+自动下架开关。 */
  model_auto_check_enabled: boolean
}

/** 号池总览统计。 */
export interface KeyPoolOverview {
  channel_count: number
  total_keys: number
  enabled_keys: number
  disabled_keys: number
  token_count: number
  token_remain_quota: number
  token_daily_quota: number
  alive_tokens: number
  dead_tokens: number
}

/** 号池渠道的后台视图。 */
export interface KeyPoolChannelInfo {
  id: number
  name: string
  provider: string
  provider_label: string
  base_url: string
  group: string
  status: number
  /** 是否多密钥渠道。非多密钥时整串密钥会被当成一把发给上游。 */
  is_multi_key: boolean
  /** 取密钥策略：polling 轮询 / random 随机。 */
  multi_key_mode: string
  key_count: number
  enabled_keys: number
  disabled_keys: number
  model_count: number
  models: string
  token_count: number
  token_remain_quota: number
  token_daily_quota: number
  created_time: number
  test_time: number
  response_time: number
  status_reason: string
}

/** 号池 Token 的后台视图。 */
export interface KeyPoolTokenRow {
  id: number
  name: string
  user_id: number
  username: string
  key_mask: string
  status: number
  daily_gift_quota: number
  remain_quota: number
  free_quota: number
  source_channel_id: number
  provider: string
  provider_label: string
  created_time: number
  accessed_time: number
  daily_quota_date: string
}

/** 单把上游密钥的验活结果。 */
export interface KeyPoolKeyTestResult {
  index: number
  key_mask: string
  valid: boolean
  error: string
}

/** 号池渠道的整体验活结果。 */
export interface KeyPoolChannelTestResult {
  channel_id: number
  channel_name: string
  provider: string
  results: KeyPoolKeyTestResult[]
  valid_count: number
  invalid_count: number
}

// ============================ 接口 ============================

/** 内置的供应商兜底表：接口不可用时页面仍能显示可选供应商。 */
const FALLBACK_PROVIDERS: KeyPoolProvider[] = [
  { id: 'sensenova', label: '商汤 SenseNova' },
  { id: 'nvidia', label: '英伟达 NVIDIA' },
]

/**
 * 供应商列表（用户与管理员都能调）。
 * 走用户可访问的接口，管理员同样可用；接口失败时回退到内置列表，
 * 避免整个页面因接口异常而不可用。
 */
export async function getKeyPoolProviders(): Promise<KeyPoolProvider[]> {
  try {
    const res = await api.get('/api/user/key-pool/providers')
    const list = res.data?.data
    if (Array.isArray(list) && list.length > 0) return list
  } catch {
    // 忽略，回退到内置供应商列表
  }
  return FALLBACK_PROVIDERS
}

/** 用户侧可见的号池公开配置。 */
export interface UserKeyPoolSettings {
  default_daily_gift_quota: number
  max_tokens_per_submit: number
  allow_contribute: boolean
}

/** 用户侧可见的号池公开配置（每日额度、单次上限、是否开放贡献）。 */
export async function getUserKeyPoolSettings(): Promise<UserKeyPoolSettings> {
  const res = await api.get('/api/user/key-pool/settings')
  return requireServerSuccess(res.data)?.data
}

/** 当前用户的号池订阅卡片。 */
export async function getUserKeyPoolCards(): Promise<KeyPoolCard[]> {
  const res = await api.get('/api/user/key-pool/cards')
  return res.data?.data ?? []
}

/** 用户自助贡献上游密钥。 */
export async function contributeKeyPool(data: {
  provider: string
  keys: string
  /** 上游账号标识（账号邮箱/用户名），用于拦截同一账号的重复贡献。 */
  account: string
  token_count?: number
}): Promise<KeyPoolResult> {
  const res = await api.post('/api/user/key-pool/contribute', data)
  return requireServerSuccess(res.data)?.data
}

/** 当前用户已贡献过的上游账号（脱敏）。 */
export interface KeyPoolAccountRow {
  provider: string
  account: string
  account_key: string
  key_mask: string
  contributor_id: number
  channel_id: number
  created_time: number
  status: number
}

/** 读取当前用户已贡献的上游账号（可按供应商过滤）。 */
export async function getUserKeyPoolAccounts(
  provider?: string
): Promise<KeyPoolAccountRow[]> {
  const res = await api.get('/api/user/key-pool/accounts', {
    params: provider ? { provider } : {},
  })
  return res.data?.data ?? []
}

/** 运营侧生成号池。 */
export async function generateKeyPool(data: {
  provider: string
  keys: string
  user_id: number
  token_count: number
  quota_per_token: number
  daily_gift_quota?: number
  models?: string
  group?: string
  name_prefix?: string
}): Promise<KeyPoolResult> {
  const res = await api.post('/api/key-pool/generate', data)
  return requireServerSuccess(res.data)?.data
}

/** 读取号池后台设置。 */
export async function getKeyPoolSettings(): Promise<KeyPoolSettings> {
  const res = await api.get('/api/key-pool/settings')
  // 业务失败是 200 + success:false，不校验的话阅读方会拿到 undefined
  return requireServerSuccess(res.data)?.data
}

/** 保存号池后台设置。 */
export async function updateKeyPoolSettings(
  settings: KeyPoolSettings
): Promise<KeyPoolSettings> {
  const res = await api.put('/api/key-pool/settings', settings)
  // 校验 success：之前失败会被当成功吞掉（页面拿到 undefined 后直接变骨架屏）
  return requireServerSuccess(res.data)?.data
}

/** 号池总览统计。 */
export async function getKeyPoolOverview(): Promise<KeyPoolOverview> {
  const res = await api.get('/api/key-pool/overview')
  return requireServerSuccess(res.data)?.data
}

/** 号池渠道列表。 */
export async function getKeyPoolChannels(): Promise<KeyPoolChannelInfo[]> {
  const res = await api.get('/api/key-pool/channels')
  return requireServerSuccess(res.data)?.data ?? []
}

/** 号池 Token 列表（分页）。 */
export async function getKeyPoolTokens(params: {
  provider?: string
  user_id?: number
  keyword?: string
  page?: number
  page_size?: number
}): Promise<{ items: KeyPoolTokenRow[]; total: number }> {
  const res = await api.get('/api/key-pool/tokens', { params })
  return { items: res.data?.data ?? [], total: res.data?.total ?? 0 }
}

/** 号池渠道内每把密钥的状态（脱敏）。 */
export interface KeyPoolKeyInfo {
  index: number
  key_mask: string
  enabled: boolean
  reason: string
  disabled_at: number
}

/** 取号池渠道内每把密钥的状态（脱敏）。 */
export async function getKeyPoolChannelKeys(
  channelId: number
): Promise<KeyPoolKeyInfo[]> {
  const res = await api.get(`/api/key-pool/channels/${channelId}/keys`)
  return res.data?.data ?? []
}

/** 启用 / 停用号池渠道里的某一把上游密钥。 */
export async function setKeyPoolKeyStatus(
  channelId: number,
  keyIndex: number,
  enabled: boolean
): Promise<void> {
  const res = await api.put(
    `/api/key-pool/channels/${channelId}/keys/${keyIndex}`,
    { enabled }
  )
  // 失败时后端同样是 200 + success:false，不校验会误报「已启用/已停用」
  requireServerSuccess(res.data)
}

/** 逐把验活号池渠道里的上游密钥。 */
export async function testKeyPoolChannel(
  channelId: number
): Promise<KeyPoolChannelTestResult> {
  const res = await api.post(`/api/key-pool/channels/${channelId}/test`)
  return requireServerSuccess(res.data)?.data
}

/** 立即把号池 Token 的余额重置为每日额度上限。 */
export async function resetKeyPoolTokenQuota(tokenId: number): Promise<void> {
  const res = await api.post(`/api/key-pool/tokens/${tokenId}/reset-quota`)
  requireServerSuccess(res.data)
}
