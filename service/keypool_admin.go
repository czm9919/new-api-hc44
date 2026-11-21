package service

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// =============================================================================
// 号池运营管理：后台可配置项、渠道视图、Token 视图、单 key 启停与渠道验活。
//
// 这一层补的是「号池只能生成、不能管」的缺口：运营侧此前只能靠 GenerateKeyPool
// 盲生成，既看不到号池渠道的真实状态（几把 key、几把被停用、绑了多少模型），
// 也看不到自己发出去的号池 Token，更没有后台配置项可调（每日额度等只能改库）。
// =============================================================================

const (
	// KeyPoolUserMaxTokensPerSubmitOptionKey 用户自助贡献时单次可生成的号池 Token 上限。
	KeyPoolUserMaxTokensPerSubmitOptionKey = "keypool_user_max_tokens_per_submit"
	// KeyPoolAllowUserContributeOptionKey 是否允许用户自助贡献密钥（运营可关闭该入口）。
	KeyPoolAllowUserContributeOptionKey = "keypool_allow_user_contribute"
	// KeyPoolAutoBindModelsOptionKey 号池渠道是否自动从上游拉取模型列表。
	// 关闭后运营必须手填模型，否则渠道 models 为空会导致任何模型都匹配不到。
	KeyPoolAutoBindModelsOptionKey = "keypool_auto_bind_models"
	// KeyPoolChannelTagPrefix 号池渠道的 tag 前缀。
	KeyPoolChannelTagPrefix = "keypool-"
	// FreeQuotaAllowedGroupsOptionKey 免费额度可用分组白名单（全局计费策略）。
	FreeQuotaAllowedGroupsOptionKey = "FreeQuotaAllowedGroups"
	// ModelAutoCheckEnabledOptionKey 单模型自动检查+自动下架开关（monitor_setting 配置节）。
	ModelAutoCheckEnabledOptionKey = "monitor_setting.model_auto_check_enabled"
)

// KeyPoolUserMaxTokensPerSubmitDefault 用户单次贡献生成 Token 数量的默认上限。
const KeyPoolUserMaxTokensPerSubmitDefault = 10

// KeyPoolSettings 号池的后台可配置项。
type KeyPoolSettings struct {
	// DefaultDailyGiftQuota 用户自助贡献时，每个号池 Token 的每日额度上限。
	DefaultDailyGiftQuota int `json:"default_daily_gift_quota"`
	// UserMaxTokensPerSubmit 用户单次贡献可生成的 Token 数量上限。
	UserMaxTokensPerSubmit int `json:"user_max_tokens_per_submit"`
	// AllowUserContribute 是否开放用户自助贡献入口。
	AllowUserContribute bool `json:"allow_user_contribute"`
	// AutoBindModels 号池渠道是否自动绑定上游真实模型列表。
	AutoBindModels bool `json:"auto_bind_models"`
	// FreeQuotaAllowedGroups 免费额度（签到奖励）可用分组白名单，逗号分隔；
	// 空表示不限制（所有分组都优先消耗免费额度）。白名单外的分组只扣付费余额。
	FreeQuotaAllowedGroups string `json:"free_quota_allowed_groups"`
	// ModelAutoCheckEnabled 巡检改为逐模型探测：坏模型自动下架、恢复自动上架。
	// 依赖「自动测试渠道」定时任务（AutoTestChannelEnabled）才会周期执行。
	ModelAutoCheckEnabled bool `json:"model_auto_check_enabled"`
}

