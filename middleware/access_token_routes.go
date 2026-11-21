package middleware

import (
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
)

type accessTokenRuleKind int

const (
	// accessTokenRuleScope requires the token to carry scope.
	accessTokenRuleScope accessTokenRuleKind = iota + 1
	// accessTokenRuleAny accepts every valid scoped token; the handler applies
	// its own checks.
	accessTokenRuleAny
	// accessTokenRuleSession rejects every access token, legacy ones included.
	accessTokenRuleSession
)

type accessTokenRouteRule struct {
	kind  accessTokenRuleKind
	scope string
}

var (
	accessTokenAnyRule     = accessTokenRouteRule{kind: accessTokenRuleAny}
	accessTokenSessionRule = accessTokenRouteRule{kind: accessTokenRuleSession}
)

func accessTokenScopeRule(scope string) accessTokenRouteRule {
	return accessTokenRouteRule{kind: accessTokenRuleScope, scope: scope}
}

// accessTokenRouteRules declares the token rule of every dashboard route that
// is not guarded by RequirePermission, keyed by method and gin full path.
// Casbin-guarded routes declare themselves through
// DeclareAccessTokenPermissionRoute when they are registered.
var accessTokenRouteRules = map[string]accessTokenRouteRule{
	// router/api-router.go: top level
	"GET /api/models":                     accessTokenScopeRule("profile:read"),
	"GET /api/status/test":                accessTokenScopeRule("log:read"),
	"GET /api/pricing":                    accessTokenAnyRule,
	"GET /api/perf-metrics/summary":       accessTokenAnyRule,
	"GET /api/perf-metrics":               accessTokenAnyRule,
	"GET /api/rankings":                   accessTokenAnyRule,
	"GET /api/pricing/leaderboard":        accessTokenAnyRule,
	"POST /api/oauth/state":               accessTokenSessionRule,
	"POST /api/oauth/email/bind/start":    accessTokenScopeRule("account_security:write"),
	"POST /api/oauth/email/bind/resend":   accessTokenScopeRule("account_security:write"),
	"POST /api/oauth/email/bind":          accessTokenScopeRule("account_security:write"),
	"POST /api/oauth/wechat/bind":         accessTokenScopeRule("account_security:write"),
	"POST /api/oauth/telegram/bind/start": accessTokenSessionRule,
	"GET /api/oauth/:provider":            accessTokenSessionRule,
	"GET /api/verify/methods":             accessTokenAnyRule,
	"POST /api/verify":                    accessTokenAnyRule,

	// router/api-router.go: /api/user (self)
	"GET /api/user/sessions":                accessTokenSessionRule,
	"DELETE /api/user/sessions/:sid":        accessTokenSessionRule,
	"POST /api/user/sessions/revoke-others": accessTokenSessionRule,
	"GET /api/user/self/groups":             accessTokenScopeRule("profile:read"),
	"GET /api/user/self":                    accessTokenScopeRule("profile:read"),
	"GET /api/user/models":                  accessTokenScopeRule("profile:read"),
	"GET /api/user/cost-insight":            accessTokenScopeRule("usage:read"),
	"GET /api/user/token-risk":              accessTokenScopeRule("api_key:read"),
	// hc31 · B211 站内通知中心
	"GET /api/user/notifications":              accessTokenScopeRule("profile:read"),
	"GET /api/user/notifications/unread-count": accessTokenScopeRule("profile:read"),
	"POST /api/user/notifications/read-all":    accessTokenScopeRule("profile:write"),
	"POST /api/user/notifications/:id/read":    accessTokenScopeRule("profile:write"),
	"PUT /api/user/self":                       accessTokenScopeRule("profile:write"),
	"DELETE /api/user/self":                    accessTokenScopeRule("account_security:write"),
	"GET /api/user/access_tokens":              accessTokenSessionRule,
	"GET /api/user/access_tokens/catalog":      accessTokenSessionRule,
	"GET /api/user/access_tokens/scopes":       accessTokenSessionRule,
	"POST /api/user/access_tokens":             accessTokenSessionRule,
	"PATCH /api/user/access_tokens/:id":        accessTokenSessionRule,
	"DELETE /api/user/access_tokens/:id":       accessTokenSessionRule,
	"DELETE /api/user/access_tokens/legacy":    accessTokenSessionRule,
	"GET /api/user/passkey":                    accessTokenScopeRule("account_security:read"),
	"POST /api/user/passkey/register/begin":    accessTokenScopeRule("account_security:write"),
	"POST /api/user/passkey/register/finish":   accessTokenScopeRule("account_security:write"),
	"POST /api/user/passkey/verify/begin":      accessTokenAnyRule,
	"POST /api/user/passkey/verify/finish":     accessTokenAnyRule,
	"DELETE /api/user/passkey":                 accessTokenScopeRule("account_security:write"),
	"GET /api/user/aff":                        accessTokenScopeRule("wallet:read"),
	"GET /api/user/topup/info":                 accessTokenScopeRule("wallet:read"),
	"GET /api/user/topup/self":                 accessTokenScopeRule("wallet:read"),
	// v1.1.0 M1 · B08 订单状态 SSE 推送（EventSource 无法带 PAT，按只读钱包作用域）
	"GET /api/user/topup/stream":          accessTokenScopeRule("wallet:read"),
	"POST /api/user/topup":                accessTokenScopeRule("wallet:write"),
	"POST /api/user/pay":                  accessTokenScopeRule("wallet:write"),
	"POST /api/user/amount":               accessTokenScopeRule("wallet:read"),
	"POST /api/user/stripe/pay":           accessTokenScopeRule("wallet:write"),
	"POST /api/user/stripe/amount":        accessTokenScopeRule("wallet:read"),
	"POST /api/user/creem/pay":            accessTokenScopeRule("wallet:write"),
	"POST /api/user/waffo/amount":         accessTokenScopeRule("wallet:read"),
	"POST /api/user/waffo/pay":            accessTokenScopeRule("wallet:write"),
	"POST /api/user/waffo-pancake/amount": accessTokenScopeRule("wallet:read"),
	"POST /api/user/waffo-pancake/pay":    accessTokenScopeRule("wallet:write"),
	"POST /api/user/aff_transfer":         accessTokenScopeRule("wallet:write"),
	"PUT /api/user/setting":               accessTokenScopeRule("profile:write"),
	"GET /api/user/2fa/status":            accessTokenScopeRule("account_security:read"),
	"POST /api/user/2fa/setup":            accessTokenScopeRule("account_security:write"),
	"POST /api/user/2fa/enable":           accessTokenScopeRule("account_security:write"),
	"POST /api/user/2fa/disable":          accessTokenScopeRule("account_security:write"),
	"POST /api/user/2fa/backup_codes":     accessTokenScopeRule("account_security:write"),
	"GET /api/user/checkin":               accessTokenScopeRule("wallet:read"),
	"POST /api/user/checkin":              accessTokenScopeRule("wallet:write"),
	// 大转盘抽奖与 QQ 绑定（v1.1.0 M2）：抽奖发的是免费额度，按钱包读写归口；
	// QQ 绑定属于账号安全域。
	"GET /api/user/lottery":                        accessTokenScopeRule("wallet:read"),
	"POST /api/user/lottery/spin":                  accessTokenScopeRule("wallet:write"),
	"GET /api/user/lottery/records":                accessTokenScopeRule("wallet:read"),
	"GET /api/user/qq/bind":                        accessTokenScopeRule("account_security:read"),
	"POST /api/user/qq/bind/code":                  accessTokenScopeRule("account_security:write"),
	"DELETE /api/user/qq/bind":                     accessTokenScopeRule("account_security:write"),
	"GET /api/user/lottery/settings":               accessTokenScopeRule("option:read"),
	"PUT /api/user/lottery/settings":               accessTokenScopeRule("option:write"),
	"POST /api/user/lottery/settings/reset":        accessTokenScopeRule("option:write"),
	"GET /api/user/lottery/qq/settings":            accessTokenScopeRule("option:read"),
	"PUT /api/user/lottery/qq/settings":            accessTokenScopeRule("option:write"),
	"DELETE /api/user/lottery/qq/settings":         accessTokenScopeRule("option:write"),
	"POST /api/user/lottery/qq/settings/rotate":    accessTokenScopeRule("option:write"),
	"GET /api/user/oauth/bindings":                 accessTokenScopeRule("account_security:read"),
	"DELETE /api/user/oauth/bindings/:provider_id": accessTokenScopeRule("account_security:write"),
	// v1.1.0 M2 · B63 邀请排行榜（本人可见的名次 + 榜单）
	"GET /api/user/aff/leaderboard": accessTokenScopeRule("wallet:read"),
	// v1.1.0 M2 · B99 发票抬头簿（开票资料属于账务信息）
	"GET /api/user/invoice_profile":        accessTokenScopeRule("wallet:read"),
	"POST /api/user/invoice_profile":       accessTokenScopeRule("wallet:write"),
	"PUT /api/user/invoice_profile/:id":    accessTokenScopeRule("wallet:write"),
	"DELETE /api/user/invoice_profile/:id": accessTokenScopeRule("wallet:write"),

	// router/api-router.go: /api/user (admin)
	"GET /api/user/":                   accessTokenScopeRule("user:read"),
	"GET /api/user/topup":              accessTokenScopeRule("billing:read"),
	"POST /api/user/topup/complete":    accessTokenScopeRule("billing:write"),
	"POST /api/user/topup/remark":      accessTokenScopeRule("billing:write"),
	"POST /api/user/batch_grant_quota": accessTokenScopeRule("user:write"),
	// v1.1.0 M2 · B222 批量导入用户（CSV/JSON 行）——与批量发额度同权限
	"POST /api/user/batch_import":                      accessTokenScopeRule("user:write"),
	"GET /api/user/search":                             accessTokenScopeRule("user:read"),
	"GET /api/user/consumption_rank":                   accessTokenScopeRule("user:read"), // hc36：hc34 漏登记 scope（PAT 恒 route_undeclared），按用户管理读权限补齐
	"GET /api/user/:id/oauth/bindings":                 accessTokenScopeRule("user:read"),
	"DELETE /api/user/:id/oauth/bindings/:provider_id": accessTokenScopeRule("user:write"),
	"DELETE /api/user/:id/bindings/:binding_type":      accessTokenScopeRule("user:write"),
	"GET /api/user/:id":                                accessTokenScopeRule("user:read"),
	"POST /api/user/":                                  accessTokenScopeRule("user:write"),
	"POST /api/user/manage":                            accessTokenScopeRule("user:write"),
	"PUT /api/user/":                                   accessTokenScopeRule("user:write"),
	"DELETE /api/user/:id":                             accessTokenScopeRule("user:write"),
	"DELETE /api/user/:id/reset_passkey":               accessTokenScopeRule("user:write"),
	"GET /api/user/2fa/stats":                          accessTokenScopeRule("user:read"),
	"DELETE /api/user/:id/2fa":                         accessTokenScopeRule("user:write"),

	// router/api-router.go: /api/subscription
	"GET /api/subscription/plans":                                    accessTokenScopeRule("wallet:read"),
	"GET /api/subscription/self":                                     accessTokenScopeRule("wallet:read"),
	"PUT /api/subscription/self/preference":                          accessTokenScopeRule("wallet:write"),
	"POST /api/subscription/balance/pay":                             accessTokenScopeRule("wallet:write"),
	"POST /api/subscription/epay/pay":                                accessTokenScopeRule("wallet:write"),
	"POST /api/subscription/stripe/pay":                              accessTokenScopeRule("wallet:write"),
	"POST /api/subscription/creem/pay":                               accessTokenScopeRule("wallet:write"),
	"POST /api/subscription/waffo-pancake/pay":                       accessTokenScopeRule("wallet:write"),
	"GET /api/subscription/admin/plans":                              accessTokenScopeRule("billing:read"),
	"POST /api/subscription/admin/plans":                             accessTokenScopeRule("billing:write"),
	"PUT /api/subscription/admin/plans/:id":                          accessTokenScopeRule("billing:write"),
	"PATCH /api/subscription/admin/plans/:id":                        accessTokenScopeRule("billing:write"),
	"POST /api/subscription/admin/bind":                              accessTokenScopeRule("billing:write"),
	"POST /api/subscription/admin/plans/:id/subscriptions/reset":     accessTokenScopeRule("billing:write"),
	"GET /api/subscription/admin/users/:id/subscriptions":            accessTokenScopeRule("billing:read"),
	"POST /api/subscription/admin/users/:id/subscriptions":           accessTokenScopeRule("billing:write"),
	"POST /api/subscription/admin/users/:id/subscriptions/reset":     accessTokenScopeRule("billing:write"),
	"POST /api/subscription/admin/user_subscriptions/:id/invalidate": accessTokenScopeRule("billing:write"),
	"DELETE /api/subscription/admin/user_subscriptions/:id":          accessTokenScopeRule("billing:write"),

	// router/api-router.go: /api/option
	"GET /api/option/":                     accessTokenScopeRule("option:read"),
	"GET /api/option/notify_event_catalog": accessTokenScopeRule("option:read"),
	"GET /api/option/request_policy":       accessTokenScopeRule("option:read"),
	"PATCH /api/option/request_policy":     accessTokenScopeRule("option:write"),
	"PUT /api/option/":                     accessTokenScopeRule("option:write"),
	"PUT /api/option/passkey/domains":      accessTokenScopeRule("option:write"),
	"GET /api/option/model_pricing":        accessTokenScopeRule("option:read"),
	"GET /api/option/model_pricing/issues": accessTokenScopeRule("option:read"),
	// v1.1.0 M2 · B13 数据库备份导出（root）：走系统配置读权限
	"GET /api/backup/export": accessTokenScopeRule("option:read"),
	// v1.1.0 M2 · B12 Redis 健康状态（root）：走运维读权限
	"GET /api/health/redis":                  accessTokenScopeRule("ops:read"),
	"PATCH /api/option/model_pricing":        accessTokenScopeRule("option:write"),
	"POST /api/option/model_pricing/convert": accessTokenScopeRule("option:read"),
	"POST /api/option/model_pricing/preview": accessTokenScopeRule("option:read"),
	// v1.1.0 M1 · B22 倍率变更 diff 预览：只读估算，不落库
	"POST /api/option/ratio_diff_preview":       accessTokenScopeRule("option:read"),
	"POST /api/option/payment_compliance":       accessTokenSessionRule,
	"GET /api/option/channel_affinity_cache":    accessTokenScopeRule("option:read"),
	"DELETE /api/option/channel_affinity_cache": accessTokenScopeRule("option:write"),
	"POST /api/option/rest_model_ratio":         accessTokenScopeRule("option:write"),
	// v1.1.0 M2 · B223 LiteLLM / OpenRouter 价目表导入（会写 ModelRatio/CompletionRatio）
	"POST /api/option/import_model_configs":                      accessTokenScopeRule("option:write"),
	"GET /api/option/waffo-pancake/catalog":                      accessTokenScopeRule("option:read"),
	"POST /api/option/waffo-pancake/pair":                        accessTokenScopeRule("option:write"),
	"POST /api/option/waffo-pancake/save":                        accessTokenScopeRule("option:write"),
	"POST /api/option/waffo-pancake/subscription-product":        accessTokenScopeRule("option:write"),
	"GET /api/option/waffo-pancake/subscription-product-options": accessTokenScopeRule("option:read"),

	// router/api-router.go: /api/custom-oauth-provider, /api/ratio_sync
	"POST /api/custom-oauth-provider/discovery": accessTokenScopeRule("option:write"),
	"GET /api/custom-oauth-provider/":           accessTokenScopeRule("option:read"),
	"GET /api/custom-oauth-provider/:id":        accessTokenScopeRule("option:read"),
	"POST /api/custom-oauth-provider/":          accessTokenScopeRule("option:write"),
	"PUT /api/custom-oauth-provider/:id":        accessTokenScopeRule("option:write"),
	"DELETE /api/custom-oauth-provider/:id":     accessTokenScopeRule("option:write"),
	"GET /api/ratio_sync/channels":              accessTokenScopeRule("option:read"),
	"POST /api/ratio_sync/fetch":                accessTokenScopeRule("option:write"),

	// router/api-router.go: /api/performance, /api/system-task, /api/system-info
	"GET /api/performance/stats":                   accessTokenScopeRule("ops:read"),
	"DELETE /api/performance/disk_cache":           accessTokenScopeRule("ops:write"),
	"POST /api/performance/reset_stats":            accessTokenScopeRule("ops:write"),
	"POST /api/performance/gc":                     accessTokenScopeRule("ops:write"),
	"GET /api/performance/logs":                    accessTokenScopeRule("ops:read"),
	"DELETE /api/performance/logs":                 accessTokenScopeRule("ops:write"),
	"POST /api/system-task/log-cleanup":            accessTokenScopeRule("ops:write"),
	"GET /api/system-task/list":                    accessTokenScopeRule("ops:read"),
	"DELETE /api/system-task/history":              accessTokenScopeRule("ops:write"),
	"GET /api/system-task/current":                 accessTokenScopeRule("ops:read"),
	"GET /api/system-task/:task_id":                accessTokenScopeRule("ops:read"),
	"GET /api/system-info/instances":               accessTokenScopeRule("ops:read"),
	"GET /api/system-info/sync":                    accessTokenScopeRule("ops:read"),
	"DELETE /api/system-info/stale-instances":      accessTokenScopeRule("ops:write"),
	"DELETE /api/system-info/instances/:node_name": accessTokenScopeRule("ops:write"),

	// router/api-router.go: /api/plugin/task
	"GET /api/plugin/task":                           accessTokenScopeRule("plugin:read"),
	"POST /api/plugin/task":                          accessTokenScopeRule("plugin:write"),
	"PUT /api/plugin/task":                           accessTokenScopeRule("plugin:write"),
	"GET /api/plugin/task/runtime/status":            accessTokenScopeRule("plugin:read"),
	"GET /api/plugin/task/marketplace/sources":       accessTokenScopeRule("plugin:read"),
	"PUT /api/plugin/task/marketplace/sources":       accessTokenScopeRule("plugin:write"),
	"GET /api/plugin/task/:key":                      accessTokenScopeRule("plugin:read"),
	"GET /api/plugin/task/:key/icon":                 accessTokenScopeRule("plugin:read"),
	"GET /api/plugin/task/:key/versions":             accessTokenScopeRule("plugin:read"),
	"POST /api/plugin/task/:key/activate":            accessTokenScopeRule("plugin:write"),
	"POST /api/plugin/task/:key/status":              accessTokenScopeRule("plugin:write"),
	"POST /api/plugin/task/:key/dryrun":              accessTokenScopeRule("plugin:write"),
	"DELETE /api/plugin/task/:key/versions/:version": accessTokenScopeRule("plugin:write"),
	// router/api-router.go: /api/request_plugin
	"PUT /api/request_plugin/instances": accessTokenScopeRule("plugin:write"),
	"POST /api/request_plugin/dry_run":  accessTokenScopeRule("plugin:write"),

	// router/channel-router.go: the key route is RootAuth, not Casbin-guarded.
	"POST /api/channel/:id/key": accessTokenScopeRule(service.AccessTokenScopeOf(authz.ChannelSecretView)),

	// router/authz-router.go
	"GET /api/authz/catalog": accessTokenScopeRule("user:read"),

	// router/api-router.go: /api/token
	"GET /api/token/":              accessTokenScopeRule("api_key:read"),
	"GET /api/token/search":        accessTokenScopeRule("api_key:read"),
	"GET /api/token/auto-groups":   accessTokenScopeRule("api_key:read"),
	"GET /api/token/risk/overview": accessTokenScopeRule("api_key:read"),
	"GET /api/token/:id":           accessTokenScopeRule("api_key:read"),
	"POST /api/token/:id/key":      accessTokenScopeRule("api_key:reveal"),
	"POST /api/token/":             accessTokenScopeRule("api_key:write"),
	"PUT /api/token/":              accessTokenScopeRule("api_key:write"),
	"DELETE /api/token/:id":        accessTokenScopeRule("api_key:write"),
	"POST /api/token/batch":        accessTokenScopeRule("api_key:write"),
	"POST /api/token/batch/keys":   accessTokenScopeRule("api_key:reveal"),

	// router/api-router.go: /api/redemption
	"GET /api/redemption/":           accessTokenScopeRule("redemption:read"),
	"GET /api/redemption/search":     accessTokenScopeRule("redemption:read"),
	"GET /api/redemption/:id":        accessTokenScopeRule("redemption:read"),
	"POST /api/redemption/":          accessTokenScopeRule("redemption:write"),
	"POST /api/redemption/batch":     accessTokenScopeRule("redemption:write"),
	"PUT /api/redemption/":           accessTokenScopeRule("redemption:write"),
	"DELETE /api/redemption/invalid": accessTokenScopeRule("redemption:write"),
	"DELETE /api/redemption/:id":     accessTokenScopeRule("redemption:write"),

	// router/api-router.go: /api/audit, /api/log, /api/data
	"GET /api/audit/self":                       accessTokenScopeRule("usage:read"),
	"GET /api/log/":                             accessTokenScopeRule("log:read"),
	"GET /api/log/stat":                         accessTokenScopeRule("log:read"),
	"GET /api/log/self/stat":                    accessTokenScopeRule("usage:read"),
	"GET /api/log/channel_affinity_usage_cache": accessTokenScopeRule("log:read"),
	"GET /api/log/search":                       accessTokenScopeRule("log:read"),
	"GET /api/log/self":                         accessTokenScopeRule("usage:read"),
	"GET /api/log/self/search":                  accessTokenScopeRule("usage:read"),
	"GET /api/log/export":                       accessTokenScopeRule("log:read"),
	"GET /api/log/self/export":                  accessTokenScopeRule("usage:read"),
	// v1.1.0 M2 · B60 扣费明细下钻（按日志 id 或 request_id 定位）
	"GET /api/log/self/detail":     accessTokenScopeRule("usage:read"),
	"GET /api/log/self/detail/:id": accessTokenScopeRule("usage:read"),
	// v1.1.0 M2 · B114 个人延迟分位数（p50/p75/p90/p95/p99）
	"GET /api/log/self/latency": accessTokenScopeRule("usage:read"),
	// v1.1.0 M1 · B26 对账月结单（管理员报表，与 /api/log/export 同权限）
	"GET /api/log/statement":  accessTokenScopeRule("log:read"),
	"GET /api/data/":          accessTokenScopeRule("log:read"),
	"GET /api/data/users":     accessTokenScopeRule("log:read"),
	"GET /api/data/self":      accessTokenScopeRule("usage:read"),
	"GET /api/data/flow":      accessTokenScopeRule("log:read"),
	"GET /api/data/flow/self": accessTokenScopeRule("usage:read"),

	// router/api-router.go: /api/group, /api/prefill_group
	"GET /api/group/":               accessTokenScopeRule("group:read"),
	"GET /api/prefill_group/":       accessTokenScopeRule("group:read"),
	"POST /api/prefill_group/":      accessTokenScopeRule("group:write"),
	"PUT /api/prefill_group/":       accessTokenScopeRule("group:write"),
	"DELETE /api/prefill_group/:id": accessTokenScopeRule("group:write"),

	// router/api-router.go: /api/mj, /api/task
	"GET /api/mj/self":                 accessTokenScopeRule("usage:read"),
	"GET /api/mj/":                     accessTokenScopeRule("log:read"),
	"GET /api/task/self":               accessTokenScopeRule("usage:read"),
	"GET /api/task":                    accessTokenScopeRule("log:read"),
	"GET /api/task/:task_id/artifacts": accessTokenScopeRule("usage:read"),

	// router/api-router.go: /api/vendors, /api/models (admin)
	"POST /api/vendors/operations/preview":  accessTokenScopeRule("model:read"),
	"POST /api/vendors/operations":          accessTokenScopeRule("model:write"),
	"GET /api/vendors/":                     accessTokenScopeRule("model:read"),
	"GET /api/vendors/search":               accessTokenScopeRule("model:read"),
	"GET /api/vendors/:id":                  accessTokenScopeRule("model:read"),
	"POST /api/vendors/":                    accessTokenScopeRule("model:write"),
	"PUT /api/vendors/":                     accessTokenScopeRule("model:write"),
	"DELETE /api/vendors/:id":               accessTokenScopeRule("model:write"),
	"GET /api/models/sync_upstream/preview": accessTokenScopeRule("model:read"),
	"POST /api/models/sync_upstream":        accessTokenScopeRule("model:write"),
	"POST /api/models/delete":               accessTokenScopeRule("model:write"),
	"GET /api/models/missing":               accessTokenScopeRule("model:read"),
	"GET /api/models/":                      accessTokenScopeRule("model:read"),
	"GET /api/models/search":                accessTokenScopeRule("model:read"),
	"GET /api/models/:id":                   accessTokenScopeRule("model:read"),
	"POST /api/models/":                     accessTokenScopeRule("model:write"),
	"PUT /api/models/":                      accessTokenScopeRule("model:write"),
	"DELETE /api/models/:id":                accessTokenScopeRule("model:write"),

	// router/api-router.go: /api/model_notice (admin)，公开的 active 接口走匿名豁免
	"GET /api/model_notice/":       accessTokenScopeRule("model:read"),
	"POST /api/model_notice/":      accessTokenScopeRule("model:write"),
	"PUT /api/model_notice/:id":    accessTokenScopeRule("model:write"),
	"DELETE /api/model_notice/:id": accessTokenScopeRule("model:write"),

	// router/api-router.go: /api/deployments
	"GET /api/deployments/settings":                     accessTokenScopeRule("deployment:read"),
	"POST /api/deployments/settings/test-connection":    accessTokenScopeRule("deployment:write"),
	"GET /api/deployments/":                             accessTokenScopeRule("deployment:read"),
	"GET /api/deployments/search":                       accessTokenScopeRule("deployment:read"),
	"POST /api/deployments/test-connection":             accessTokenScopeRule("deployment:write"),
	"GET /api/deployments/hardware-types":               accessTokenScopeRule("deployment:read"),
	"GET /api/deployments/locations":                    accessTokenScopeRule("deployment:read"),
	"GET /api/deployments/available-replicas":           accessTokenScopeRule("deployment:read"),
	"POST /api/deployments/price-estimation":            accessTokenScopeRule("deployment:read"),
	"GET /api/deployments/check-name":                   accessTokenScopeRule("deployment:read"),
	"POST /api/deployments/":                            accessTokenScopeRule("deployment:write"),
	"GET /api/deployments/:id":                          accessTokenScopeRule("deployment:read"),
	"GET /api/deployments/:id/logs":                     accessTokenScopeRule("deployment:read"),
	"GET /api/deployments/:id/containers":               accessTokenScopeRule("deployment:read"),
	"GET /api/deployments/:id/containers/:container_id": accessTokenScopeRule("deployment:read"),
	"PUT /api/deployments/:id":                          accessTokenScopeRule("deployment:write"),
	"PUT /api/deployments/:id/name":                     accessTokenScopeRule("deployment:write"),
	"POST /api/deployments/:id/extend":                  accessTokenScopeRule("deployment:write"),
	"DELETE /api/deployments/:id":                       accessTokenScopeRule("deployment:write"),

	// router/relay-router.go
	"POST /pg/chat/completions": accessTokenSessionRule,

	// router/api-router.go: 号池 key-pool（管理员）
	"GET /api/key-pool/providers":                accessTokenAnyRule,
	"POST /api/key-pool/generate":                accessTokenAnyRule,
	"GET /api/key-pool/settings":                 accessTokenAnyRule,
	"PUT /api/key-pool/settings":                 accessTokenAnyRule,
	"GET /api/key-pool/overview":                 accessTokenAnyRule,
	"GET /api/key-pool/channels":                 accessTokenAnyRule,
	"GET /api/key-pool/tokens":                   accessTokenAnyRule,
	"GET /api/key-pool/channels/:id/keys":        accessTokenAnyRule,
	"POST /api/key-pool/channels/:id/test":       accessTokenAnyRule,
	"PUT /api/key-pool/channels/:id/keys/:index": accessTokenAnyRule,
	"POST /api/key-pool/tokens/:id/reset-quota":  accessTokenAnyRule,
	// 已占用的上游账号台账（hc22）
	"GET /api/key-pool/accounts": accessTokenAnyRule,

	// router/api-router.go: 号池 key-pool（用户自助）
	"GET /api/user/key-pool/providers":   accessTokenAnyRule,
	"POST /api/user/key-pool/contribute": accessTokenAnyRule,
	"GET /api/user/key-pool/cards":       accessTokenAnyRule,
	"GET /api/user/key-pool/settings":    accessTokenAnyRule,
	// 本人已贡献的上游账号（hc22）
	"GET /api/user/key-pool/accounts": accessTokenAnyRule,

	// router/api-router.go: 违规防护（VGuard）事件审计
	"GET /api/vguard/status":        accessTokenAnyRule,
	"GET /api/vguard/daily":         accessTokenAnyRule,
	"GET /api/vguard/events":        accessTokenAnyRule,
	"GET /api/vguard/events/:id":    accessTokenAnyRule,
	"GET /api/vguard/events/export": accessTokenAnyRule,
	"POST /api/vguard/unblock":      accessTokenAnyRule,

	// router/api-router.go: 实名认证
	"GET /api/user/realname/status":      accessTokenAnyRule,
	"POST /api/user/realname/id2":        accessTokenAnyRule,
	"POST /api/user/realname/manual":     accessTokenAnyRule,
	"POST /api/user/realname/phone/bind": accessTokenAnyRule,
	"POST /api/user/realname/phone/send": accessTokenAnyRule,
	"GET /api/realname/admin/list":       accessTokenAnyRule,
	"POST /api/realname/admin/review":    accessTokenAnyRule,

	// router/api-router.go: 订阅自助
	"POST /api/subscription/self/:id/cancel": accessTokenAnyRule,
}

