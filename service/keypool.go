package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// KeyPoolDefaultDailyGiftQuotaOptionKey 配置项 key：用户自助贡献时每个号池 token 的
// 默认每日赠送余额，由管理员在后台设定；未配置时回退到 KeyPoolDefaultDailyGiftQuota。
const KeyPoolDefaultDailyGiftQuotaOptionKey = "keypool_default_daily_gift_quota"

// KeyPoolProvider 标识号池接受的上游供应商。
type KeyPoolProvider string

const (
	ProviderSenseNova KeyPoolProvider = "sensenova"
	ProviderNVIDIA    KeyPoolProvider = "nvidia"
)

// providerBaseURL 各供应商的 OpenAI 兼容接口地址（**验活专用**，必须带 /v1 才能访问
// /v1/models、/v1/chat/completions）。
var providerBaseURL = map[KeyPoolProvider]string{
	// 商汤 Token Plan（公测主推）：sk- 开头密钥 + Authorization: Bearer。
	// 旧地址 api.sensenova.cn/compatible-mode/v1 属于 sensecore 兼容模式，使用
	// Access Key ID/Secret 认证，与 Token Plan 的 sk- 密钥不兼容，会导致验活与调用失败。
	ProviderSenseNova: "https://token.sensenova.cn/v1",
	ProviderNVIDIA:    "https://integrate.api.nvidia.com/v1",
}

// providerChannelBaseURL 写入渠道 BaseURL 的接口根地址（**不要求带 /v1**）。
//
// new-api 中继会把客户端请求路径（/v1/chat/completions）原样拼在渠道 BaseURL 之后：
//
//	GetFullRequestURL(baseURL, requestURL) = baseURL + requestURL
//
// 所以渠道里存 https://token.sensenova.cn/v1 时，实际请求会变成
// https://token.sensenova.cn/v1/v1/chat/completions → 404 NOT_FOUND。
// 渠道必须只存到域名根，/v1 由中继补。
var providerChannelBaseURL = map[KeyPoolProvider]string{
	ProviderSenseNova: "https://token.sensenova.cn",
	ProviderNVIDIA:    "https://integrate.api.nvidia.com",
}

// providerDefaultModels 拉不到上游 /models 时的兜底模型列表。
// 商汤 Token Plan 公测期模型集固定且可枚举，NVIDIA 模型过多且 /models 公开可取，故只给商汤兜底。
var providerDefaultModels = map[KeyPoolProvider]string{
	ProviderSenseNova: "sensenova-6.7-flash-lite,sensenova-u1-fast,deepseek-v4-flash",
}

// providerLegacyBaseURL 记录历史版本写进过渠道 BaseURL 的错误地址（换成别的域名的那批）。
// 只在「同域名仅差 /v1」之外的场景使用：那种情况由 normalizeChannelBaseURL 归一后自动命中。
var providerLegacyBaseURL = map[KeyPoolProvider][]string{
	ProviderSenseNova: {"https://api.sensenova.cn/compatible-mode/v1"},
}

// normalizeChannelBaseURL 去掉尾部斜杠和末尾的 /v1：渠道基址不带 /v1，/v1 由中继补。
// （历史上号池把验活地址直接写进了渠道，导致 /v1/v1/... 404。）
func normalizeChannelBaseURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if strings.HasSuffix(u, "/v1") {
		u = strings.TrimSuffix(u, "/v1")
	}
	if u == "" {
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	return u
}

// channelBaseEqual 比较两个渠道基址是否指向同一接口（忽略末尾 /v1 与斜杠差异）。
func channelBaseEqual(a, b string) bool {
	return normalizeChannelBaseURL(a) == normalizeChannelBaseURL(b)
}

