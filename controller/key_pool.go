package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// KeyPoolUserMaxTokensPerSubmit 用户自助贡献单次可生成的号池 token 上限。
const KeyPoolUserMaxTokensPerSubmit = 10

// AdminGenerateKeyPool 运营侧：粘贴商汤/英伟达上游 key，指定目标用户，
// 验活后建成渠道（加入号池），并生成一批固定额度 token（号池）可分发。
func AdminGenerateKeyPool(c *gin.Context) {
	var req service.KeyPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.UserId <= 0 {
		common.ApiErrorT(c, "target user_id is required")
		return
	}
	result, err := service.GenerateKeyPool(req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "message": result.Message, "data": result})
}

// UserContributeKeyPool 用户自助：提交自己的商汤/英伟达 key，验活后加入号池，
// 并按管理员配置的每日额度为自己开通号池订阅（首次贡献生成带每日额度的号池 token）。
func UserContributeKeyPool(c *gin.Context) {
	// 运营可在后台关闭用户自助贡献入口。
	if !service.IsUserContributeAllowed() {
		common.ApiErrorT(c, "自助贡献功能已关闭，请联系管理员")
		return
	}
	var req service.KeyPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	req.UserId = c.GetInt("id")

	// 用户自助通道只接受「供应商 + 上游密钥 + 生成数量」，所有影响额度与路由的字段一律由平台控制，
	// 防止用户自定固定额度 / 无限额度 / 自定义分组 / 自定义接口地址（额度与路由不可由用户决定）。
	req.UnlimitedQuota = false
	req.QuotaPerToken = 0 // 固定额度仅运营侧可发放；用户侧只享受管理员配置的每日额度
	req.Models = "*"
	req.Group = "default"
	req.BaseURL = ""
	req.NamePrefix = ""
	req.NoEmptyTokens = true // 无可发放额度时只并池，不生成空 token
	// 用户自助通道强制校验「同一上游账号只能贡献一把密钥」：
	// 同一账号下新建的第二把 key 不增加上游真实额度，必须拦截。
	req.EnforceAccountDedup = true

	if req.TokenCount <= 0 {
		req.TokenCount = 1
	}
	// 用户自助贡献时限制单次生成的 token 数量，避免一次性自助签发大量号池 token，
	// 同时避免误输入（如 10000）导致批量创建。运营侧接口不受此限制。
	if req.TokenCount > KeyPoolUserMaxTokensPerSubmit {
		req.TokenCount = KeyPoolUserMaxTokensPerSubmit
	}
	// 每日额度由管理员在后台统一配置（keypool_default_daily_gift_quota），用户请求中的该字段一律忽略。
	req.DailyGiftQuota = service.GetKeyPoolDefaultDailyGiftQuota()
	// 同供应商多 key 不叠加：若用户已存在该供应商的活跃「每日订阅」号池 token，则本次只把上游 key
	// 加入号池增加容量，不再发放额度、也不生成空 token（NoEmptyTokens）。
	if has, _ := service.HasActivePoolTokenForProvider(req.UserId, service.KeyPoolProvider(req.Provider)); has {
		req.DailyGiftQuota = 0
	}
	result, err := service.GenerateKeyPool(req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "message": result.Message, "data": result})
}

// GetKeyPoolProviders 返回号池支持的供应商列表（前端下拉框用）。
func GetKeyPoolProviders(c *gin.Context) {
	c.JSON(200, gin.H{"success": true, "data": service.ListKeyPoolProviders()})
}

// GetUserKeyPoolCards 返回当前用户「按供应商」的号池订阅卡片（前端独立卡片展示用，
// 严格模仿示例 freeshare 的按供应商独立额度卡片）。
func GetUserKeyPoolCards(c *gin.Context) {
	userId := c.GetInt("id")
	cards, err := service.GetUserKeyPoolCards(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "data": cards})
}

// ============================ 后台号池运营管理 ============================

// GetKeyPoolSettings 返回号池的后台可配置项。
func GetKeyPoolSettings(c *gin.Context) {
	c.JSON(200, gin.H{"success": true, "data": service.GetKeyPoolSettings()})
}

