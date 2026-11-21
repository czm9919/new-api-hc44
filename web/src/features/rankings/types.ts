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
// ----------------------------------------------------------------------------
// Rankings types
// ----------------------------------------------------------------------------
//
// Shape of the real data shown on the /rankings page.

export type RankingPeriod = 'today' | 'week' | 'month' | 'year'

export type RankingCategoryId =
  | 'all'
  | 'programming'
  | 'roleplay'
  | 'marketing'
  | 'translation'
  | 'science'
  | 'finance'
  | 'health'
  | 'legal'
  | 'education'
  | 'productivity'
  | 'multimodal'

export type ModelRanking = {
  rank: number
  /** Previous rank in the same period; undefined means "new". */
  previous_rank?: number
  model_name: string
  vendor: string
  vendor_icon?: string
  category: RankingCategoryId
  /** Total tokens routed through this model in the period. */
  total_tokens: number
  /** Share of all tokens served (0..1). */
  share: number
  /** Period-over-period change in token volume (%). */
  growth_pct: number
}

export type VendorRanking = {
  rank: number
  vendor: string
  vendor_icon?: string
  total_tokens: number
  share: number
  growth_pct: number
  /** Number of distinct models from this vendor with traffic. */
  models_count: number
  /** Top model from this vendor in the period. */
  top_model: string
}

export type RankingMover = {
  model_name: string
  vendor: string
  vendor_icon?: string
  /** Positive = climbed, negative = dropped. */
  rank_delta: number
  current_rank: number
  /** Token-volume change percent. */
  growth_pct: number
}

/**
 * One sample of a model's token usage at a given timestamp.
 * Flat shape ready to feed VChart's stacked-bar spec.
 */
export type ModelHistoryPoint = {
  ts: string
  /** Pre-formatted x-axis label (e.g. "May 5", "12:00"). */
  label: string
  /** Model display name shown in tooltip / legend. */
  model: string
  vendor: string
  /** Token count routed through the model in this bucket. */
  tokens: number
}

export type ModelHistorySeries = {
  /** Flat points ready for VChart, ordered oldest → newest. */
  points: ModelHistoryPoint[]
  /** Models that appear in the series, sorted by total tokens desc. */
  models: Array<{ name: string; vendor: string; total: number }>
  /** Bucket count (used for sizing axis ticks). */
  buckets: number
}

/**
 * One sample of a vendor's market share at a given timestamp. `share` is
 * normalised within the bucket (sums to 1.0 across all vendors at the same
 * `ts`); `tokens` is preserved for tooltip use.
 */
export type VendorSharePoint = {
  ts: string
  label: string
  vendor: string
  share: number
  tokens: number
}

export type VendorShareSeries = {
  /** Flat points ready for VChart, ordered oldest → newest. */
  points: VendorSharePoint[]
  /** Vendors that appear in the series, sorted by aggregate tokens desc. */
  vendors: Array<{ name: string; total: number; share: number }>
  buckets: number
}

export type RankingsSnapshot = {
  // Overall (all categories) ------------------------------------------------
  models: ModelRanking[]
  vendors: VendorRanking[]
  /** Largest rank gainers in this period. */
  top_movers: RankingMover[]
  /** Largest rank losers in this period. */
  top_droppers: RankingMover[]
  /** Stacked-bar history of token usage by model over the period. */
  models_history: ModelHistorySeries
  /** 100%-stacked area history of token share by vendor over the period. */
  vendor_share_history: VendorShareSeries
}

// ----------------------------------------------------------------------------
// Price leaderboard (hc41)
// ----------------------------------------------------------------------------
//
// 本站单价统一换算成「美元 / 百万 tokens」；按次计费的模型单独用
// `site_request_price` 并排在按 token 计费的模型之后。
// `official_*` 来自管理员「一键同步官方定价」落库的官方参考价快照。

export type PricingLeaderboardRow = {
  rank: number
  model_name: string
  vendor?: string
  /** 'token' | 'request' */
  unit: string
  billing_mode?: string
  site_input_price: number
  site_output_price: number
  site_cache_price?: number
  site_request_price?: number
  official_input_price?: number
  official_output_price?: number
  official_source?: string
  /** 相对官方价的偏差百分比：负数代表比官方便宜。 */
  discount_pct?: number
  has_official: boolean
}

/** 单个官方源的健康状况（hc42：自动同步后会带上）。 */
export type PricingSourceState = {
  count: number
  fetched_at: number
  last_error?: string
}

export type PricingLeaderboard = {
  rows: PricingLeaderboardRow[]
  total: number
  limit: number
  official_available: boolean
  fetched_at?: number
  /** 官方价内容最后一次真正变化的时间。 */
  updated_at?: number
  sources?: string[]
  counts?: Record<string, number>
  official_total?: number
  revision?: string
  /** 快照是否已过新鲜期（后端会在过期时后台自动补一次）。 */
  stale?: boolean
  /** 快照年龄（秒），前端直接拿它显示「x 分钟前」。 */
  age_seconds?: number
  /** 本次抓取失败、条目沿用自上一版快照的源。 */
  carried?: string[]
  source_state?: Record<string, PricingSourceState>
  /** 后端是否启用了自动同步（OFFICIAL_PRICING_SYNC_INTERVAL > 0）。 */
  auto_sync?: boolean
  /** 自动同步间隔（秒）。 */
  sync_interval?: number
  /** 超过多少秒算过期。 */
  stale_after?: number
}
