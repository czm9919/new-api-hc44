package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Token struct {
	Id                 int            `json:"id"`
	UserId             int            `json:"user_id" gorm:"index"`
	Key                string         `json:"key" gorm:"type:varchar(128);uniqueIndex"`
	Status             int            `json:"status" gorm:"default:1"`
	Name               string         `json:"name" gorm:"index" `
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	AccessedTime       int64          `json:"accessed_time" gorm:"bigint"`
	ExpiredTime        int64          `json:"expired_time" gorm:"bigint;default:-1"` // -1 means never expired
	RemainQuota        int            `json:"remain_quota" gorm:"default:0"`
	FreeQuota          int            `json:"free_quota" gorm:"bigint;default:0"`                                              // 免费（每日赠送）余额，是 remain_quota 的一部分；消费时优先扣减它，剩下的即付费余额(remain_quota - free_quota)
	DailyGiftQuota     int            `json:"daily_gift_quota" gorm:"bigint;default:0"`                                        // 号池 token 的「每日额度上限」(管理员定)；每天 00:00(北京时间)重置时把 remain_quota 归位到此值(上游 alive)或 0(上游 dead)
	SourceChannelId    int            `json:"source_channel_id" gorm:"bigint;default:0"`                                       // 号池 token 关联的上游渠道 id（用于每日重置前验活），0 表示无关联（按可用处理）
	DailyQuotaDate     string         `json:"daily_quota_date" gorm:"column:last_daily_gift_date;type:varchar(20);default:''"` // 最近一次每日额度重置日期(yyyy-mm-dd)，复用历史列 last_daily_gift_date；用于幂等、避免重复重置
	UnlimitedQuota     bool           `json:"unlimited_quota"`
	ModelLimitsEnabled bool           `json:"model_limits_enabled"`
	ModelLimits        string         `json:"model_limits" gorm:"type:text"`
	AllowIps           *string        `json:"allow_ips" gorm:"default:''"`
	UsedQuota          int            `json:"used_quota" gorm:"default:0"` // used quota
	Group              string         `json:"group" gorm:"default:''"`
	CrossGroupRetry    bool           `json:"cross_group_retry"` // 跨分组重试，仅auto分组有效
	AutoGroups         string         `json:"-" gorm:"type:text"`
	DeletedAt          gorm.DeletedAt `gorm:"index"`
	// 允许用此令牌查询所属用户的额度；新建令牌默认开启，已有令牌升级后保持关闭
	AllowUserQuotaQuery bool   `json:"allow_user_quota_query"`
	CacheExpiresAt      int64  `json:"-" gorm:"-"`
	CacheMAC            string `json:"-" gorm:"-"`
}

// IsFreeTier 号池发放的 token（关联上游渠道）视为「免费级 key」：
// 只能路由到免费分组白名单（FreeQuotaAllowedGroups）内的分组，
// 与充值/管理员发放的付费 key 天然隔离，无需额外表字段。
func (token *Token) IsFreeTier() bool {
	return token != nil && token.SourceChannelId > 0
}

