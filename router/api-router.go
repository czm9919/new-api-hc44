package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"

	// Import oauth package to register providers via init()
	_ "github.com/QuantumNous/new-api/oauth"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func SetApiRouter(router *gin.Engine) {
	apiRouter := router.Group("/api")
	apiRouter.Use(middleware.RouteTag("api"))
	// CSRF 双保险（v1.1.0 M1 · B04）：对带 Origin/Referer 的写请求校验来源，
	// 与 SameSite=Strict 的 refresh cookie 形成两道防线。无浏览器头的
	// SDK/服务器调用不受影响，OAuth 与支付回调路径在中间件内部豁免。
	apiRouter.Use(middleware.CSRFOriginCheck())
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	apiRouter.Use(middleware.AccessTokenAudit())
	apiRouter.Use(middleware.BodyStorageCleanup()) // 清理请求体存储
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	anonymousRequestBodyLimit := middleware.AnonymousRequestBodyLimit()
	{
		apiRouter.GET("/setup", controller.GetSetup)
		apiRouter.POST("/setup", anonymousRequestBodyLimit, controller.PostSetup)
		apiRouter.GET("/status", controller.GetStatus)
		apiRouter.GET("/uptime/status", controller.GetUptimeKumaStatus)
		apiRouter.GET("/models", middleware.UserAuth(), controller.DashboardListModels)
		apiRouter.GET("/status/test", middleware.AdminAuth(), controller.TestStatus)
		apiRouter.GET("/notice", controller.GetNotice)
		// 模型公告位（v1.1.0 M2 · B64）：公开读取当前生效的下架/维护公告
		apiRouter.GET("/model_notice/active", middleware.DisableCache(), controller.GetActiveModelNotices)
		// 公开售卖页（v1.1.0 M1 · B20 /buy）：未登录可看套餐，下单仍需登录。
		apiRouter.GET("/subscription/public/plans", middleware.DisableCache(), controller.GetPublicSubscriptionPlans)
		// 自研模型展示页（/lab）：纯展示 + 求赞助，匿名只读，与主站计费/路由零关联。
		// status 是对推理服务器状态端点的服务端代理，避开 CORS。
		apiRouter.GET("/lab/public/config", middleware.DisableCache(), controller.GetLabPublicConfig)
		apiRouter.GET("/lab/public/status", middleware.DisableCache(), controller.GetLabPublicStatus)
		// AstrBot 机器人接入（v1.1.0 M2）：机器人用自己的服务令牌调用，
		// 与用户会话隔离；令牌未配置时接口直接返回 503（默认关闭）。
		// 限流挂在 group 上（先拦再读 body），预算按群规模配置：机器人出口
		// IP 固定，一个 IP 承载整群成员的绑定请求（QQ_AGENT_RATE_LIMIT）。
		qqAgentRoute := apiRouter.Group("/qq/agent", middleware.QQAgentRateLimit())
		{
			qqAgentRoute.GET("/ping", controller.QQAgentPing)
			qqAgentRoute.POST("/verify", anonymousRequestBodyLimit, controller.QQAgentVerify)
			qqAgentRoute.GET("/verify", controller.QQAgentVerify)
			// 自助查询（hc45）：余额 / 用量 / 签到 / 排行，只读或只影响调用者自己
			qqAgentRoute.GET("/me", controller.QQAgentMe)
			qqAgentRoute.POST("/checkin", anonymousRequestBodyLimit, controller.QQAgentCheckin)
			qqAgentRoute.GET("/rank", controller.QQAgentRank)
		}
		apiRouter.GET("/user-agreement", controller.GetUserAgreement)
		apiRouter.GET("/privacy-policy", controller.GetPrivacyPolicy)
		apiRouter.GET("/about", controller.GetAbout)
		//apiRouter.GET("/midjourney", controller.GetMidjourney)
		apiRouter.GET("/home_page_content", controller.GetHomePageContent)
		apiRouter.GET("/pricing", middleware.HeaderNavModuleAuth("pricing"), controller.GetPricing)
		// 价格排行榜（hc41）：公开只读，与 /pricing 共用同一个导航开关。
		// 数据 = 本站单价 + 管理员最近一次同步的官方单价快照。
		apiRouter.GET("/pricing/leaderboard", middleware.HeaderNavModuleAuth("pricing"), controller.GetPricingLeaderboard)
		perfMetricsRoute := apiRouter.Group("/perf-metrics")
		perfMetricsRoute.Use(middleware.HeaderNavModulePublicOrUserAuth("pricing"))
		{
			perfMetricsRoute.GET("/summary", controller.GetPerfMetricsSummary)
			perfMetricsRoute.GET("", controller.GetPerfMetrics)
		}
		apiRouter.GET("/rankings", middleware.HeaderNavModuleAuth("rankings"), controller.GetRankings)
		// 用户消耗排行榜（hc47）：与 /rankings 共用同一个导航开关。
		// 匿名可看榜（用户名打码），登录后额外返回「我的排名」。
		apiRouter.GET("/rankings/users", middleware.HeaderNavModuleAuth("rankings"), controller.GetUserRankingLeaderboard)
		apiRouter.GET("/verification", middleware.EmailVerificationRateLimit(), middleware.TurnstileCheck(), controller.SendEmailVerification)
		apiRouter.GET("/reset_password", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.SendPasswordResetEmail)
		apiRouter.POST("/user/reset", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.ResetPassword)
		// OAuth routes - specific routes must come before :provider wildcard
		apiRouter.POST("/oauth/state", middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.TryUserAuth(), anonymousRequestBodyLimit, controller.GenerateOAuthCode)
		apiRouter.POST("/oauth/email/bind/start", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("account-security"), middleware.EmailVerificationRateLimit(), middleware.DisableCache(), controller.EmailBindStart)
		apiRouter.POST("/oauth/email/bind/resend", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("account-security"), middleware.EmailVerificationRateLimit(), middleware.DisableCache(), controller.EmailBindResend)
		apiRouter.POST("/oauth/email/bind", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("account-security"), middleware.DisableCache(), controller.EmailBind)
		// WeChat uses its existing authorization-code service.
		apiRouter.GET("/oauth/wechat", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.WeChatAuth)
		apiRouter.POST("/oauth/wechat/bind", middleware.UserAuth(), middleware.CriticalRateLimit(), controller.WeChatBind)
		apiRouter.GET("/oauth/telegram/login", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.TelegramLegacyAuth)
		apiRouter.POST("/oauth/telegram/bind/start", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.DisableCache(), controller.TelegramLegacyAuth)
		apiRouter.GET("/oauth/telegram/bind/:flow_token", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.TelegramLegacyAuth)
		// Standard OAuth providers (GitHub, Discord, OIDC, LinuxDO, Telegram) - unified route
		apiRouter.GET("/oauth/:provider", middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.TryUserAuth(), controller.HandleOAuth)
		apiRouter.GET("/ratio_config", middleware.CriticalRateLimit(), controller.GetRatioConfig)

		apiRouter.POST("/stripe/webhook", anonymousRequestBodyLimit, controller.StripeWebhook)
		apiRouter.POST("/creem/webhook", anonymousRequestBodyLimit, controller.CreemWebhook)
		apiRouter.POST("/waffo/webhook", anonymousRequestBodyLimit, controller.WaffoWebhook)
		// :env separates test vs prod URLs so the operator can register each
		// in Pancake's matching webhook slot; handler enforces env match.
		apiRouter.POST("/waffo-pancake/webhook/:env", anonymousRequestBodyLimit, controller.WaffoPancakeWebhook)

		// Universal secure verification routes
		apiRouter.GET("/verify/methods", middleware.UserAuth(), middleware.DisableCache(), controller.GetVerificationMethods)
		apiRouter.POST("/verify", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("security-verification"), middleware.DisableCache(), controller.UniversalVerify)

		userRoute := apiRouter.Group("/user")
		{
			userRoute.POST("/auth/refresh", middleware.SessionCookieOriginGuard(), middleware.CriticalRateLimit(), middleware.DisableCache(), controller.RefreshAuth)
			userRoute.POST("/auth/logout", middleware.SessionCookieOriginGuard(), middleware.CriticalRateLimit(), middleware.DisableCache(), controller.AuthLogout)
			userRoute.POST("/register", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, middleware.TurnstileCheck(), controller.Register)
			userRoute.GET("/login/encryption-key", middleware.DisableCache(), controller.GetPasswordEncryptionKey)
			userRoute.POST("/login", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, middleware.TurnstileCheck(), controller.Login)
			userRoute.POST("/login/2fa", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.Verify2FALogin)
			userRoute.POST("/login/verify", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.VerifyLogin)
			userRoute.POST("/login/passkey/begin", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.LoginPasskeyBegin)
			userRoute.POST("/login/passkey/finish", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.LoginPasskeyFinish)
			userRoute.POST("/passkey/login/begin", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.PasskeyLoginBegin)
			userRoute.POST("/passkey/login/finish", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.PasskeyLoginFinish)
			//userRoute.POST("/tokenlog", middleware.CriticalRateLimit(), controller.TokenLog)
			userRoute.POST("/epay/notify", anonymousRequestBodyLimit, controller.EpayNotify)
			userRoute.GET("/epay/notify", controller.EpayNotify)
			userRoute.GET("/groups", controller.GetUserGroups)

			selfRoute := userRoute.Group("/")
			selfRoute.Use(middleware.DisableCache(), middleware.UserAuth())
			{
				selfRoute.GET("/sessions", middleware.DisableCache(), controller.GetLoginSessions)
				selfRoute.DELETE("/sessions/:sid", middleware.DisableCache(), controller.DeleteLoginSession)
				selfRoute.POST("/sessions/revoke-others", middleware.DisableCache(), controller.RevokeOtherLoginSessions)
				selfRoute.GET("/self/groups", controller.GetUserGroups)
				selfRoute.GET("/self", controller.GetSelf)
				selfRoute.GET("/models", controller.GetUserModels)
				// v1.1.0 M2 · B90 成本优化建议（近 N 天逐模型消耗 + 倍率）
				selfRoute.GET("/cost-insight", controller.GetCostInsight)
				// v1.1.0 M2 · B93 密钥泄露风险自查（token × IP 聚合）
				selfRoute.GET("/token-risk", controller.GetUserTokenRisk)
				// hc31 · B211 站内通知中心
				selfRoute.GET("/notifications", controller.GetNotifications)
				selfRoute.GET("/notifications/unread-count", controller.GetNotificationUnreadCount)
				selfRoute.POST("/notifications/read-all", controller.MarkAllNotificationsRead)
				selfRoute.POST("/notifications/:id/read", controller.MarkNotificationRead)
				selfRoute.PUT("/self", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.UpdateSelf)
				selfRoute.DELETE("/self", middleware.DisableCache(), controller.DeleteSelf)
				accessTokenRoute := selfRoute.Group("/access_tokens")
				accessTokenRoute.Use(middleware.DisableCache())
				{
					accessTokenRoute.GET("", controller.ListAccessTokens)
					accessTokenRoute.GET("/catalog", controller.GetAccessTokenCatalog)
					accessTokenRoute.GET("/scopes", controller.GetAccessTokenScopes)
					accessTokenRoute.POST("", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("access-token"), controller.CreateAccessToken)
					accessTokenRoute.PATCH("/:id", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("access-token"), controller.UpdateAccessToken)
					accessTokenRoute.DELETE("/:id", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("access-token"), controller.DeleteAccessToken)
					accessTokenRoute.DELETE("/legacy", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("access-token"), controller.RevokeLegacyAccessToken)
				}
				selfRoute.GET("/passkey", controller.PasskeyStatus)
				selfRoute.POST("/passkey/register/begin", middleware.UserCriticalRateLimit("security-verification"), middleware.DisableCache(), controller.PasskeyRegisterBegin)
				selfRoute.POST("/passkey/register/finish", middleware.UserCriticalRateLimit("security-verification"), middleware.DisableCache(), controller.PasskeyRegisterFinish)
				selfRoute.POST("/passkey/verify/begin", middleware.UserCriticalRateLimit("security-verification"), middleware.DisableCache(), controller.PasskeyVerifyBegin)
				selfRoute.POST("/passkey/verify/finish", middleware.UserCriticalRateLimit("security-verification"), middleware.DisableCache(), controller.PasskeyVerifyFinish)
				selfRoute.DELETE("/passkey", middleware.DisableCache(), controller.PasskeyDelete)
				selfRoute.GET("/aff", controller.GetAffCode)
				// 邀请排行榜（v1.1.0 M2 · B63）：用户名脱敏展示
				selfRoute.GET("/aff/leaderboard", controller.GetAffLeaderboard)
				// 发票抬头管理（v1.1.0 M2 · B99）
				selfRoute.GET("/invoice_profile", controller.GetUserInvoiceProfiles)
				selfRoute.POST("/invoice_profile", controller.AddInvoiceProfile)
				selfRoute.PUT("/invoice_profile/:id", controller.UpdateInvoiceProfile)
				selfRoute.DELETE("/invoice_profile/:id", controller.DeleteInvoiceProfile)
				selfRoute.GET("/realname/status", controller.GetRealNameStatus)
				selfRoute.POST("/realname/phone/send", middleware.CriticalRateLimit(), controller.SendRealNamePhoneCode)
				selfRoute.POST("/realname/phone/bind", middleware.CriticalRateLimit(), controller.BindRealNamePhone)
				selfRoute.POST("/realname/id2", middleware.CriticalRateLimit(), controller.SubmitRealNameId2)
				selfRoute.POST("/realname/manual", middleware.CriticalRateLimit(), controller.SubmitRealNameManual)
				selfRoute.GET("/topup/info", controller.GetTopUpInfo)
				selfRoute.GET("/topup/self", controller.GetUserTopUps)
				// 订单状态推送（v1.1.0 M1 · B14）：SSE 长连接，前端订阅后
				// 支付回调完成即被动收到订单状态，不再自己轮询。
				selfRoute.GET("/topup/stream", controller.StreamTopUpStatus)
				selfRoute.POST("/topup", middleware.CriticalRateLimit(), controller.TopUp)
				selfRoute.POST("/pay", middleware.CriticalRateLimit(), controller.RequestEpay)
				selfRoute.POST("/amount", controller.RequestAmount)
				selfRoute.POST("/stripe/pay", middleware.CriticalRateLimit(), controller.RequestStripePay)
				selfRoute.POST("/stripe/amount", controller.RequestStripeAmount)
				selfRoute.POST("/creem/pay", middleware.CriticalRateLimit(), controller.RequestCreemPay)
				selfRoute.POST("/waffo/amount", controller.RequestWaffoAmount)
				selfRoute.POST("/waffo/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPay)
				selfRoute.POST("/waffo-pancake/amount", controller.RequestWaffoPancakeAmount)
				selfRoute.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPancakePay)
				selfRoute.POST("/aff_transfer", middleware.UserCriticalRateLimit("aff-transfer"), controller.TransferAffQuota)
				selfRoute.PUT("/setting", controller.UpdateUserSetting)

				// 2FA routes
				selfRoute.GET("/2fa/status", controller.Get2FAStatus)
				selfRoute.POST("/2fa/setup", middleware.UserCriticalRateLimit("security-verification"), middleware.DisableCache(), controller.Setup2FA)
				selfRoute.POST("/2fa/enable", middleware.UserCriticalRateLimit("security-verification"), middleware.DisableCache(), controller.Enable2FA)
				selfRoute.POST("/2fa/disable", middleware.DisableCache(), controller.Disable2FA)
				selfRoute.POST("/2fa/backup_codes", middleware.DisableCache(), controller.RegenerateBackupCodes)

				// Check-in routes
				selfRoute.GET("/checkin", controller.GetCheckinStatus)
				selfRoute.POST("/checkin", middleware.TurnstileCheck(), controller.DoCheckin)

				// 大转盘抽奖（v1.1.0 M2）：奖池/次数由 option 配置，
				// 默认要求绑定 QQ（与免费模型门禁同一凭据）。
				selfRoute.GET("/lottery", controller.GetLottery)
				selfRoute.POST("/lottery/spin", middleware.CriticalRateLimit(), controller.SpinLottery)
				selfRoute.GET("/lottery/records", controller.GetLotteryRecords)

				// 加速专线（hc48）：套餐列表 + 我的订阅 + 开通 + 自动续费开关。
				// 真正决定「能不能用加速分组」的逻辑在 TokenAuth 里，这里只管售卖。
				selfRoute.GET("/accel", controller.GetAccelOverview)
				selfRoute.GET("/accel/lines", controller.GetAccelLines)
				selfRoute.POST("/accel/subscribe", middleware.UserCriticalRateLimit("accel-subscribe"), controller.SubscribeAccel)
				selfRoute.POST("/accel/auto_renew", controller.UpdateAccelAutoRenew)

				// QQ 绑定（v1.1.0 M2）：面板只生成一次性码，绑定动作在
				// 配套 AstrBot 机器人侧完成（见 /api/qq/agent/verify）。
				selfRoute.GET("/qq/bind", controller.GetQQBindStatus)
				selfRoute.POST("/qq/bind/code", middleware.CriticalRateLimit(), controller.CreateQQBindCode)
				selfRoute.DELETE("/qq/bind", controller.UnbindQQAccount)

				// Custom OAuth bindings
				selfRoute.GET("/oauth/bindings", controller.GetUserOAuthBindings)
				selfRoute.DELETE("/oauth/bindings/:provider_id", controller.UnbindCustomOAuth)
			}

			adminRoute := userRoute.Group("/")
			adminRoute.Use(middleware.AdminAuth())
			{
				adminRoute.GET("/", controller.GetAllUsers)
				adminRoute.GET("/topup", controller.GetAllTopUps)
				adminRoute.POST("/topup/complete", controller.AdminCompleteTopUp)
				// v1.1.0 M2 · B59 订单备注：给一笔充值单写运营备注（最长 255 字）
				adminRoute.POST("/topup/remark", controller.AdminUpdateTopUpRemark)
				// v1.1.0 M2 · B58 批量发额度：一次给最多 200 个用户加额度，
				// 逐个独立提交，失败的单独回报原因
				adminRoute.POST("/batch_grant_quota", controller.BatchGrantUserQuota)
				// v1.1.0 M2 · B222 用户批量导入：逐行校验、独立事务、返回明细
				adminRoute.POST("/batch_import", controller.BatchImportUsers)
				// 大转盘抽奖配置（v1.1.0 M2）：开关/每日次数/奖池 JSON/是否要求 QQ
				adminRoute.GET("/lottery/settings", controller.AdminGetLotterySettings)
				adminRoute.PUT("/lottery/settings", controller.AdminUpdateLotterySettings)
				// 恢复默认奖池：运营手改坏了不用记原始 JSON
				adminRoute.POST("/lottery/settings/reset", controller.AdminResetLotterySettings)
				// 加速专线套餐（hc48）：分组必须已在「分组倍率」里配好，
				// 且不能出现在「用户可用分组」中（否则等于对全员免费）。
				adminRoute.GET("/accel/plans", controller.AdminListAccelPlans)
				adminRoute.POST("/accel/plans", controller.AdminCreateAccelPlan)
				adminRoute.PUT("/accel/plans/:id", controller.AdminUpdateAccelPlan)
				adminRoute.DELETE("/accel/plans/:id", controller.AdminDeleteAccelPlan)
				adminRoute.GET("/accel/subscriptions", controller.AdminListAccelSubs)
				// 守卫放行/拦截计数（hc52）：拦截突增说明域名被传播。
				adminRoute.GET("/accel/ingress/stats", controller.GetAccelIngressStats)				// 线路查询与用户端共用 /accel/lines：带 ?group= 时要求管理员角色，
				// 单独开一条 admin 路由会和 selfRoute 撞同一个 path+method。
				adminRoute.POST("/accel/subscriptions/extend", controller.AdminExtendAccelSub)
				adminRoute.POST("/accel/subscriptions/revoke", controller.AdminRevokeAccelSub)
				// QQ 机器人接入配置（v1.1.0 M2）：服务令牌 + 免费模型门禁开关。
				// 单独一组接口，避免把令牌混进用户态 /api/user/lottery 响应。
				adminRoute.GET("/lottery/qq/settings", controller.AdminGetQQAgentSettings)
				adminRoute.PUT("/lottery/qq/settings", controller.AdminUpdateQQAgentSettings)
				// 撤销令牌（DELETE）与一键轮换（POST rotate，明文只回一次）
				adminRoute.DELETE("/lottery/qq/settings", controller.AdminClearQQAgentToken)
				adminRoute.POST("/lottery/qq/settings/rotate", controller.AdminRotateQQAgentToken)
				adminRoute.GET("/search", controller.SearchUsers)
				// hc34 用户消耗排行榜：窗口内消费额度倒序前 N 名（管理员专属）
				adminRoute.GET("/consumption_rank", controller.GetUserConsumptionRank)
				adminRoute.GET("/:id/oauth/bindings", controller.GetUserOAuthBindingsByAdmin)
				adminRoute.DELETE("/:id/oauth/bindings/:provider_id", controller.UnbindCustomOAuthByAdmin)
				adminRoute.DELETE("/:id/bindings/:binding_type", controller.AdminClearUserBinding)
				adminRoute.GET("/:id", controller.GetUser)
				adminRoute.POST("/", controller.CreateUser)
				adminRoute.POST("/manage", controller.ManageUser)
				adminRoute.PUT("/", controller.UpdateUser)
				adminRoute.DELETE("/:id", controller.DeleteUser)
				adminRoute.DELETE("/:id/reset_passkey", controller.AdminResetPasskey)

				// Admin 2FA routes
				adminRoute.GET("/2fa/stats", controller.Admin2FAStats)
				adminRoute.DELETE("/:id/2fa", controller.AdminDisable2FA)
			}
		}

		// Subscription billing (plans, purchase, admin management)
		subscriptionRoute := apiRouter.Group("/subscription")
		subscriptionRoute.Use(middleware.UserAuth())
		{
			subscriptionRoute.GET("/plans", controller.GetSubscriptionPlans)
			subscriptionRoute.GET("/self", controller.GetSubscriptionSelf)
			subscriptionRoute.PUT("/self/preference", controller.UpdateSubscriptionPreference)
			subscriptionRoute.POST("/self/:id/cancel", middleware.CriticalRateLimit(), controller.UserCancelSubscription)
			subscriptionRoute.POST("/balance/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestBalancePay)
			subscriptionRoute.POST("/epay/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestEpay)
			subscriptionRoute.POST("/stripe/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestStripePay)
			subscriptionRoute.POST("/creem/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestCreemPay)
			subscriptionRoute.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestWaffoPancakePay)
		}
		subscriptionAdminRoute := apiRouter.Group("/subscription/admin")
		subscriptionAdminRoute.Use(middleware.AdminAuth())
		{
			subscriptionAdminRoute.GET("/plans", controller.AdminListSubscriptionPlans)
			subscriptionAdminRoute.POST("/plans", controller.AdminCreateSubscriptionPlan)
			subscriptionAdminRoute.PUT("/plans/:id", controller.AdminUpdateSubscriptionPlan)
			subscriptionAdminRoute.PATCH("/plans/:id", controller.AdminUpdateSubscriptionPlanStatus)
			subscriptionAdminRoute.POST("/bind", controller.AdminBindSubscription)
			subscriptionAdminRoute.POST("/plans/:id/subscriptions/reset", controller.AdminResetPlanSubscriptions)

			// User subscription management (admin)
			subscriptionAdminRoute.GET("/users/:id/subscriptions", controller.AdminListUserSubscriptions)
			subscriptionAdminRoute.POST("/users/:id/subscriptions", controller.AdminCreateUserSubscription)
			subscriptionAdminRoute.POST("/users/:id/subscriptions/reset", controller.AdminResetUserSubscriptionsByPlan)
			subscriptionAdminRoute.POST("/user_subscriptions/:id/invalidate", controller.AdminInvalidateUserSubscription)
			subscriptionAdminRoute.DELETE("/user_subscriptions/:id", controller.AdminDeleteUserSubscription)
		}

		// Subscription payment callbacks (no auth)
		apiRouter.POST("/subscription/epay/notify", anonymousRequestBodyLimit, controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/notify", controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/return", controller.SubscriptionEpayReturn)
		apiRouter.POST("/subscription/epay/return", anonymousRequestBodyLimit, controller.SubscriptionEpayReturn)
		vguardRoute := apiRouter.Group("/vguard")
		vguardRoute.Use(middleware.RootAuth())
		{
			vguardRoute.GET("/status", controller.GetVGuardStatus)
			vguardRoute.GET("/events", controller.GetVGuardEvents)
			vguardRoute.GET("/events/export", controller.ExportVGuardEvents)
			vguardRoute.GET("/events/:id", controller.GetVGuardEventDetail)
			vguardRoute.GET("/daily", controller.GetVGuardDaily)
			vguardRoute.POST("/unblock", controller.PostVGuardUnblock)
		}
		realnameRoute := apiRouter.Group("/realname")
		realnameRoute.Use(middleware.RootAuth())
		{
			realnameRoute.GET("/admin/list", controller.GetRealNameAdminList)
			realnameRoute.POST("/admin/review", controller.PostRealNameReview)
		}

		optionRoute := apiRouter.Group("/option")
		optionRoute.Use(middleware.RootAuth())
		{
			optionRoute.GET("/", controller.GetOptions)
			optionRoute.GET("/request_policy", controller.GetRequestPolicy)
			optionRoute.PATCH("/request_policy", controller.UpdateRequestPolicy)
			optionRoute.PUT("/", controller.UpdateOption)
			optionRoute.PUT("/passkey/domains", controller.UpdatePasskeyDomains)
			optionRoute.GET("/model_pricing", controller.GetModelPricingConfig)
			optionRoute.GET("/model_pricing/issues", controller.GetModelPricingIssues)
			optionRoute.PATCH("/model_pricing", controller.UpdateModelPricingConfig)
			optionRoute.POST("/model_pricing/convert", controller.PreviewModelPricingConversion)
			optionRoute.POST("/model_pricing/preview", controller.PreviewModelPricing)
			optionRoute.POST("/payment_compliance", controller.ConfirmPaymentCompliance)
			optionRoute.GET("/channel_affinity_cache", controller.GetChannelAffinityCacheStats)
			// hc31 · B213 通知事件覆盖表
			optionRoute.GET("/notify_event_catalog", controller.GetNotifyEventCatalog)
			optionRoute.DELETE("/channel_affinity_cache", controller.ClearChannelAffinityCache)
			optionRoute.POST("/ratio_diff_preview", controller.PreviewRatioDiff)
			optionRoute.POST("/rest_model_ratio", controller.ResetModelRatio)
			// 第三方价格表导入（v1.1.0 M2 · B223）：LiteLLM / OpenRouter → 倍率
			optionRoute.POST("/import_model_configs", controller.ImportModelConfigs)
			optionRoute.GET("/waffo-pancake/catalog", controller.ListWaffoPancakeCatalog)
			optionRoute.POST("/waffo-pancake/pair", controller.CreateWaffoPancakePair)
			optionRoute.POST("/waffo-pancake/save", controller.SaveWaffoPancake)
			optionRoute.POST("/waffo-pancake/subscription-product", controller.CreateWaffoPancakeSubscriptionProduct)
			optionRoute.GET("/waffo-pancake/subscription-product-options", controller.ListWaffoPancakeSubscriptionProductOptions)
		}

		// Custom OAuth provider management (root only)
		customOAuthRoute := apiRouter.Group("/custom-oauth-provider")
		customOAuthRoute.Use(middleware.RootAuth())
		{
			customOAuthRoute.POST("/discovery", controller.FetchCustomOAuthDiscovery)
			customOAuthRoute.GET("/", controller.GetCustomOAuthProviders)
			customOAuthRoute.GET("/:id", controller.GetCustomOAuthProvider)
			customOAuthRoute.POST("/", controller.CreateCustomOAuthProvider)
			customOAuthRoute.PUT("/:id", controller.UpdateCustomOAuthProvider)
			customOAuthRoute.DELETE("/:id", controller.DeleteCustomOAuthProvider)
		}
		// v1.1.0 M2 · B13 数据库备份导出（仅 root）：SQLite 直接导出库文件，
		// 其他库返回 501 引导使用 mysqldump/pg_dump。
		backupRoute := apiRouter.Group("/backup")
		backupRoute.Use(middleware.DisableCache(), middleware.RootAuth())
		{
			backupRoute.GET("/export", controller.ExportDatabaseBackup)
		}

		// v1.1.0 M2 · B12 Redis 健康状态（仅 root）：限流是否正在降级、
		// 连续失败计数、最近错误与恢复时间。
		redisHealthRoute := apiRouter.Group("/health")
		redisHealthRoute.Use(middleware.DisableCache(), middleware.RootAuth())
		{
			redisHealthRoute.GET("/redis", controller.GetRedisHealth)
		}

		performanceRoute := apiRouter.Group("/performance")
		performanceRoute.Use(middleware.RootAuth())
		{
			performanceRoute.GET("/stats", controller.GetPerformanceStats)
			performanceRoute.DELETE("/disk_cache", controller.ClearDiskCache)
			performanceRoute.POST("/reset_stats", controller.ResetPerformanceStats)
			performanceRoute.POST("/gc", controller.ForceGC)
			performanceRoute.GET("/logs", controller.GetLogFiles)
			performanceRoute.DELETE("/logs", controller.CleanupLogFiles)
		}
		ratioSyncRoute := apiRouter.Group("/ratio_sync")
		ratioSyncRoute.Use(middleware.RootAuth())
		{
			ratioSyncRoute.GET("/channels", controller.GetSyncableChannels)
			ratioSyncRoute.POST("/fetch", controller.FetchUpstreamRatios)
			// 一键同步官方定价：只刷新「官方参考价」快照供排行榜使用，
			// 改本站倍率仍然走前端确认流程（fetch -> 选择 -> Apply Sync）。
			ratioSyncRoute.POST("/official/refresh", controller.RefreshOfficialPricing)
		}
		taskPluginRoute := apiRouter.Group("/plugin/task")
		taskPluginRoute.Use(middleware.RootAuth())
		{
			taskPluginRoute.GET("", controller.ListTaskPlugins)
			taskPluginRoute.POST("", controller.UploadTaskPlugin)
			taskPluginRoute.PUT("", controller.UploadTaskPlugin)
			taskPluginRoute.GET("/runtime/status", controller.GetTaskPluginRuntime)
			taskPluginRoute.GET("/marketplace/sources", controller.GetTaskPluginMarketplaceSources)
			taskPluginRoute.PUT("/marketplace/sources", controller.UpdateTaskPluginMarketplaceSources)
			taskPluginRoute.GET("/:key", controller.GetTaskPlugin)
			taskPluginRoute.GET("/:key/icon", controller.GetTaskPluginIcon)
			taskPluginRoute.GET("/:key/versions", controller.GetTaskPluginVersions)
			taskPluginRoute.POST("/:key/activate", controller.ActivateTaskPlugin)
			taskPluginRoute.POST("/:key/status", controller.SetTaskPluginStatus)
			taskPluginRoute.POST("/:key/dryrun", controller.DryRunTaskPlugin)
			taskPluginRoute.DELETE("/:key/versions/:version", controller.DeleteTaskPluginVersion)
		}
		handlePermissionRoute(apiRouter.Group("", middleware.AdminAuth()), http.MethodGet, "/task_plugin_options", authz.TaskPluginBind, controller.GetTaskPluginOptions)
		// Channel editors read the instances to bind them; changing them is
		// plugin management, as installing a plugin is.
		handlePermissionRoute(apiRouter.Group("", middleware.AdminAuth()), http.MethodGet, "/request_plugin/instances", authz.TaskPluginBind, controller.GetRequestPluginInstances)
		requestPluginRoute := apiRouter.Group("/request_plugin")
		requestPluginRoute.Use(middleware.RootAuth())
		{
			requestPluginRoute.PUT("/instances", controller.UpdateRequestPluginInstances)
			requestPluginRoute.POST("/dry_run", controller.DryRunRequestPlugins)
		}
		registerChannelRoutes(apiRouter)
		registerAuthzRoutes(apiRouter)
		tokenRoute := apiRouter.Group("/token")
		tokenRoute.Use(middleware.UserAuth())
		tokenRoute.Use(middleware.TokenOperationAudit())
		{
			tokenRoute.GET("/", controller.GetAllTokens)
			tokenRoute.GET("/search", middleware.SearchRateLimit(), controller.SearchTokens)
			tokenRoute.GET("/auto-groups", controller.GetTokenAutoGroups)
			tokenRoute.GET("/:id", controller.GetToken)
			tokenRoute.POST("/:id/key", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.GetTokenKey)
			tokenRoute.POST("/", controller.AddToken)
			tokenRoute.PUT("/", controller.UpdateToken)
			tokenRoute.DELETE("/:id", controller.DeleteToken)
			tokenRoute.POST("/batch", controller.DeleteTokenBatch)
			tokenRoute.POST("/batch/keys", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.GetTokenKeysBatch)
		}
		// v1.1.0 M2 · B93 / hc36：管理端密钥泄露风险巡检跨用户汇总，必须管理员。
		// 修复越权：原先挂在 tokenRoute（仅 UserAuth），任何已登录用户都能拉到
		// 全站所有用户的密钥风险证据（用户名、来源 IP、用量）。
		tokenAdminRoute := apiRouter.Group("/token")
		tokenAdminRoute.Use(middleware.AdminAuth())
		tokenAdminRoute.Use(middleware.TokenOperationAudit())
		{
			tokenAdminRoute.GET("/risk/overview", controller.GetTokenRiskOverview)
		}

		// 号池 key-pool：商汤/英伟达 key 验活后加入号池并生成固定额度 token
		keyPoolRoute := apiRouter.Group("/key-pool")
		keyPoolRoute.Use(middleware.AdminAuth())
		{
			keyPoolRoute.GET("/providers", controller.GetKeyPoolProviders)
			keyPoolRoute.POST("/generate", controller.AdminGenerateKeyPool)
			// 后台号池运营管理：设置、概览、渠道视图、Token 视图、单 key 启停、渠道验活
			keyPoolRoute.GET("/settings", controller.GetKeyPoolSettings)
			keyPoolRoute.PUT("/settings", controller.UpdateKeyPoolSettings)
			keyPoolRoute.GET("/overview", controller.GetKeyPoolOverview)
			keyPoolRoute.GET("/channels", controller.GetKeyPoolChannels)
			keyPoolRoute.GET("/tokens", controller.GetKeyPoolTokens)
			keyPoolRoute.GET("/accounts", controller.GetKeyPoolAccounts)
			keyPoolRoute.GET("/channels/:id/keys", controller.GetKeyPoolChannelKeys)
			keyPoolRoute.POST("/channels/:id/test", controller.TestKeyPoolChannel)
			keyPoolRoute.PUT("/channels/:id/keys/:index", controller.SetKeyPoolKeyStatus)
			keyPoolRoute.POST("/tokens/:id/reset-quota", controller.ResetKeyPoolTokenQuota)
		}
		userKeyPoolRoute := apiRouter.Group("/user/key-pool")
		userKeyPoolRoute.Use(middleware.UserAuth())
		{
			userKeyPoolRoute.GET("/providers", controller.GetKeyPoolProviders)
			userKeyPoolRoute.POST("/contribute", controller.UserContributeKeyPool)
			userKeyPoolRoute.GET("/cards", controller.GetUserKeyPoolCards)
			userKeyPoolRoute.GET("/settings", controller.GetUserKeyPoolSettings)
			userKeyPoolRoute.GET("/accounts", controller.GetUserKeyPoolAccounts)
		}

		usageRoute := apiRouter.Group("/usage")
		usageRoute.Use(middleware.CORS(), middleware.CriticalRateLimit())
		{
			tokenUsageRoute := usageRoute.Group("/token")
			tokenUsageRoute.Use(middleware.TokenAuthReadOnly())
			{
				tokenUsageRoute.GET("/", controller.GetTokenUsage)
			}
		}

		redemptionRoute := apiRouter.Group("/redemption")
		redemptionRoute.Use(middleware.AdminAuth())
		{
			redemptionRoute.GET("/", controller.GetAllRedemptions)
			redemptionRoute.GET("/search", controller.SearchRedemptions)
			redemptionRoute.GET("/:id", controller.GetRedemption)
			redemptionRoute.POST("/", controller.AddRedemption)
			redemptionRoute.POST("/batch", controller.DeleteRedemptionBatch)
			redemptionRoute.PUT("/", controller.UpdateRedemption)
			redemptionRoute.DELETE("/invalid", controller.DeleteInvalidRedemption)
			redemptionRoute.DELETE("/:id", controller.DeleteRedemption)
		}
		handlePermissionRoute(apiRouter.Group("", middleware.DisableCache(), middleware.AdminAuth()), http.MethodGet, "/audit", authz.AuditRead, controller.GetAuditLogs)
		handlePermissionRoute(apiRouter.Group("", middleware.DisableCache(), middleware.AdminAuth()), http.MethodGet, "/audit/verify", authz.AuditRead, controller.VerifyAuditLogChain)
		apiRouter.GET("/audit/self", middleware.DisableCache(), middleware.UserAuth(), controller.GetAuditLogs)
		logRoute := apiRouter.Group("/log")
		// 对账月结单（v1.1.0 M1 · B26）：按月汇总充值/消耗/退款/挂单。
		logRoute.GET("/statement", middleware.AdminAuth(), controller.GetMonthlyStatement)
		logRoute.GET("/", middleware.AdminAuth(), controller.GetAllLogs)
		logRoute.GET("/stat", middleware.AdminAuth(), controller.GetLogsStat)
		logRoute.GET("/self/stat", middleware.UserAuth(), controller.GetLogsSelfStat)
		logRoute.GET("/channel_affinity_usage_cache", middleware.AdminAuth(), controller.GetChannelAffinityUsageCacheStats)
		logRoute.GET("/search", middleware.AdminAuth(), controller.SearchAllLogs)
		logRoute.GET("/self", middleware.UserAuth(), controller.GetUserLogs)
		logRoute.GET("/export", middleware.AdminAuth(), controller.ExportAllLogs)
		logRoute.GET("/self/export", middleware.UserAuth(), controller.ExportUserLogs)
		logRoute.GET("/self/search", middleware.UserAuth(), middleware.SearchRateLimit(), controller.SearchUserLogs)
		// 扣费明细下钻（v1.1.0 M2 · B60）：/self/detail/:id，也支持 ?request_id= 定位
		logRoute.GET("/self/detail/:id", middleware.UserAuth(), controller.GetSelfLogBillingDetail)
		logRoute.GET("/self/detail", middleware.UserAuth(), controller.GetSelfLogBillingDetail)
		// 请求耗时分布（v1.1.0 M2 · B114）
		logRoute.GET("/self/latency", middleware.UserAuth(), controller.GetSelfLatencyStats)

		systemTaskRoute := apiRouter.Group("/system-task")
		systemTaskRoute.Use(middleware.RootAuth())
		{
			systemTaskRoute.POST("/log-cleanup", controller.CreateLogCleanupSystemTask)
			systemTaskRoute.GET("/list", controller.ListSystemTasks)
			systemTaskRoute.DELETE("/history", controller.DeleteSystemTaskHistory)
			systemTaskRoute.GET("/current", controller.GetCurrentSystemTask)
			systemTaskRoute.GET("/:task_id", controller.GetSystemTask)
		}
		systemInfoRoute := apiRouter.Group("/system-info")
		systemInfoRoute.Use(middleware.RootAuth())
		{
			systemInfoRoute.GET("/instances", controller.ListSystemInstances)
			systemInfoRoute.GET("/sync", controller.GetNodeSync)
			systemInfoRoute.DELETE("/stale-instances", controller.DeleteStaleSystemInstances)
			systemInfoRoute.DELETE("/instances/:node_name", controller.DeleteStaleSystemInstance)
		}

		dataRoute := apiRouter.Group("/data")
		dataRoute.GET("/", middleware.AdminAuth(), controller.GetAllQuotaDates)
		dataRoute.GET("/users", middleware.AdminAuth(), controller.GetQuotaDatesByUser)
		dataRoute.GET("/self", middleware.UserAuth(), controller.GetUserQuotaDates)
		dataRoute.GET("/flow", middleware.AdminAuth(), controller.GetAllFlowQuotaDates)
		dataRoute.GET("/flow/self", middleware.UserAuth(), controller.GetUserFlowQuotaDates)

		logRoute.Use(middleware.CORS(), middleware.CriticalRateLimit())
		{
			logRoute.GET("/token", middleware.TokenAuthReadOnly(), controller.GetLogByKey)
		}
		groupRoute := apiRouter.Group("/group")
		groupRoute.Use(middleware.AdminAuth())
		{
			groupRoute.GET("/", controller.GetGroups)
		}

		prefillGroupRoute := apiRouter.Group("/prefill_group")
		prefillGroupRoute.Use(middleware.AdminAuth())
		{
			prefillGroupRoute.GET("/", controller.GetPrefillGroups)
			prefillGroupRoute.POST("/", controller.CreatePrefillGroup)
			prefillGroupRoute.PUT("/", controller.UpdatePrefillGroup)
			prefillGroupRoute.DELETE("/:id", controller.DeletePrefillGroup)
		}

		mjRoute := apiRouter.Group("/mj")
		mjRoute.GET("/self", middleware.UserAuth(), controller.GetUserMidjourney)
		mjRoute.GET("/", middleware.AdminAuth(), controller.GetAllMidjourney)

		taskRoute := apiRouter.Group("/task")
		{
			taskRoute.GET("/self", middleware.UserAuth(), controller.GetUserTask)
			taskRoute.GET("", middleware.AdminAuth(), controller.GetAllTask)
			taskRoute.GET("/:task_id/artifacts", middleware.UserAuth(), controller.GetDashboardTaskArtifacts)
		}

		vendorRoute := apiRouter.Group("/vendors")
		vendorRoute.Use(middleware.AdminAuth())
		{
			vendorRoute.POST("/operations/preview", controller.PreviewVendorOperation)
			vendorRoute.POST("/operations", controller.ApplyVendorOperation)
			vendorRoute.GET("/", controller.GetAllVendors)
			vendorRoute.GET("/search", controller.SearchVendors)
			vendorRoute.GET("/:id", controller.GetVendorMeta)
			vendorRoute.POST("/", controller.CreateVendorMeta)
			vendorRoute.PUT("/", controller.UpdateVendorMeta)
			vendorRoute.DELETE("/:id", controller.DeleteVendorMeta)
		}

		modelsRoute := apiRouter.Group("/models")
		modelsRoute.Use(middleware.AdminAuth())
		{
			modelsRoute.GET("/sync_upstream/preview", controller.SyncUpstreamPreview)
			modelsRoute.POST("/sync_upstream", controller.SyncUpstreamModels)
			modelsRoute.POST("/delete", controller.BatchDeleteModelMeta)
			modelsRoute.GET("/missing", controller.GetMissingModels)
			modelsRoute.GET("/", controller.GetAllModelsMeta)
			modelsRoute.GET("/search", controller.SearchModelsMeta)
			modelsRoute.GET("/:id", controller.GetModelMeta)
			modelsRoute.POST("/", controller.CreateModelMeta)
			modelsRoute.PUT("/", controller.UpdateModelMeta)
			modelsRoute.DELETE("/:id", controller.DeleteModelMeta)
		}

		// 模型公告位后台管理（v1.1.0 M2 · B64）
		modelNoticeRoute := apiRouter.Group("/model_notice")
		modelNoticeRoute.Use(middleware.AdminAuth())
		{
			modelNoticeRoute.GET("/", controller.GetAllModelNotices)
			modelNoticeRoute.POST("/", controller.AddModelNotice)
			modelNoticeRoute.PUT("/:id", controller.UpdateModelNotice)
			modelNoticeRoute.DELETE("/:id", controller.DeleteModelNotice)
		}

		// Deployments (model deployment management)
		deploymentsRoute := apiRouter.Group("/deployments")
		deploymentsRoute.Use(middleware.AdminAuth())
		{
			deploymentsRoute.GET("/settings", controller.GetModelDeploymentSettings)
			deploymentsRoute.POST("/settings/test-connection", controller.TestIoNetConnection)
			deploymentsRoute.GET("/", controller.GetAllDeployments)
			deploymentsRoute.GET("/search", controller.SearchDeployments)
			deploymentsRoute.POST("/test-connection", controller.TestIoNetConnection)
			deploymentsRoute.GET("/hardware-types", controller.GetHardwareTypes)
			deploymentsRoute.GET("/locations", controller.GetLocations)
			deploymentsRoute.GET("/available-replicas", controller.GetAvailableReplicas)
			deploymentsRoute.POST("/price-estimation", controller.GetPriceEstimation)
			deploymentsRoute.GET("/check-name", controller.CheckClusterNameAvailability)
			deploymentsRoute.POST("/", controller.CreateDeployment)

			deploymentsRoute.GET("/:id", controller.GetDeployment)
			deploymentsRoute.GET("/:id/logs", controller.GetDeploymentLogs)
			deploymentsRoute.GET("/:id/containers", controller.ListDeploymentContainers)
			deploymentsRoute.GET("/:id/containers/:container_id", controller.GetContainerDetails)
			deploymentsRoute.PUT("/:id", controller.UpdateDeployment)
			deploymentsRoute.PUT("/:id/name", controller.UpdateDeploymentName)
			deploymentsRoute.POST("/:id/extend", controller.ExtendDeployment)
			deploymentsRoute.DELETE("/:id", controller.DeleteDeployment)
		}
	}
}