var (
	permissionRouteRulesMu sync.RWMutex
	permissionRouteRules   = map[string]accessTokenRouteRule{}
)

// DeclareAccessTokenPermissionRoute records that the Casbin permission guarding
// a route is also the token scope it requires. It runs while routes are
// registered and panics on conflicting declarations.
func DeclareAccessTokenPermissionRoute(method, fullPath string, permission authz.Permission) {
	key := method + " " + fullPath
	rule := accessTokenScopeRule(service.AccessTokenScopeOf(permission))
	if _, ok := accessTokenRouteRules[key]; ok {
		panic(fmt.Sprintf("access token route %s is declared twice", key))
	}
	permissionRouteRulesMu.Lock()
	defer permissionRouteRulesMu.Unlock()
	if existing, ok := permissionRouteRules[key]; ok && existing != rule {
		panic(fmt.Sprintf("access token route %s is declared with %s and %s", key, existing.scope, rule.scope))
	}
	permissionRouteRules[key] = rule
}

// AccessTokenRouteRule returns the rule for "METHOD /full/path".
func AccessTokenRouteRule(key string) (accessTokenRouteRule, bool) {
	if rule, ok := accessTokenRouteRules[key]; ok {
		return rule, true
	}
	permissionRouteRulesMu.RLock()
	defer permissionRouteRulesMu.RUnlock()
	rule, ok := permissionRouteRules[key]
	return rule, ok
}

// AccessTokenRouteRuleKeys lists every declared route key, sorted.
func AccessTokenRouteRuleKeys() []string {
	permissionRouteRulesMu.RLock()
	keys := slices.Collect(maps.Keys(permissionRouteRules))
	permissionRouteRulesMu.RUnlock()
	keys = slices.AppendSeq(keys, maps.Keys(accessTokenRouteRules))
	slices.Sort(keys)
	return keys
}

// Scope returns the token scope the rule requires, or "" for any/session rules.
func (rule accessTokenRouteRule) Scope() string {
	return rule.scope
}

// Kind names the rule category: "scope", "any" or "session".
func (rule accessTokenRouteRule) Kind() string {
	switch rule.kind {
	case accessTokenRuleScope:
		return "scope"
	case accessTokenRuleAny:
		return "any"
	case accessTokenRuleSession:
		return "session"
	}
	return ""
}