// GetKeyPoolSettings 读取号池的后台可配置项（未配置的项回落到默认值）。
func GetKeyPoolSettings() KeyPoolSettings {
	s := KeyPoolSettings{
		DefaultDailyGiftQuota:  GetKeyPoolDefaultDailyGiftQuota(),
		UserMaxTokensPerSubmit: KeyPoolUserMaxTokensPerSubmitDefault,
		AllowUserContribute:    true,
		AutoBindModels:         true,
	}
	if v, err := model.GetOptionValue(KeyPoolUserMaxTokensPerSubmitOptionKey); err == nil && v != "" {
		if n, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil && n > 0 {
			s.UserMaxTokensPerSubmit = n
		}
	}
	if v, err := model.GetOptionValue(KeyPoolAllowUserContributeOptionKey); err == nil && v != "" {
		s.AllowUserContribute = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, err := model.GetOptionValue(KeyPoolAutoBindModelsOptionKey); err == nil && v != "" {
		s.AutoBindModels = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, err := model.GetOptionValue(FreeQuotaAllowedGroupsOptionKey); err == nil {
		s.FreeQuotaAllowedGroups = v
	}
	s.ModelAutoCheckEnabled = operation_setting.GetMonitorSetting().ModelAutoCheckEnabled
	return s
}

// UpdateKeyPoolSettings 保存号池的后台可配置项。
func UpdateKeyPoolSettings(s KeyPoolSettings) error {
	if s.DefaultDailyGiftQuota < 0 {
		return common.NewMessage("每日额度不能为负数")
	}
	if s.UserMaxTokensPerSubmit <= 0 {
		return common.NewMessage("用户单次贡献上限必须大于 0")
	}
	// 白名单逐项去空白，非法形态（全逗号等）直接归空表示不限制。
	groups := model.ParseFreeQuotaAllowedGroups(s.FreeQuotaAllowedGroups)
	allowedRaw := strings.Join(groups, ",")
	return model.UpdateOptionsBulk(map[string]string{
		KeyPoolDefaultDailyGiftQuotaOptionKey:  strconv.Itoa(s.DefaultDailyGiftQuota),
		KeyPoolUserMaxTokensPerSubmitOptionKey: strconv.Itoa(s.UserMaxTokensPerSubmit),
		KeyPoolAllowUserContributeOptionKey:    strconv.FormatBool(s.AllowUserContribute),
		KeyPoolAutoBindModelsOptionKey:         strconv.FormatBool(s.AutoBindModels),
		FreeQuotaAllowedGroupsOptionKey:        allowedRaw,
		ModelAutoCheckEnabledOptionKey:         strconv.FormatBool(s.ModelAutoCheckEnabled),
	})
}

// IsUserContributeAllowed 用户自助贡献入口是否开放。
func IsUserContributeAllowed() bool {
	return GetKeyPoolSettings().AllowUserContribute
}

// KeyPoolChannelInfo 后台看到的号池渠道视图。
type KeyPoolChannelInfo struct {
	Id            int    `json:"id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	ProviderLabel string `json:"provider_label"`
	BaseURL       string `json:"base_url"`
	Group         string `json:"group"`
	Status        int    `json:"status"`
	// IsMultiKey 是否多密钥渠道。多密钥渠道逐把取 key；非多密钥时整串 key 会被当成一把。
	IsMultiKey bool `json:"is_multi_key"`
	// MultiKeyMode 取 key 策略：polling 轮询 / random 随机。
	MultiKeyMode string `json:"multi_key_mode"`
	// KeyCount 渠道内上游密钥总数。
	KeyCount int `json:"key_count"`
	// EnabledKeys 当前可用（未停用）的密钥数。
	EnabledKeys int `json:"enabled_keys"`
	// DisabledKeys 已被停用的密钥数。
	DisabledKeys int `json:"disabled_keys"`
	// ModelCount 绑定的模型数量（渠道模型是精确匹配，无通配）。
	ModelCount int `json:"model_count"`
	// Models 绑定的模型列表（逗号分隔）。
	Models string `json:"models"`
	// TokenCount 关联该渠道的号池 Token 数量。
	TokenCount int `json:"token_count"`
	// TokenRemainQuota 这些 Token 当前剩余额度合计。
	TokenRemainQuota int `json:"token_remain_quota"`
	// TokenDailyQuota 这些 Token 的每日额度上限合计。
	TokenDailyQuota int   `json:"token_daily_quota"`
	CreatedTime     int64 `json:"created_time"`
	TestTime        int64 `json:"test_time"`
	ResponseTime    int64 `json:"response_time"`
	// StatusReason 渠道被停用时的原因（来自 other_info）。
	StatusReason string `json:"status_reason"`
}

// poolChannelTokenStats 号池渠道的 Token 聚合统计。
type poolChannelTokenStats struct {
	SourceChannelId int
	Cnt             int
	Remain          int
	Daily           int
}

// GetKeyPoolChannels 返回全部号池渠道的后台视图（含密钥健康度与 Token 统计）。
func GetKeyPoolChannels() ([]KeyPoolChannelInfo, error) {
	channels, err := model.GetChannelsByTagPrefix(KeyPoolChannelTagPrefix, true)
	if err != nil {
		return nil, err
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].Id < channels[j].Id })

	ids := make([]int, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, ch.Id)
	}
	stats := map[int]poolChannelTokenStats{}
	if len(ids) > 0 {
		var aggs []poolChannelTokenStats
		err := model.DB.Model(&model.Token{}).
			Select("source_channel_id, COUNT(*) as cnt, SUM(remain_quota) as remain, SUM(daily_gift_quota) as daily").
			Where("source_channel_id IN ?", ids).
			Group("source_channel_id").
			Scan(&aggs).Error
		if err != nil {
			return nil, err
		}
		for _, a := range aggs {
			stats[a.SourceChannelId] = a
		}
	}

	out := make([]KeyPoolChannelInfo, 0, len(channels))
	for _, ch := range channels {
		provider := ""
		if ch.Tag != nil {
			provider = strings.TrimPrefix(*ch.Tag, KeyPoolChannelTagPrefix)
		}
		label := providerLabel[KeyPoolProvider(provider)]
		if label == "" {
			label = provider
		}
		keyCount := countChannelKeys(ch.Key)
		disabled := 0
		for idx, st := range ch.ChannelInfo.MultiKeyStatusList {
			if st != common.ChannelStatusEnabled && idx < keyCount {
				disabled++
			}
		}
		enabled := keyCount - disabled
		if enabled < 0 {
			enabled = 0
		}
		st := stats[ch.Id]
		out = append(out, KeyPoolChannelInfo{
			Id:               ch.Id,
			Name:             ch.Name,
			Provider:         provider,
			ProviderLabel:    label,
			BaseURL:          ch.GetBaseURL(),
			Group:            ch.Group,
			Status:           ch.Status,
			IsMultiKey:       ch.ChannelInfo.IsMultiKey,
			MultiKeyMode:     string(ch.ChannelInfo.MultiKeyMode),
			KeyCount:         keyCount,
			EnabledKeys:      enabled,
			DisabledKeys:     disabled,
			ModelCount:       len(ch.GetModels()),
			Models:           ch.Models,
			TokenCount:       st.Cnt,
			TokenRemainQuota: st.Remain,
			TokenDailyQuota:  st.Daily,
			CreatedTime:      ch.CreatedTime,
			TestTime:         ch.TestTime,
			ResponseTime:     int64(ch.ResponseTime),
			StatusReason:     channelStatusReason(ch),
		})
	}
	return out, nil
}

// channelStatusReason 读取渠道 other_info 里的停用原因（没有则返回空串）。
func channelStatusReason(ch *model.Channel) string {
	if ch.OtherInfo == "" {
		return ""
	}
	reason, err := extractStatusReason(ch.OtherInfo)
	if err != nil {
		return ""
	}
	return reason
}

// extractStatusReason 从 other_info JSON 里取出 status_reason。
func extractStatusReason(otherInfo string) (string, error) {
	var info map[string]any
	if err := common.Unmarshal([]byte(otherInfo), &info); err != nil {
		return "", err
	}
	if v, ok := info["status_reason"].(string); ok {
		return v, nil
	}
	return "", nil
}

// KeyPoolTokenRow 后台看到的号池 Token 视图。
type KeyPoolTokenRow struct {
	Id       int    `json:"id"`
	Name     string `json:"name"`
	UserId   int    `json:"user_id"`
	Username string `json:"username"`
	// KeyMask 脱敏后的密钥（仅展示前 4 后 4 位）。
	KeyMask         string `json:"key_mask"`
	Status          int    `json:"status"`
	DailyGiftQuota  int    `json:"daily_gift_quota"`
	RemainQuota     int    `json:"remain_quota"`
	FreeQuota       int    `json:"free_quota"`
	SourceChannelId int    `json:"source_channel_id"`
	Provider        string `json:"provider"`
	ProviderLabel   string `json:"provider_label"`
	CreatedTime     int64  `json:"created_time"`
	AccessedTime    int64  `json:"accessed_time"`
	DailyQuotaDate  string `json:"daily_quota_date"`
}

// GetKeyPoolTokens 返回号池 Token 列表。provider 非空时只返回该供应商的；
// userId > 0 时只返回该用户的；keyword 非空时按 Token 名或密钥模糊匹配。
// 返回列表与总数，供前端分页。
func GetKeyPoolTokens(provider string, userId int, keyword string, page, pageSize int) ([]KeyPoolTokenRow, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}

	// 号池渠道 id → 供应商（渠道 tag 形如 keypool-nvidia）。
	providerByChannel := map[int]string{}
	allPoolIds, err := model.GetChannelIdsByTagPrefix(KeyPoolChannelTagPrefix)
	if err != nil {
		return nil, 0, err
	}
	for _, id := range allPoolIds {
		ch, e := model.GetChannelById(id, false)
		if e != nil || ch == nil || ch.Tag == nil {
			continue
		}
		providerByChannel[id] = strings.TrimPrefix(*ch.Tag, KeyPoolChannelTagPrefix)
	}

	// 按供应商过滤（provider 为空表示不限）。
	providerChIds := make([]int, 0, len(providerByChannel))
	for id, p := range providerByChannel {
		if provider == "" || p == provider {
			providerChIds = append(providerChIds, id)
		}
	}
	if len(providerChIds) == 0 {
		return []KeyPoolTokenRow{}, 0, nil
	}

	tokens, total, err := model.GetPoolTokens(providerChIds, userId, keyword, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	// 用户名映射
	usernames := map[int]string{}
	if len(tokens) > 0 {
		userIds := make([]int, 0, len(tokens))
		for _, t := range tokens {
			userIds = append(userIds, t.UserId)
		}
		type uRow struct {
			Id       int
			Username string
		}
		var uRows []uRow
		if err := model.DB.Model(&model.User{}).Select("id, username").
			Where("id IN ?", userIds).Find(&uRows).Error; err == nil {
			for _, u := range uRows {
				usernames[u.Id] = u.Username
			}
		}
	}

	out := make([]KeyPoolTokenRow, 0, len(tokens))
	for _, t := range tokens {
		p := providerByChannel[t.SourceChannelId]
		label := providerLabel[KeyPoolProvider(p)]
		if label == "" {
			label = p
		}
		out = append(out, KeyPoolTokenRow{
			Id:              t.Id,
			Name:            t.Name,
			UserId:          t.UserId,
			Username:        usernames[t.UserId],
			KeyMask:         maskKey(t.Key),
			Status:          t.Status,
			DailyGiftQuota:  t.DailyGiftQuota,
			RemainQuota:     t.RemainQuota,
			FreeQuota:       t.FreeQuota,
			SourceChannelId: t.SourceChannelId,
			Provider:        p,
			ProviderLabel:   label,
			CreatedTime:     t.CreatedTime,
			AccessedTime:    t.AccessedTime,
			DailyQuotaDate:  t.DailyQuotaDate,
		})
	}
	return out, int(total), nil
}

// SetPoolChannelKeyStatus 启用/停用号池渠道里的某一把上游密钥。
//
// 停用一把坏 key 而不是删掉整个渠道，是号池的核心运维动作：池里其他 key 继续服务，
// 坏 key 被标记后 GetNextEnabledKey 会跳过它。
func SetPoolChannelKeyStatus(channelId int, keyIndex int, enabled bool) error {
	ch, err := model.GetChannelById(channelId, true)
	if err != nil {
		return common.NewMessage("号池渠道不存在")
	}
	if ch.Tag == nil || !strings.HasPrefix(*ch.Tag, KeyPoolChannelTagPrefix) {
		return common.NewMessage("只能操作号池渠道")
	}
	keys := ch.GetKeys()
	if keyIndex < 0 || keyIndex >= len(keys) {
		return common.NewMessage("密钥序号超出范围")
	}
	// 停用前先确保渠道是多密钥渠道，否则整串 key 会被当成一把，停用单把没有意义。
	if !ch.ChannelInfo.IsMultiKey && len(keys) > 1 {
		ch.ChannelInfo.IsMultiKey = true
	}
	if ch.ChannelInfo.MultiKeyMode == "" {
		ch.ChannelInfo.MultiKeyMode = "polling"
	}
	if ch.ChannelInfo.MultiKeyStatusList == nil {
		ch.ChannelInfo.MultiKeyStatusList = make(map[int]int)
	}
	if ch.ChannelInfo.MultiKeyDisabledReason == nil {
		ch.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
	}
	if ch.ChannelInfo.MultiKeyDisabledTime == nil {
		ch.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
	}
	if enabled {
		ch.ChannelInfo.MultiKeyStatusList[keyIndex] = common.ChannelStatusEnabled
		delete(ch.ChannelInfo.MultiKeyDisabledReason, keyIndex)
		delete(ch.ChannelInfo.MultiKeyDisabledTime, keyIndex)
	} else {
		ch.ChannelInfo.MultiKeyStatusList[keyIndex] = common.ChannelStatusManuallyDisabled
		ch.ChannelInfo.MultiKeyDisabledReason[keyIndex] = "管理员手动停用"
		ch.ChannelInfo.MultiKeyDisabledTime[keyIndex] = time.Now().Unix()
	}
	if err := ch.SaveChannelInfo(); err != nil {
		return err
	}
	model.InitChannelCache()
	return nil
}

// KeyPoolKeyInfo 号池渠道里单把上游密钥的明细（对外一律脱敏）。
type KeyPoolKeyInfo struct {
	// Index 密钥在渠道内的序号（停用/启用时用它定位）。
	Index int `json:"index"`
	// KeyMask 脱敏后的密钥（只展示前 4 后 4 位）。
	KeyMask string `json:"key_mask"`
	// Enabled 当前是否可用（未被停用）。
	Enabled bool `json:"enabled"`
	// Reason 停用原因（可用时为空）。
	Reason string `json:"reason"`
	// DisabledAt 停用时间戳（可用时为 0）。
	DisabledAt int64 `json:"disabled_at"`
}

// GetPoolChannelKeys 返回号池渠道内每把密钥的状态（脱敏）。
func GetPoolChannelKeys(channelId int) ([]KeyPoolKeyInfo, error) {
	ch, err := model.GetChannelById(channelId, true)
	if err != nil {
		return nil, common.NewMessage("号池渠道不存在")
	}
	if ch.Tag == nil || !strings.HasPrefix(*ch.Tag, KeyPoolChannelTagPrefix) {
		return nil, common.NewMessage("只能操作号池渠道")
	}
	keys := ch.GetKeys()
	out := make([]KeyPoolKeyInfo, 0, len(keys))
	for i, k := range keys {
		info := KeyPoolKeyInfo{Index: i, KeyMask: maskKey(k), Enabled: true}
		if st, ok := ch.ChannelInfo.MultiKeyStatusList[i]; ok && st != common.ChannelStatusEnabled {
			info.Enabled = false
			info.Reason = ch.ChannelInfo.MultiKeyDisabledReason[i]
			info.DisabledAt = ch.ChannelInfo.MultiKeyDisabledTime[i]
		}
		out = append(out, info)
	}
	return out, nil
}

// KeyPoolKeyTestResult 单把上游密钥的验活结果。
type KeyPoolKeyTestResult struct {
	Index int `json:"index"`
	// KeyMask 脱敏后的密钥。
	KeyMask string `json:"key_mask"`
	Valid   bool   `json:"valid"`
	Error   string `json:"error"`
}

// KeyPoolChannelTestResult 号池渠道整体验活结果。
type KeyPoolChannelTestResult struct {
	ChannelId    int                    `json:"channel_id"`
	ChannelName  string                 `json:"channel_name"`
	Provider     string                 `json:"provider"`
	Results      []KeyPoolKeyTestResult `json:"results"`
	ValidCount   int                    `json:"valid_count"`
	InvalidCount int                    `json:"invalid_count"`
}

// TestKeyPoolChannel 逐把验活号池渠道里的上游密钥。
//
// 与「删除重建渠道」不同，这里只做诊断：不会改动渠道，也不会停用坏 key
// （是否停用由运营在界面上逐把决定，避免误伤）。
func TestKeyPoolChannel(channelId int) (*KeyPoolChannelTestResult, error) {
	ch, err := model.GetChannelById(channelId, true)
	if err != nil {
		return nil, common.NewMessage("号池渠道不存在")
	}
	if ch.Tag == nil || !strings.HasPrefix(*ch.Tag, KeyPoolChannelTagPrefix) {
		return nil, common.NewMessage("只能操作号池渠道")
	}
	provider := KeyPoolProvider(strings.TrimPrefix(*ch.Tag, KeyPoolChannelTagPrefix))
	if _, ok := providerBaseURL[provider]; !ok {
		return nil, common.NewMessage("不支持的供应商")
	}
	keys := ch.GetKeys()
	result := &KeyPoolChannelTestResult{
		ChannelId:   ch.Id,
		ChannelName: ch.Name,
		Provider:    string(provider),
		Results:     make([]KeyPoolKeyTestResult, 0, len(keys)),
	}
	for i, k := range keys {
		r := KeyPoolKeyTestResult{Index: i, KeyMask: maskKey(k)}
		valid, verr := ValidateUpstreamKey(provider, k)
		r.Valid = valid && verr == nil
		if verr != nil {
			r.Error = verr.Error()
		} else if !valid {
			r.Error = "鉴权失败（key 无效或已过期）"
		}
		if r.Valid {
			result.ValidCount++
		} else {
			result.InvalidCount++
		}
		result.Results = append(result.Results, r)
	}
	return result, nil
}

// GetKeyPoolOverview 号池总览：渠道数、密钥健康度、Token 规模。
type KeyPoolOverview struct {
	ChannelCount int `json:"channel_count"`
	TotalKeys    int `json:"total_keys"`
	EnabledKeys  int `json:"enabled_keys"`
	DisabledKeys int `json:"disabled_keys"`
	TokenCount   int `json:"token_count"`
	TokenRemain  int `json:"token_remain_quota"`
	TokenDaily   int `json:"token_daily_quota"`
	AliveTokens  int `json:"alive_tokens"`
	DeadTokens   int `json:"dead_tokens"`
}

// GetKeyPoolOverview 返回号池的整体统计。
func GetKeyPoolOverview() (*KeyPoolOverview, error) {
	channels, err := GetKeyPoolChannels()
	if err != nil {
		return nil, err
	}
	ov := &KeyPoolOverview{ChannelCount: len(channels)}
	for _, c := range channels {
		ov.TotalKeys += c.KeyCount
		ov.EnabledKeys += c.EnabledKeys
		ov.DisabledKeys += c.DisabledKeys
		ov.TokenCount += c.TokenCount
		ov.TokenRemain += c.TokenRemainQuota
		ov.TokenDaily += c.TokenDailyQuota
	}
	// 存活 Token：今日剩余额度 > 0（死 key 会在每日重置时被归零）。
	var ids []int
	for _, c := range channels {
		ids = append(ids, c.Id)
	}
	if len(ids) > 0 {
		var alive int64
		if err := model.DB.Model(&model.Token{}).
			Where("source_channel_id IN ? AND remain_quota > 0", ids).
			Count(&alive).Error; err == nil {
			ov.AliveTokens = int(alive)
			ov.DeadTokens = ov.TokenCount - ov.AliveTokens
		}
	}
	return ov, nil
}

// ResetKeyPoolTokenQuota 立即把某个号池 Token 的余额重置为它的每日额度上限。
// 用于「上游今天恢复了，手动把额度补回来」，避免只能等次日 00:00 自动重置。
func ResetKeyPoolTokenQuota(tokenId int) error {
	token, err := model.GetTokenById(tokenId)
	if err != nil {
		return common.NewMessage("号池 Token 不存在")
	}
	if token.SourceChannelId <= 0 || token.DailyGiftQuota <= 0 {
		return common.NewMessage("该 Token 不是带每日额度的号池 Token")
	}
	token.RemainQuota = token.DailyGiftQuota
	token.FreeQuota = token.DailyGiftQuota
	token.DailyQuotaDate = keypoolTodayBeijing()
	// 必须用 UpdatePoolQuotaFields：Token.Update 的字段白名单不含额度相关字段。
	return token.UpdatePoolQuotaFields()
}