// UpdateKeyPoolSettings 保存号池的后台可配置项。
func UpdateKeyPoolSettings(c *gin.Context) {
	var req service.KeyPoolSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := service.UpdateKeyPoolSettings(req); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "message": "号池设置已保存", "data": service.GetKeyPoolSettings()})
}

// GetKeyPoolOverview 返回号池总览统计。
func GetKeyPoolOverview(c *gin.Context) {
	overview, err := service.GetKeyPoolOverview()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "data": overview})
}

// GetKeyPoolChannels 返回号池渠道列表（含密钥健康度与 Token 统计）。
func GetKeyPoolChannels(c *gin.Context) {
	channels, err := service.GetKeyPoolChannels()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "data": channels})
}

// GetKeyPoolTokens 返回号池 Token 列表（支持供应商、用户、关键词筛选与分页）。
func GetKeyPoolTokens(c *gin.Context) {
	provider := c.Query("provider")
	keyword := c.Query("keyword")
	userId, _ := strconv.Atoi(c.Query("user_id"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	tokens, total, err := service.GetKeyPoolTokens(provider, userId, keyword, page, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"data":    tokens,
		"total":   total,
		"page":    page,
		"size":    pageSize,
	})
}

// SetKeyPoolKeyStatus 启用/停用号池渠道里的某一把上游密钥。
func SetKeyPoolKeyStatus(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "invalid channel id")
		return
	}
	keyIndex, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		common.ApiErrorT(c, "invalid key index")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := service.SetPoolChannelKeyStatus(channelId, keyIndex, req.Enabled); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "message": "密钥状态已更新"})
}

// TestKeyPoolChannel 逐把验活号池渠道里的上游密钥。
func TestKeyPoolChannel(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "invalid channel id")
		return
	}
	result, err := service.TestKeyPoolChannel(channelId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "data": result})
}

// ResetKeyPoolTokenQuota 立即把号池 Token 的余额重置为每日额度上限。
func ResetKeyPoolTokenQuota(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "invalid token id")
		return
	}
	if err := service.ResetKeyPoolTokenQuota(tokenId); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "message": "号池额度已重置为每日上限"})
}

// GetKeyPoolChannelKeys 返回号池渠道内每把密钥的状态（脱敏）。
func GetKeyPoolChannelKeys(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "invalid channel id")
		return
	}
	keys, err := service.GetPoolChannelKeys(channelId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "data": keys})
}

// UserKeyPoolSettings 返回用户侧可见的号池公开配置（不含管理端内部开关）。
type UserKeyPoolSettings struct {
	// DefaultDailyGiftQuota 贡献后每个号池 Token 的每日额度。
	DefaultDailyGiftQuota int `json:"default_daily_gift_quota"`
	// MaxTokensPerSubmit 单次贡献可生成的 Token 数上限。
	MaxTokensPerSubmit int `json:"max_tokens_per_submit"`
	// AllowContribute 是否开放自助贡献。
	AllowContribute bool `json:"allow_contribute"`
}

// GetUserKeyPoolSettings 返回用户侧可见的号池公开配置。
func GetUserKeyPoolSettings(c *gin.Context) {
	s := service.GetKeyPoolSettings()
	c.JSON(200, gin.H{"success": true, "data": UserKeyPoolSettings{
		DefaultDailyGiftQuota: s.DefaultDailyGiftQuota,
		MaxTokensPerSubmit:    s.UserMaxTokensPerSubmit,
		AllowContribute:       s.AllowUserContribute,
	}})
}

// GetUserKeyPoolAccounts 返回当前用户已贡献过的上游账号（脱敏），
// 供贡献表单提示「该供应商下你已经贡献过哪些账号」，减少重复提交被打回。
func GetUserKeyPoolAccounts(c *gin.Context) {
	userId := c.GetInt("id")
	accounts, err := service.ListUserPoolKeyAccounts(userId, c.Query("provider"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "data": accounts})
}

// GetKeyPoolAccounts 运营后台：查看某供应商下已被占用的上游账号与贡献者。
func GetKeyPoolAccounts(c *gin.Context) {
	accounts, err := service.ListPoolKeyAccounts(c.Query("provider"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{"success": true, "data": accounts})
}