// fetchProviderModels 用一条有效 key 拉取上游的模型列表（GET /v1/models）。
func fetchProviderModels(provider KeyPoolProvider, key string) ([]string, error) {
	base, ok := providerBaseURL[provider]
	if !ok {
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
	url := strings.TrimRight(base, "/") + "/models"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models endpoint returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	// 兼容两种返回形态：OpenAI 的 {"data":[{"id":"..."}]} 与裸数组 [{"id":"..."}]。
	var wrapper struct {
		Data []struct {
			Id string `json:"id"`
		} `json:"data"`
	}
	raw := make([]struct {
		Id string `json:"id"`
	}, 0)
	if err := json.Unmarshal(body, &wrapper); err == nil && len(wrapper.Data) > 0 {
		raw = wrapper.Data
	} else if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, m := range raw {
		id := strings.TrimSpace(m.Id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
		if len(models) >= 300 { // 防御：个别供应商模型上千条，避免渠道字段过长
			break
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("empty model list")
	}
	return models, nil
}

// resolvePoolChannelModels 决定号池渠道要绑定的模型列表。
//
// 渠道的 models 是「精确匹配」的（选渠道走 WHERE model = ?），不存在 "*" 通配，
// 所以把 "*" 原样写进渠道等于只支持一个叫 "*" 的模型，实际请求一律「无可用渠道」。
// 因此当管理员/用户留空或填 "*" 时，改为用有效 key 从上游拉真实模型列表。
func resolvePoolChannelModels(provider KeyPoolProvider, requested string, keys []string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" && requested != "*" {
		return requested, nil
	}
	// 运营在后台关闭了「自动绑定上游模型」时，不再拉取上游列表：
	// 回落到内置兜底模型；连兜底都没有则要求手填（否则渠道 models 为空，
	// 任何模型都匹配不到，等于渠道建出来也用不了）。
	if !GetKeyPoolSettings().AutoBindModels {
		if fallback := strings.TrimSpace(providerDefaultModels[provider]); fallback != "" {
			return fallback, nil
		}
		return "", fmt.Errorf("已关闭自动绑定模型，请手动填写模型后重试")
	}
	for _, k := range keys {
		models, err := fetchProviderModels(provider, k)
		if err != nil || len(models) == 0 {
			continue
		}
		return strings.Join(models, ","), nil
	}
	if fallback := strings.TrimSpace(providerDefaultModels[provider]); fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("无法自动获取 %s 的模型列表，请手动填写模型后重试", providerLabel[provider])
}

// isLegacyBaseURL 判断给定地址是否是该供应商的历史地址。
func isLegacyBaseURL(provider KeyPoolProvider, baseURL string) bool {
	for _, legacy := range providerLegacyBaseURL[provider] {
		if legacy == baseURL {
			return true
		}
	}
	return false
}

// providerLabel 用于渠道/号池命名的中文标签。
var providerLabel = map[KeyPoolProvider]string{
	ProviderSenseNova: "商汤SenseNova",
	ProviderNVIDIA:    "英伟达NVIDIA",
}

// providerPoolName 每个供应商对应的「号池渠道」名称。用户贡献（或运营生成）的同一供应商 key
// 会自动合并进该渠道，形成真正的「英伟达号池 / 商汤号池」单一渠道。
var providerPoolName = map[KeyPoolProvider]string{
	ProviderSenseNova: "商汤号池",
	ProviderNVIDIA:    "英伟达号池",
}

// KeyPoolProviderInfo 返回给前端下拉框的供应商信息。
type KeyPoolProviderInfo struct {
	Id    string `json:"id"`
	Label string `json:"label"`
}

// ListKeyPoolProviders 返回支持的供应商列表。
func ListKeyPoolProviders() []KeyPoolProviderInfo {
	return []KeyPoolProviderInfo{
		{Id: string(ProviderSenseNova), Label: providerLabel[ProviderSenseNova]},
		{Id: string(ProviderNVIDIA), Label: providerLabel[ProviderNVIDIA]},
	}
}

// KeyPoolRequest 生成号池的请求参数。
type KeyPoolRequest struct {
	Provider       string `json:"provider"`         // sensenova / nvidia
	Keys           string `json:"keys"`             // 上游 key，每行一个
	UserId         int    `json:"user_id"`          // 号池归属用户（运营侧指定；用户侧用当前用户）
	TokenCount     int    `json:"token_count"`      // 号池中 token（账号）数量
	QuotaPerToken  int    `json:"quota_per_token"`  // 每个 token 的固定额度
	DailyGiftQuota int    `json:"daily_gift_quota"` // 每个 token 的每日赠送余额（与固定额度分开存储、分开展示）
	Models         string `json:"models"`           // 渠道绑定的模型；留空或 "*" 表示自动从上游拉取
	Group          string `json:"group"`            // 中转分组，token 与渠道共用以便路由
	NamePrefix     string `json:"name_prefix"`      // 渠道/号池命名前缀
	UnlimitedQuota bool   `json:"unlimited_quota"`  // 号池 token 是否无限额度
	BaseURL        string `json:"base_url"`         // 可选：覆盖默认接口地址
	// Account 上游账号标识（如注册邮箱/用户名）。同一上游账号只能贡献一把密钥，
	// 用于拦截「同一账号下新建第二把 key 再贡献一次」的虚增容量行为。
	Account string `json:"account"`
	// EnforceAccountDedup 是否强制校验上游账号唯一（仅用户自助通道为 true）。
	// 运营侧后台发放可能确实需要把同一账号的多把 key 都收进号池，不拦截。
	EnforceAccountDedup bool `json:"-"`
	// NoEmptyTokens 为 true 时，若本次没有任何可发放额度（无每日额度、无固定额度、非无限额度），
	// 只把密钥并入号池增加容量，不生成 0 额度的空 token。用户自助通道使用该模式。
	NoEmptyTokens bool `json:"-"`
}

// KeyPoolResult 生成号池的结果。
type KeyPoolResult struct {
	ChannelsCreated int                `json:"channels_created"`  // 本次新建的号池渠道数（合并进已有渠道时为 0）
	PoolChannelId   int                `json:"pool_channel_id"`   // 合并后的号池渠道 ID（英伟达号池 / 商汤号池）
	PoolChannelName string             `json:"pool_channel_name"` // 号池渠道名称
	ValidCount      int                `json:"valid_count"`       // 通过验活的有效 key 数
	Models          string             `json:"models"`            // 号池渠道实际绑定的模型（逗号分隔）
	TokensCreated   int                `json:"tokens_created"`
	TokenKeys       []string           `json:"token_keys"`   // 完整 key（仅此处返回一次）
	Tokens          []KeyPoolTokenInfo `json:"tokens"`       // 每个 token 的额度明细（固定余额 + 每日赠送余额）
	InvalidKeys     []string           `json:"invalid_keys"` // 未通过验活的 key（已脱敏）
	Message         string             `json:"message"`
}

// KeyPoolTokenInfo 单个号池 token 的额度明细。
type KeyPoolTokenInfo struct {
	Key            string `json:"key"`
	RemainQuota    int    `json:"remain_quota"`     // 固定余额
	DailyGiftQuota int    `json:"daily_gift_quota"` // 每日赠送余额
	FreeQuota      int    `json:"free_quota"`       // 其中属于免费（每日额度）的部分
	PaidQuota      int    `json:"paid_quota"`       // 其中属于付费（固定额度）的部分
}

// KeyPoolDefaultQuota 用户自助贡献时每个号池 token 的默认固定额度（quota 单位）。
const KeyPoolDefaultQuota = 500000

// KeyPoolDefaultDailyGiftQuota 用户自助贡献时每个号池 token 的默认每日赠送余额（quota 单位）。
// 运营侧批量生成如不填写则默认为 0（即不赠送）。
const KeyPoolDefaultDailyGiftQuota = 50000

func strPtr(s string) *string { return &s }

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// nvidiaProbeModel 用于 NVIDIA 鉴权探针的模型名。NVIDIA 的 /v1/models 为公开接口（无需鉴权即
// 返回 200），无法据此判断 key 是否有效，因此改用 /v1/chat/completions 做鉴权探针：无效 key 返回
// 401/403，有效 key 即使模型不存在也不会返回 401。该模型需在路由表中存在（否则 NVIDIA 会在鉴权前
// 直接 404），故取一个当前稳定存在的模型；若该模型被下架，有效/无效 key 都不会返回 401，此时退化为
// “不跳过”（仍按可用处理），属于安全方向的退化。
const nvidiaProbeModel = "01-ai/yi-large"

// ValidateUpstreamKey 验活上游 key 是否仍可用（用于号池加入前的验活，以及每日赠送前的复核）。
// 不同供应商的「可用」判定方式不同：
//   - sensenova: GET /models 需要鉴权，无效 key 返回 401/404，有效 key 返回 200。
//   - nvidia:    /models 公开，改用 /chat/completions 鉴权探针，无效 key 返回 401/403。
func ValidateUpstreamKey(provider KeyPoolProvider, key string) (bool, error) {
	base, ok := providerBaseURL[provider]
	if !ok {
		return false, fmt.Errorf("unsupported provider: %s", provider)
	}
	if provider == ProviderNVIDIA {
		return validateNvidiaKey(base, key)
	}
	return validateByModelsEndpoint(base, key)
}

// validateByModelsEndpoint 通过 OpenAI 兼容的 /models 接口验活（商汤等需要鉴权的供应商）。
func validateByModelsEndpoint(base, key string) (bool, error) {
	url := strings.TrimRight(base, "/") + "/models"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// validateNvidiaKey 通过 /chat/completions 鉴权探针验活 NVIDIA key。
// 返回 true 表示 key 鉴权通过（可用）；401/403 表示 key 无效（不可用）；其余状态（含网络成功但
// 模型相关错误）均视为 key 有效，避免误杀可用 key。
func validateNvidiaKey(base, key string) (bool, error) {
	url := strings.TrimRight(base, "/") + "/chat/completions"
	payload := map[string]any{
		"model":      nvidiaProbeModel,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	// 仅 401/403 表示鉴权失败（key 无效）；其余一律视为 key 有效。
	return resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden, nil
}

// HasActivePoolTokenForProvider 判断该用户是否已存在「活跃且带每日额度」的某供应商号池 token。
// 用于「同供应商多 key 不叠加」：已存在则该供应商的每日订阅额度不应被再次发放（模仿示例
// freeshare 的同供应商多 key 不叠加——多 key 只增加号池容量，不开通第二张订阅卡片）。
func HasActivePoolTokenForProvider(userId int, provider KeyPoolProvider) (bool, error) {
	tokens, err := model.GetUserActiveDailyGiftTokens(userId)
	if err != nil {
		return false, err
	}
	want := "keypool-" + string(provider)
	for _, t := range tokens {
		if t.SourceChannelId <= 0 {
			continue
		}
		ch, e := model.GetChannelById(t.SourceChannelId, true) // 取回 tag 用于比对供应商
		if e != nil || ch == nil || ch.Tag == nil {
			continue
		}
		if *ch.Tag == want {
			return true, nil
		}
	}
	return false, nil
}

// KeyPoolCard 用户视角的「按供应商」号池订阅卡片（严格模仿示例 freeshare 的按供应商独立卡片）。
type KeyPoolCard struct {
	Provider      string `json:"provider"`       // 供应商 id，如 sensenova / nvidia
	ProviderLabel string `json:"provider_label"` // 中文标签
	DailyQuota    int    `json:"daily_quota"`    // 该供应商每日订阅额度上限
	RemainQuota   int    `json:"remain_quota"`   // 今日剩余（即当前 remain_quota 之和）
	FreeQuota     int    `json:"free_quota"`     // 其中属于免费（每日额度）的剩余
	PaidQuota     int    `json:"paid_quota"`     // 其中属于付费（固定额度）的剩余
	NextReset     int64  `json:"next_reset"`     // 下次重置时间戳（北京时间 00:00，秒）
	Alive         bool   `json:"alive"`          // 上游当前是否仍可用（true=正常，false=已归零/摘除）
}

// GetUserKeyPoolCards 返回当前用户「按供应商」的号池订阅卡片：每个有活跃每日订阅 token 的供应商一张卡片，
// 展示每日额度上限、今日剩余、下次重置时间。严格模仿示例 freeshare 的按供应商独立卡片。
func GetUserKeyPoolCards(userId int) ([]KeyPoolCard, error) {
	tokens, err := model.GetUserActiveDailyGiftTokens(userId)
	if err != nil {
		return nil, err
	}
	byProvider := make(map[KeyPoolProvider]*KeyPoolCard)
	for _, t := range tokens {
		if t.SourceChannelId <= 0 {
			continue
		}
		ch, e := model.GetChannelById(t.SourceChannelId, true)
		if e != nil || ch == nil || ch.Tag == nil || !strings.HasPrefix(*ch.Tag, "keypool-") {
			continue
		}
		provider := KeyPoolProvider(strings.TrimPrefix(*ch.Tag, "keypool-"))
		label := providerLabel[provider]
		if label == "" {
			label = string(provider)
		}
		card, ok := byProvider[provider]
		if !ok {
			card = &KeyPoolCard{
				Provider:      string(provider),
				ProviderLabel: label,
				DailyQuota:    t.DailyGiftQuota,
				Alive:         true,
			}
			byProvider[provider] = card
		}
		card.RemainQuota += t.RemainQuota
		card.FreeQuota += t.FreeQuota
	}
	next := nextBeijingMidnight()
	cards := make([]KeyPoolCard, 0, len(byProvider))
	for _, c := range byProvider {
		c.NextReset = next
		// 免费余额不可能超过总剩余，做一次兜底钳制，避免历史数据异常时算出负的付费余额。
		if c.FreeQuota > c.RemainQuota {
			c.FreeQuota = c.RemainQuota
		}
		c.PaidQuota = c.RemainQuota - c.FreeQuota
		// 上游是否仍可用：以「今日剩余余额 > 0」作为卡片可用性的近似（死 key 会在每日重置时被归零，
		// 因此 remain_quota==0 即代表今天已被归零/摘除，与示例的「死 key 当天归零/摘除」一致）。
		c.Alive = c.RemainQuota > 0
		cards = append(cards, *c)
	}
	return cards, nil
}

// nextBeijingMidnight 返回下一个北京时间的 00:00 时间戳（秒）。
func nextBeijingMidnight() int64 {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	now := time.Now().In(loc)
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Add(24 * time.Hour)
	return next.Unix()
}

// getOrCreatePoolChannel 取得（或新建）某个供应商对应的「号池渠道」（英伟达号池 / 商汤号池）。
// 同一供应商、同一接口地址只会有唯一一个号池渠道，贡献 / 生成的上游 key 都会合并进该渠道，
// 形成真正的「号池」。返回渠道、是否本次新建、错误。
func getOrCreatePoolChannel(provider KeyPoolProvider, baseURL, models, group string) (*model.Channel, bool, error) {
	tag := "keypool-" + string(provider)
	existing, err := model.GetChannelsByTag(tag, false, true)
	if err != nil {
		return nil, false, err
	}
	for _, ch := range existing {
		if ch.BaseURL == nil || !channelBaseEqual(*ch.BaseURL, baseURL) {
			continue
		}
		// 命中同一接口的已有渠道：顺手修复历史遗留问题（基址带 /v1、models 是字面量 "*"、
		// 多把 key 却没有多密钥标记）。这三种遗留会让渠道建出来「能用但永远匹配不到/
		// 永远 404 / 整串 key 被当成一把」，必须就地修掉。
		changed := false
		if normalized := normalizeChannelBaseURL(*ch.BaseURL); normalized != *ch.BaseURL {
			bu := normalized
			ch.BaseURL = &bu
			changed = true
		}
		if models != "" && (ch.Models == "" || ch.Models == "*") {
			ch.Models = models
			changed = true
		}
		if ensurePoolChannelMultiKey(ch) {
			changed = true
		}
		if changed {
			if err := ch.Update(); err != nil {
				return nil, false, err
			}
		}
		return ch, false, nil
	}
	// 换域名的历史地址（如商汤 sensecore）：原地改写已有渠道的 BaseURL 并复用，
	// 而不是再建一个同名号池渠道——后者会让历史 token 与新号池指向两个不同渠道。
	for _, ch := range existing {
		if ch.BaseURL == nil || !isLegacyBaseURL(provider, *ch.BaseURL) {
			continue
		}
		bu := baseURL
		ch.BaseURL = &bu
		if models != "" && (ch.Models == "" || ch.Models == "*") {
			ch.Models = models
		}
		ensurePoolChannelMultiKey(ch)
		if err := ch.Update(); err != nil {
			return nil, false, err
		}
		return ch, false, nil
	}
	bu := baseURL
	name := providerPoolName[provider]
	ch := &model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Name:    name,
		Status:  1,
		BaseURL: &bu,
		Models:  models,
		Group:   group,
		Tag:     strPtr(tag),
		// 新建时先给默认轮询策略；真正的 is_multi_key 由调用方按合并后的 key 条数决定
		// （GenerateKeyPool 会在 merge 之后调用 ensurePoolChannelMultiKey 落库）。
		ChannelInfo: model.ChannelInfo{
			MultiKeyMode: constant.MultiKeyModePolling,
		},
	}
	if err := ch.Insert(); err != nil {
		return nil, false, err
	}
	return ch, true, nil
}

// firstChannelKey 取换行分隔的 key 串里的第一条（用于修复渠道时拉取模型列表）。
func firstChannelKey(keyStr string) string {
	for _, k := range strings.Split(keyStr, "\n") {
		if k = strings.TrimSpace(k); k != "" {
			return k
		}
	}
	return ""
}

// ensurePoolChannelMultiKey 保证「渠道里有 2 把以上 key」时渠道一定是多密钥渠道。
//
// 这是「渠道建了但用不了 / 建的不是多密钥分组」的根因：号池渠道合并多把上游 key 后，
// 若 channel_info.is_multi_key 仍为 false，model.Channel.GetNextEnabledKey 会走
// !IsMultiKey 分支，把整串 "key1\nkey2" 当成一把密钥发给上游，请求必然全部鉴权失败。
//
// 同时把 multi_key_mode 补成 polling（默认轮询取 key），避免空值落到 switch 的
// unknown 分支、每次都固定取第一把 key。
//
// 返回 true 表示渠道属性被修改，需要落库。
func ensurePoolChannelMultiKey(ch *model.Channel) bool {
	changed := false
	keyCount := countChannelKeys(ch.Key)
	if keyCount > 1 && !ch.ChannelInfo.IsMultiKey {
		ch.ChannelInfo.IsMultiKey = true
		changed = true
	}
	if ch.ChannelInfo.IsMultiKey && ch.ChannelInfo.MultiKeyMode != constant.MultiKeyModePolling {
		ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModePolling
		changed = true
	}
	return changed
}

// MigrateKeyPoolChannels 修复历史号池渠道（幂等，可重复执行）：
//  1. 基址归一：去掉 /v1 与尾部斜杠。中继会把 /v1/chat/completions 拼在 BaseURL 后，
//     带 /v1 会变成 /v1/v1/... 直接 404（「渠道建了但用不了」的根因）。
//  2. 模型修复：models 仍是字面量 "*" 时，用渠道内的 key 拉一次上游 /models 写回真实列表。
//     渠道 models 是精确匹配，没有通配，"*" 等于只支持一个叫 "*" 的模型。
//  3. 多密钥标记修复：渠道里有 2 把以上 key 却没标 is_multi_key 时，整串 key 会被当成
//     一把密钥发给上游，请求全部鉴权失败（「建的不是多密钥分组」）。
//
// 必须在渠道缓存初始化之前执行，否则缓存里仍是旧基址与旧 channel_info。
func MigrateKeyPoolChannels() error {
	for provider, target := range providerChannelBaseURL {
		tag := "keypool-" + string(provider)
		channels, err := model.GetChannelsByTag(tag, false, true)
		if err != nil {
			return err
		}
		for _, ch := range channels {
			changed := false
			oldBase := ch.GetBaseURL()
			// 仅修复「同一接口但写法不规范」或「已知历史域名」的基址，不动管理员自定义的其它地址。
			if ch.BaseURL != nil && oldBase != target &&
				(channelBaseEqual(oldBase, target) || isLegacyBaseURL(provider, oldBase)) {
				bu := target
				ch.BaseURL = &bu
				changed = true
			}
			if ch.Models == "" || ch.Models == "*" {
				if key := firstChannelKey(ch.Key); key != "" {
					if list, ferr := fetchProviderModels(provider, key); ferr == nil && len(list) > 0 {
						ch.Models = strings.Join(list, ",")
						changed = true
					} else if fallback := providerDefaultModels[provider]; fallback != "" {
						ch.Models = fallback
						changed = true
					}
				}
			}
			multiKeyFixed := ensurePoolChannelMultiKey(ch)
			if multiKeyFixed {
				changed = true
			}
			if !changed {
				continue
			}
			if err := ch.Update(); err != nil {
				return err
			}
			common.SysLog(common.LogText("key pool channel %d (%s) migrated: base %q -> %q, models=%q, multi_key=%v mode=%q",
				ch.Id, ch.Name, oldBase, ch.GetBaseURL(), ch.Models,
				ch.ChannelInfo.IsMultiKey, ch.ChannelInfo.MultiKeyMode))
		}
	}
	return nil
}

// RefreshKeyPoolChannelModels 用渠道内的有效 key 拉取上游 /models 全量列表，
// 并把号池渠道的模型列表同步为上游真实状态（巡检前调用，保证「检测所有模型」
// 而不是只检测渠道已配置的那批）：
//   - 上游新增的模型自动绑定（写入渠道 models + abilities，默认上架）；
//   - 上游已下线的模型自动移除（abilities 行删除，不再参与调度）；
//   - 既有模型保持原样，单模型自动下架的状态不会被重置。
//
// 仅处理号池渠道（tag 前缀 keypool-）；运营关闭 AutoBindModels 时不做任何变更。
// 返回新增与移除的模型数及列表。
func RefreshKeyPoolChannelModels(channel *model.Channel) (added, removed []string, err error) {
	if channel == nil {
		return nil, nil, fmt.Errorf("channel is nil")
	}
	if channel.Tag == nil || !strings.HasPrefix(*channel.Tag, KeyPoolChannelTagPrefix) {
		return nil, nil, fmt.Errorf("not a key pool channel")
	}
	provider := KeyPoolProvider(strings.TrimPrefix(*channel.Tag, KeyPoolChannelTagPrefix))
	if _, ok := providerBaseURL[provider]; !ok {
		return nil, nil, fmt.Errorf("unsupported provider: %s", provider)
	}
	// 运营关闭了「自动绑定上游模型」= 模型列表手管，巡检只测已配置的模型。
	if !GetKeyPoolSettings().AutoBindModels {
		return nil, nil, nil
	}
	var list []string
	for _, k := range channel.GetKeys() {
		models, ferr := fetchProviderModels(provider, k)
		if ferr == nil && len(models) > 0 {
			list = models
			break
		}
	}
	if len(list) == 0 {
		return nil, nil, fmt.Errorf("无法从 %s 上游获取模型列表（所有 key 均拉取失败）", providerLabel[provider])
	}
	return model.SyncChannelModelList(channel, list)
}

// mergeChannelKeys 把新 key 合并进已有渠道的 key 列表（去重，换行分隔）。
//
// 顺序必须稳定：渠道的轮询下标（MultiKeyPollingIndex）和单 key 状态表
// （MultiKeyStatusList）都是按位置索引的。若每次合并都重排，下标会指向另一条
// key——被禁用的坏 key 会被当成可用、好 key 反而被跳过。
// 这里保持「已有 key 原序在前，本次新 key 按提交顺序追加在后」。
func mergeChannelKeys(existingKey string, newKeys []string) string {
	seen := make(map[string]struct{})
	keys := make([]string, 0, len(newKeys)+4)
	appendKey := func(raw string) {
		k := strings.TrimSpace(raw)
		if k == "" {
			return
		}
		if _, dup := seen[k]; dup {
			return
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for _, k := range strings.Split(strings.Trim(existingKey, "\n"), "\n") {
		appendKey(k)
	}
	for _, k := range newKeys {
		appendKey(k)
	}
	return strings.Join(keys, "\n")
}

// countChannelKeys 统计换行分隔的 key 条数。
func countChannelKeys(keyStr string) int {
	n := 0
	for _, k := range strings.Split(keyStr, "\n") {
		if strings.TrimSpace(k) != "" {
			n++
		}
	}
	return n
}

// GenerateKeyPool 把用户给的上游 key 逐个验活后，合并进该供应商对应的「号池渠道」（英伟达号池 / 商汤号池），
// 再生成一批固定额度的 token（号池）发给指定用户。
func GenerateKeyPool(req KeyPoolRequest) (*KeyPoolResult, error) {
	provider := KeyPoolProvider(req.Provider)
	if _, ok := providerBaseURL[provider]; !ok {
		return nil, fmt.Errorf("unsupported provider: %s", req.Provider)
	}
	// 渠道基址不带 /v1：中继会把 /v1/chat/completions 拼在 BaseURL 之后，
	// 存 https://xxx/v1 会变成 https://xxx/v1/v1/chat/completions 直接 404。
	channelBase := providerChannelBaseURL[provider]
	if req.BaseURL != "" {
		channelBase = normalizeChannelBaseURL(req.BaseURL)
	}
	if req.TokenCount <= 0 {
		req.TokenCount = 1
	}
	if req.Group == "" {
		req.Group = "default"
	}
	if req.Models == "" {
		req.Models = "*"
	}
	poolName := providerPoolName[provider]
	if poolName == "" {
		poolName = providerLabel[provider]
	}

	keyList := make([]string, 0)
	seenKey := make(map[string]struct{})
	for k := range strings.SplitSeq(strings.TrimSpace(req.Keys), "\n") {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		// 同一次提交内的重复 key 只计一次，避免重复验活与 ValidCount 虚高。
		if _, dup := seenKey[k]; dup {
			continue
		}
		seenKey[k] = struct{}{}
		keyList = append(keyList, k)
	}
	if len(keyList) == 0 {
		return nil, fmt.Errorf("no upstream keys provided")
	}

	// 用户自助贡献：同一个上游账号只能贡献一把密钥。
	//
	// 同一账号下新建的第二把 key 不会增加上游的真实额度，只会把同一账号重复计入号池
	// （容量虚增，且历史实现会按 token 重复发放每日额度）。这里在验活之前就拦掉，
	// 既省掉无谓的上游请求，也能给出「已是谁贡献的」这种可操作提示。
	if req.EnforceAccountDedup {
		accountKey := model.NormalizePoolAccountKey(req.Account)
		if accountKey == "" {
			return nil, fmt.Errorf("请填写上游账号标识（如账号邮箱/用户名），用于拦截同一账号的重复贡献")
		}
		conflicts, cerr := CheckPoolKeyConflicts(provider, keyList, accountKey)
		if cerr != nil {
			return nil, cerr
		}
		if cerr := FormatPoolKeyConflicts(conflicts); cerr != nil {
			return nil, cerr
		}
	}

	result := &KeyPoolResult{}

	// 逐个验活上游 key，有效则合并进号池渠道，无效则记录（脱敏）。
	validKeys := make([]string, 0)
	invalid := make([]string, 0)
	for _, key := range keyList {
		valid, verr := ValidateUpstreamKey(provider, key)
		if verr != nil || !valid {
			invalid = append(invalid, maskKey(key))
			continue
		}
		validKeys = append(validKeys, key)
	}
	result.InvalidKeys = invalid
	result.ValidCount = len(validKeys)

	// 没有任何通过验活的有效 key：直接返回，绝不生成任何号池 token（「死 key 不发证」，
	// 避免出现 SourceChannelId=0 的悬空 token——那会在每日重置时被误判为始终 alive 而永不归零）。
	if len(validKeys) == 0 {
		result.Message = "没有通过验活的有效 key，未生成任何号池 token（请检查密钥是否正确或已过期）"
		return result, nil
	}

	// 号池渠道的 models 是精确匹配的（选渠道走 WHERE model = ?），没有 "*" 通配；
	// 留空或填 "*" 时改用有效 key 从上游拉真实模型列表，否则渠道建出来也匹配不到任何模型。
	channelModels, merr := resolvePoolChannelModels(provider, req.Models, validKeys)
	if merr != nil {
		return nil, merr
	}
	result.Models = channelModels

	var srcChannelId int
	poolCh, created, e := getOrCreatePoolChannel(provider, channelBase, channelModels, req.Group)
	if e != nil {
		return nil, e
	}
	merged := mergeChannelKeys(poolCh.Key, validKeys)
	needsUpdate := merged != poolCh.Key
	poolCh.Key = merged
	// 号池渠道是多 key 渠道：必须置 IsMultiKey 并按轮询取 key。
	// 漏了这一步时 model.Channel.GetNextEnabledKey 在 !IsMultiKey 分支会把整串
	// "key1\nkey2" 当成一个密钥发给上游，多 key 渠道的请求必然全部鉴权失败。
	if ensurePoolChannelMultiKey(poolCh) {
		needsUpdate = true
	}
	if needsUpdate {
		if err := poolCh.Update(); err != nil {
			return nil, err
		}
	}
	model.InitChannelCache()
	if created {
		result.ChannelsCreated = 1
	}
	result.PoolChannelId = poolCh.Id
	result.PoolChannelName = poolCh.Name
	srcChannelId = poolCh.Id

	// 写入贡献台账（密钥指纹 + 上游账号 + 贡献者），供后续判重使用。
	// 台账写入失败不影响已经完成的并池结果，只记日志。
	RecordPoolContributions(provider, poolCh.Id, req.UserId, req.Account, validKeys)

	// 本次没有任何可发放额度（既无每日额度、也无固定额度，且非无限额度）：只增加号池容量，
	// 不生成 0 额度的空 token，避免用户令牌列表被无意义的空令牌污染。
	if req.NoEmptyTokens && req.DailyGiftQuota <= 0 && req.QuotaPerToken <= 0 && !req.UnlimitedQuota {
		result.TokensCreated = 0
		result.Message = fmt.Sprintf("已把 %d 个有效 key 并入【%s】渠道（%d 个无效已跳过），号池容量已增加；该供应商每日额度已开通，本次不重复发放额度、未新增 Token",
			result.ValidCount, poolName, len(invalid))
		return result, nil
	}

	tokenKeys := make([]string, 0, req.TokenCount)
	tokenInfos := make([]KeyPoolTokenInfo, 0, req.TokenCount)
	for i := 0; i < req.TokenCount; i++ {
		key, err := common.GenerateKey()
		if err != nil {
			return nil, err
		}
		// 号池 token 关联到「号池渠道」（srcChannelId>0），便于每日重置前重新验活：
		// 渠道内多 key 任一可用即视为该 token 上游 alive（不会因某一条死 key 误杀整池）。
		// 初始可用余额 = 每日额度本身（号池 token 的「可用余额」就是每日订阅额度，次日 00:00 重置回该值）；
		// 仅当未配置每日额度（如同供应商叠加贡献时置 0）才回退到固定额度 seed，避免首日就被每日重置清空。
		seedRemain := req.QuotaPerToken
		if req.DailyGiftQuota > 0 {
			seedRemain = req.DailyGiftQuota
		}
		// 由每日额度撑起的余额属于「免费余额」；运营侧固定额度属于「付费余额」。
		freeQuota := 0
		if req.DailyGiftQuota > 0 {
			freeQuota = seedRemain
		}
		token := model.Token{
			UserId:          req.UserId,
			Key:             key,
			Name:            fmt.Sprintf("%s %d", poolName, i+1),
			CreatedTime:     common.GetTimestamp(),
			AccessedTime:    common.GetTimestamp(),
			RemainQuota:     seedRemain,
			FreeQuota:       freeQuota,
			DailyGiftQuota:  req.DailyGiftQuota,
			SourceChannelId: srcChannelId,
			UnlimitedQuota:  req.UnlimitedQuota,
			Status:          1,
			Group:           req.Group,
		}
		if err := token.Insert(); err != nil {
			return nil, err
		}
		tokenKeys = append(tokenKeys, key)
		tokenInfos = append(tokenInfos, KeyPoolTokenInfo{
			Key:            key,
			RemainQuota:    seedRemain,
			DailyGiftQuota: req.DailyGiftQuota,
			FreeQuota:      freeQuota,
			PaidQuota:      seedRemain - freeQuota,
		})
	}
	result.TokenKeys = tokenKeys
	result.Tokens = tokenInfos
	result.TokensCreated = req.TokenCount
	result.Message = fmt.Sprintf("已把 %d 个有效 key 合并进【%s】渠道（%d 个无效已跳过），并为用户 %d 生成 %d 个号池 token",
		result.ValidCount, poolName, len(invalid), req.UserId, result.TokensCreated)
	return result, nil
}

// GetKeyPoolDefaultDailyGiftQuota 返回「用户自助贡献时每个号池 token 的默认每日赠送余额」。
// 优先读取管理员在后台配置的 Option(keypool_default_daily_gift_quota)；未配置或非法时
// 回退到常量 KeyPoolDefaultDailyGiftQuota(50000)。即「赠送额度管理员定」。
func GetKeyPoolDefaultDailyGiftQuota() int {
	v, err := model.GetOptionValue(KeyPoolDefaultDailyGiftQuotaOptionKey)
	if err == nil && v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 {
			return n
		}
	}
	return KeyPoolDefaultDailyGiftQuota
}