func (token *Token) GetAutoGroups() ([]string, error) {
	if token.AutoGroups == "" {
		return nil, nil
	}
	var groups []string
	if err := common.UnmarshalJsonStr(token.AutoGroups, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func (token *Token) SetAutoGroups(groups []string) error {
	if len(groups) == 0 {
		token.AutoGroups = ""
		return nil
	}
	data, err := common.Marshal(groups)
	if err != nil {
		return err
	}
	token.AutoGroups = string(data)
	return nil
}

func (token *Token) Clean() {
	token.Key = ""
}

func MaskTokenKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	if len(key) <= 8 {
		return key[:2] + "****" + key[len(key)-2:]
	}
	return key[:4] + "**********" + key[len(key)-4:]
}

func (token *Token) GetFullKey() string {
	return token.Key
}

func (token *Token) GetMaskedKey() string {
	return MaskTokenKey(token.Key)
}

func (token *Token) GetIpLimits() []string {
	// delete empty spaces
	//split with \n
	ipLimits := make([]string, 0)
	if token.AllowIps == nil {
		return ipLimits
	}
	cleanIps := strings.ReplaceAll(*token.AllowIps, " ", "")
	if cleanIps == "" {
		return ipLimits
	}
	ips := strings.SplitSeq(cleanIps, "\n")
	for ip := range ips {
		ip = strings.TrimSpace(ip)
		ip = strings.ReplaceAll(ip, ",", "")
		if ip != "" {
			ipLimits = append(ipLimits, ip)
		}
	}
	return ipLimits
}

func GetAllUserTokens(userId int, startIdx int, num int) ([]*Token, error) {
	var tokens []*Token
	var err error
	err = DB.Where("user_id = ?", userId).Order("id desc").Limit(num).Offset(startIdx).Find(&tokens).Error
	return tokens, err
}

// GetTokensByIds 按 id 批量取 token（hc31 · B93 管理端风险概览用，不限用户）。
func GetTokensByIds(ids []int) ([]*Token, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var tokens []*Token
	err := DB.Where("id IN ?", ids).Find(&tokens).Error
	return tokens, err
}

// GetUserActiveDailyGiftTokens 返回该用户「活跃且带每日额度」的号池 token（status=1、daily_gift_quota>0、
// 且关联了上游渠道 source_channel_id>0）。用于「同供应商多 key 不叠加」的去重判断：已存在此类 token
// 时，新的同供应商贡献只把上游 key 加入号池（增容量），不再发放新的每日订阅额度。
func GetUserActiveDailyGiftTokens(userId int) ([]Token, error) {
	var tokens []Token
	err := DB.Where("user_id = ? AND status = ? AND daily_gift_quota > 0 AND source_channel_id > 0",
		userId, common.TokenStatusEnabled).Find(&tokens).Error
	return tokens, err
}

// sanitizeLikePattern 校验并清洗用户输入的 LIKE 搜索模式。
// 规则：
//  1. 转义 ! 和 _（使用 ! 作为 ESCAPE 字符，兼容 MySQL/PostgreSQL/SQLite）
//  2. 连续的 % 合并为单个 %
//  3. 最多允许 2 个 %
//  4. 含 % 时（模糊搜索），去掉 % 后关键词长度必须 >= 2
//  5. 不含 % 时按精确匹配
func sanitizeLikePattern(input string) (string, error) {
	// 1. 先转义 ESCAPE 字符 ! 自身，再转义 _
	//    使用 ! 而非 \ 作为 ESCAPE 字符，避免 MySQL 中反斜杠的字符串转义问题
	input = strings.ReplaceAll(input, "!", "!!")
	input = strings.ReplaceAll(input, `_`, `!_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}

	// 5. 无 % 时，精确全匹配
	return input, nil
}

func validateLikePattern(input string) error {
	// 1. 连续的 % 直接拒绝
	if strings.Contains(input, "%%") {
		return common.NewMessage("The search pattern cannot contain consecutive % wildcards")
	}

	// 2. 统计 % 数量，不得超过 2
	count := strings.Count(input, "%")
	if count > 2 {
		return common.NewMessage("The search pattern can contain at most 2 % wildcards")
	}

	// 3. 含 % 时，去掉 % 后关键词长度必须 >= 2
	if count > 0 {
		stripped := strings.ReplaceAll(input, "%", "")
		if len(stripped) < 2 {
			return common.NewMessage("Fuzzy search keywords must be at least 2 characters long")
		}
	}

	return nil
}

const searchHardLimit = 100

func SearchUserTokens(userId int, keyword string, token string, offset int, limit int) (tokens []*Token, total int64, err error) {
	// model 层强制截断
	if limit <= 0 || limit > searchHardLimit {
		limit = searchHardLimit
	}
	if offset < 0 {
		offset = 0
	}

	if token != "" {
		token = strings.TrimPrefix(token, "sk-")
	}

	// 超量用户（令牌数超过上限）只允许精确搜索，禁止模糊搜索
	maxTokens := operation_setting.GetMaxUserTokens()
	hasFuzzy := strings.Contains(keyword, "%") || strings.Contains(token, "%")
	if hasFuzzy {
		count, err := CountUserTokens(userId)
		if err != nil {
			common.SysLog(common.LogText("failed to count user tokens: %s", err.Error()))
			return nil, 0, common.NewMessage("Failed to get the token count")
		}
		if int(count) > maxTokens {
			return nil, 0, common.NewMessage("Token count exceeds the limit. Only exact search is allowed, do not use the % wildcard")
		}
	}

	baseQuery := DB.Model(&Token{}).Where("user_id = ?", userId)

	// 非空才加 LIKE 条件，空则跳过（不过滤该字段）
	if keyword != "" {
		keywordPattern, err := sanitizeLikePattern(keyword)
		if err != nil {
			return nil, 0, err
		}
		baseQuery = baseQuery.Where("name LIKE ? ESCAPE '!'", keywordPattern)
	}
	if token != "" {
		tokenPattern, err := sanitizeLikePattern(token)
		if err != nil {
			return nil, 0, err
		}
		baseQuery = baseQuery.Where(commonKeyCol+" LIKE ? ESCAPE '!'", tokenPattern)
	}

	// 先查匹配总数（用于分页，受 maxTokens 上限保护，避免全表 COUNT）
	err = baseQuery.Limit(maxTokens).Count(&total).Error
	if err != nil {
		common.SysError(common.LogText("failed to count search tokens: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to search tokens")
	}

	// 再分页查数据
	err = baseQuery.Order("id desc").Offset(offset).Limit(limit).Find(&tokens).Error
	if err != nil {
		common.SysError(common.LogText("failed to search tokens: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to search tokens")
	}
	return tokens, total, nil
}

func ValidateUserToken(key string) (token *Token, err error) {
	if key == "" {
		return nil, ErrTokenNotProvided
	}
	token, err = GetTokenByKey(key, false)
	if err == nil {
		if token.Status == common.TokenStatusExhausted ||
			token.Status == common.TokenStatusExpired ||
			token.Status != common.TokenStatusEnabled {
			return token, ErrTokenInvalid
		}
		if token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp() {
			if !common.RedisEnabled {
				token.Status = common.TokenStatusExpired
				err := token.SelectUpdate()
				if err != nil {
					common.SysLog(common.LogText("failed to update token status%s", err.Error()))
				}
			}
			return token, ErrTokenInvalid
		}
		if !token.UnlimitedQuota && token.RemainQuota <= 0 {
			if !common.RedisEnabled {
				token.Status = common.TokenStatusExhausted
				err := token.SelectUpdate()
				if err != nil {
					common.SysLog(common.LogText("failed to update token status%s", err.Error()))
				}
			}
			return token, ErrTokenInvalid
		}
		return token, nil
	}
	common.SysLog(common.LogText("ValidateUserToken: failed to get token: %s", err.Error()))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTokenInvalid
	}
	return nil, fmt.Errorf("%w: %v", ErrDatabase, err)
}

func GetTokenByIds(id int, userId int) (*Token, error) {
	if id == 0 || userId == 0 {
		return nil, common.NewMessage("Token ID or user ID is empty")
	}
	token := Token{Id: id, UserId: userId}
	var err error = nil
	err = DB.First(&token, "id = ? and user_id = ?", id, userId).Error
	return &token, err
}

func GetTokenById(id int) (*Token, error) {
	if id == 0 {
		return nil, errors.New("token id is empty")
	}
	token := Token{Id: id}
	var err error = nil
	err = DB.First(&token, "id = ?", id).Error
	return &token, err
}

func GetTokenByKey(key string, fromDB bool) (token *Token, err error) {
	if !fromDB && common.RedisEnabled {
		// Try Redis first
		token, err := cacheGetTokenByKey(key)
		if err == nil {
			return token, nil
		}
		// Don't return error - fall through to DB
	}
	token = &Token{}
	if err = DB.Where(commonKeyCol+" = ?", key).First(token).Error; err != nil {
		return nil, err
	}
	if common.RedisEnabled {
		// 用数据库快照初始化或修复元数据，Lua 保留正在使用的额度计数。
		// 初始化失败不影响本次读取。
		if _, cacheErr := cacheInitToken(*token); cacheErr != nil {
			common.SysLog(common.LogText("failed to init token cache: %s", cacheErr.Error()))
		}
	}
	return token, nil
}

func (token *Token) Insert() error {
	var err error
	err = DB.Create(token).Error
	return err
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (token *Token) Update() (err error) {
	// 写库前失效缓存并设置 fence，防止并发读者把过期快照重新写回缓存。
	if cacheErr := invalidateTokenCacheForMutation(token.Key); cacheErr != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before update: %s", cacheErr.Error()))
	}
	return DB.Model(token).Select("name", "status", "expired_time", "remain_quota", "unlimited_quota",
		"model_limits_enabled", "model_limits", "allow_ips", "group", "cross_group_retry", "auto_groups",
		"allow_user_quota_query").Updates(token).Error
}

func (token *Token) SelectUpdate() (err error) {
	if cacheErr := invalidateTokenCacheForMutation(token.Key); cacheErr != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before status update: %s", cacheErr.Error()))
	}
	// This can update zero values
	return DB.Model(token).Select("accessed_time", "status").Updates(token).Error
}

func (token *Token) Delete() (err error) {
	if cacheErr := invalidateTokenCacheForMutation(token.Key); cacheErr != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before delete: %s", cacheErr.Error()))
	}
	return DB.Delete(token).Error
}

func (token *Token) IsModelLimitsEnabled() bool {
	return token.ModelLimitsEnabled
}

func (token *Token) GetModelLimits() []string {
	if token.ModelLimits == "" {
		return []string{}
	}
	return strings.Split(token.ModelLimits, ",")
}

func (token *Token) GetModelLimitsMap() map[string]bool {
	limits := token.GetModelLimits()
	limitsMap := make(map[string]bool)
	for _, limit := range limits {
		limitsMap[limit] = true
	}
	return limitsMap
}

func DisableModelLimits(tokenId int) error {
	token, err := GetTokenById(tokenId)
	if err != nil {
		return err
	}
	token.ModelLimitsEnabled = false
	token.ModelLimits = ""
	return token.Update()
}

func DeleteTokenById(id int, userId int) (err error) {
	// Why we need userId here? In case user want to delete other's token.
	if id == 0 || userId == 0 {
		return common.NewMessage("Token ID or user ID is empty")
	}
	token := Token{Id: id, UserId: userId}
	err = DB.Where(token).First(&token).Error
	if err != nil {
		return err
	}
	return token.Delete()
}

func IncreaseTokenQuota(tokenId int, key string, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if common.RedisEnabled {
		gopool.Go(func() {
			// 守卫式增量：哈希不存在时跳过，由下次读取从数据库水合，
			// 绝不创建只有配额字段的残缺哈希。
			if _, err := cacheApplyTokenQuotaDelta(tokenId, key, int64(quota)); err != nil {
				common.SysLog(common.LogText("failed to increase token quota: %s", err.Error()))
			}
		})
	}
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeTokenQuota, tokenId, quota)
		return nil
	}
	return increaseTokenQuota(tokenId, quota)
}

func increaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]any{
			"remain_quota":  gorm.Expr("remain_quota + ?", quota),
			"free_quota":    freeQuotaAdjustExpr(quota),
			"used_quota":    gorm.Expr("used_quota - ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	).Error
	return err
}

// GetEligibleDailyResetTokens 取出「待重置每日额度」的号池 token：status=1、
// daily_gift_quota>0（属于号池、有每日额度），且今天尚未重置过（daily_quota_date 为空或不等于 today）。
// 由「每日重置号池额度」定时任务分批调用，配合 ResetTokenDailyQuota 实现幂等（避免重复刷）。
func GetEligibleDailyResetTokens(batchSize int, today string) ([]Token, error) {
	var tokens []Token
	if err := DB.Model(&Token{}).
		Where("status = ? AND daily_gift_quota > 0", common.TokenStatusEnabled).
		Where("last_daily_gift_date IS NULL OR last_daily_gift_date <> ?", today).
		Order("id asc").
		Limit(batchSize).
		Find(&tokens).Error; err != nil {
		return nil, err
	}
	return tokens, nil
}

// ResetTokenDailyQuota 把号池 token 的当日可用余额(remain_quota)重置为 newRemain
// （上游 alive 时为 daily_gift_quota；上游 dead 时为 0），并记录重置日期 today，
// 用于幂等、避免重复重置。注意：此函数直接写库，Redis 缓存需上层在 Redis 开启时另行处理。
func ResetTokenDailyQuota(id int, newRemain int, today string) error {
	// 每日重置时，新的可用余额全部来自「免费额度」，因此 free_quota 与 remain_quota 同步归位。
	return DB.Model(&Token{}).Where("id = ?", id).Updates(map[string]any{
		"remain_quota":         newRemain,
		"free_quota":           newRemain,
		"last_daily_gift_date": today,
	}).Error
}

// BackfillTokenFreeQuota 一次性回填历史 token 的免费余额（free_quota）。
// free_quota 是后加字段，历史数据默认为 0，会把「每日赠送撑起的余额」全算成付费余额。
// 号池 token（daily_gift_quota > 0）的可用余额本来就来自每日赠送，故按 remain_quota 回填；
// 其余 token 不动（其余额本来就算付费）。用 Option 标记保证只执行一次，避免每次启动重复覆盖。
func BackfillTokenFreeQuota() error {
	const markerKey = "token_free_quota_backfilled_v1"
	// 注意：迁移阶段 common.OptionMap 尚未加载（为 nil），这里只能直接读写数据库，
	// 不能走 UpdateOption（它会写内存 map 导致 panic）。
	marker, err := GetOptionValue(markerKey)
	if err != nil {
		return err
	}
	if marker == "1" {
		return nil
	}
	if err := DB.Model(&Token{}).
		Where("daily_gift_quota > 0 AND free_quota = 0 AND remain_quota > 0").
		Update("free_quota", gorm.Expr("remain_quota")).Error; err != nil {
		return err
	}
	opt := Option{Key: markerKey}
	if err := DB.Where("`key` = ?", markerKey).FirstOrCreate(&opt).Error; err != nil {
		return err
	}
	return DB.Model(&Option{}).Where("`key` = ?", markerKey).Update("value", "1").Error
}

// freeQuotaDeductExpr 返回「优先扣免费余额」的 SQL 表达式：免费余额足够时扣减 quota，不足时归零。
// 免费余额始终是 remain_quota 的一部分，因此扣减后 remain_quota - free_quota 即为付费余额。
func freeQuotaDeductExpr(quota int) clause.Expr {
	return gorm.Expr("CASE WHEN free_quota >= ? THEN free_quota - ? ELSE 0 END", quota, quota)
}

// freeQuotaAdjustExpr 同时处理「扣减」与「回补」两个方向的免费余额变化：
//   - 消费(delta<0)：优先扣免费余额，不足部分再吃付费余额；
//   - 回补(delta>=0，如预扣多于实扣的退款)：把退回的部分重新记回免费余额。
//     否则每笔请求的退款都会把免费余额一点点「磨损」成付费余额，分开展示就失真了。
//     回补上限是当日赠送额度(daily_gift_quota)；纯付费 token 该值为 0，不会被误记成免费。
func freeQuotaAdjustExpr(delta int) clause.Expr {
	if delta >= 0 {
		return gorm.Expr(
			"CASE WHEN free_quota + ? <= daily_gift_quota THEN free_quota + ? ELSE daily_gift_quota END",
			delta, delta)
	}
	return freeQuotaDeductExpr(-delta)
}

func DecreaseTokenQuota(id int, key string, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if common.RedisEnabled {
		gopool.Go(func() {
			if _, err := cacheApplyTokenQuotaDelta(id, key, int64(-quota)); err != nil {
				common.SysLog(common.LogText("failed to decrease token quota: %s", err.Error()))
			}
		})
	}
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeTokenQuota, id, -quota)
		return nil
	}
	return decreaseTokenQuota(id, quota)
}

func decreaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]any{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"free_quota":    freeQuotaDeductExpr(quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	).Error
	return err
}

// CountUserTokens returns total number of tokens for the given user, used for pagination
func CountUserTokens(userId int) (int64, error) {
	var total int64
	err := DB.Model(&Token{}).Where("user_id = ?", userId).Count(&total).Error
	return total, err
}

// BatchDeleteTokens 删除指定用户的一组令牌，返回成功删除数量
func BatchDeleteTokens(ids []int, userId int) (int, error) {
	if len(ids) == 0 {
		return 0, common.NewMessage("Token IDs cannot be empty")
	}

	tx := DB.Begin()

	var tokens []Token
	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Find(&tokens).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := invalidateTokensCache(tokens); err != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before batch delete: %s", err.Error()))
	}

	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Delete(&Token{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}

	return len(tokens), nil
}

func GetTokenKeysByIds(ids []int, userId int) ([]Token, error) {
	var tokens []Token
	err := DB.Select("id", commonKeyCol).
		Where("user_id = ? AND id IN (?)", userId, ids).
		Find(&tokens).Error
	return tokens, err
}

// InvalidateUserTokensCache 清理指定用户所有令牌在 Redis 中的缓存，
// 配合 InvalidateUserCache 使用，可在用户被禁用/删除时立即阻断其令牌的请求。
// 下一次请求将从数据库重新加载令牌及用户状态，从而立即识别出被禁用的用户。
func InvalidateUserTokensCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 {
		return errors.New("invalid user id")
	}
	var tokens []Token
	if err := DB.Unscoped().
		Select("id", commonKeyCol).
		Where("user_id = ?", userId).
		Find(&tokens).Error; err != nil {
		return err
	}
	return invalidateTokensCache(tokens)
}

func invalidateTokensCache(tokens []Token) error {
	if !common.RedisEnabled {
		return nil
	}
	var firstErr error
	for _, t := range tokens {
		if t.Key == "" {
			continue
		}
		if err := invalidateTokenCacheForMutation(t.Key); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// GetPoolTokens 查询号池 Token 列表（供后台号池管理页使用）。
//
// channelIds 为空时直接返回空列表——绝不能退化成「无条件查全表」。
// userId > 0 时只返回该用户的；keyword 非空时按 Token 名或密钥模糊匹配
// （密钥列名按数据库方言加引号，兼容 key 这个保留字）。
// 返回本页数据与命中总数，供前端分页。
func GetPoolTokens(channelIds []int, userId int, keyword string, page, pageSize int) ([]Token, int64, error) {
	if len(channelIds) == 0 {
		return []Token{}, 0, nil
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	q := DB.Model(&Token{}).Where("source_channel_id IN ?", channelIds)
	if userId > 0 {
		q = q.Where("user_id = ?", userId)
	}
	if keyword != "" {
		kw := strings.TrimSpace(keyword)
		if len(kw) >= 2 {
			like := "%" + kw + "%"
			q = q.Where("name LIKE ? OR "+commonKeyCol+" LIKE ?", like, like)
		}
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var tokens []Token
	if err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&tokens).Error; err != nil {
		return nil, 0, err
	}
	return tokens, total, nil
}

// UpdatePoolQuotaFields 单独更新号池额度相关的字段。
//
// Token.Update 的 Select 白名单里没有 free_quota / daily_gift_quota / daily_quota_date，
// 用它改不了每日额度字段（号池每日重置与手动补额度都必须用这个方法）。
func (token *Token) UpdatePoolQuotaFields() error {
	return DB.Model(token).
		Select("remain_quota", "free_quota", "daily_gift_quota", "last_daily_gift_date").
		Updates(token).Error
}
