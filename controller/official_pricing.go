package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// 一键同步官方定价 / 价格排行榜（hc41）
//
// 设计口径：
//   - 官方价一律换算成「美元 / 百万 tokens」，方便和本站倍率直接对比。
//     本站倍率 r 对应 1e6 * r / 1000 quota，再除以 common.QuotaPerUnit 得美元。
//   - 官方 expression（basellm 提供的 tiered_expr）用真实计费引擎
//     billingexpr.RunExpr 求值，不靠正则猜系数。
//   - 官方倍率（models.dev）沿用 ratio_sync.go 的换算，保持两处口径一致。
//   - 快照存在 option 表里，重启后可继续给排行榜供数。

const (
	officialPricingSnapshotOptionKey = "OfficialPricingSnapshot"
	officialPricingSourceBasellm     = "basellm"
	officialPricingSourceModelsDev   = "models.dev"
	// LiteLLM 维护的厂商价表（model_prices_and_context_window.json）覆盖面最广：
	// 大量未在 models.dev 上架的托管模型在这里有按 token 官方报价。
	officialPricingSourceLiteLLM = "litellm"
	// models.dev/api.json 在本机实测要 30~40s（约 700KB gzip），
	// 官方表达式表则 <1s，LiteLLM 价表约 5MB。整体预算给到 150s，
	// 三个源并发跑，别让慢的那个拖死整个刷新。
	officialPricingFetchTimeout = 150 * time.Second
	officialPricingMaxBytes     = 32 << 20
	pricingLeaderboardLimit     = 200
	pricingUnitToken            = "token"
	pricingUnitRequest          = "request"
)

// 上游地址与客户端做成变量：测试里换成 httptest，就能把「一个源失败、沿用上一版」
// 这类分支跑出来，而不必依赖真实网络。
var (
	officialPricingBasellmURL   = officialRatioPresetBaseURL + "/llm-metadata/api/newapi/ratio_config-v1-base.json"
	officialPricingModelsDevURL = modelsDevPresetBaseURL + modelsDevPath
	officialPricingLiteLLMURL   = litellmPresetBaseURL + litellmPresetPath
)

// officialModelPrice 是官方单价的规范化形式。
type officialModelPrice struct {
	ModelName   string  `json:"model_name"`
	Source      string  `json:"source"`
	BillingMode string  `json:"billing_mode,omitempty"`
	BillingExpr string  `json:"billing_expr,omitempty"`
	InputPrice  float64 `json:"input_price"`
	OutputPrice float64 `json:"output_price"`
	CachePrice  float64 `json:"cache_price,omitempty"`
}

// officialSourceState 是单个上游源的抓取状态。
// 条目本身不在这里重复存——快照的 Models 里每一条都带 Source 字段，
// 某个源这次抓失败时按 Source 把上次的条目挑回来沿用即可。
type officialSourceState struct {
	Count     int    `json:"count"`
	FetchedAt int64  `json:"fetched_at"`
	ETag      string `json:"etag,omitempty"`
	LastMod   string `json:"last_modified,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

type officialPricingSnapshot struct {
	// FetchedAt 是最后一次「确认过源」的时间（304 也算）。
	FetchedAt int64 `json:"fetched_at"`
	// UpdatedAt 是内容最后一次真正变化的时间，用来判断该不该提醒运维。
	UpdatedAt int64 `json:"updated_at"`
	// Revision 是合并结果的内容指纹：没变就不必重写快照。
	Revision string         `json:"revision,omitempty"`
	Sources  []string       `json:"sources"`
	Counts   map[string]int `json:"counts"`
	// Carried 列出本次抓取失败、条目沿用自上一版快照的源。
	Carried     []string                       `json:"carried,omitempty"`
	Models      map[string]officialModelPrice  `json:"models"`
	SourceState map[string]officialSourceState `json:"source_state,omitempty"`
}

// officialPricingRefreshResult 是刷新接口返回给前端的摘要。
type officialPricingRefreshResult struct {
	FetchedAt int64          `json:"fetched_at"`
	UpdatedAt int64          `json:"updated_at"`
	Sources   []string       `json:"sources"`
	Counts    map[string]int `json:"counts"`
	Carried   []string       `json:"carried,omitempty"`
	Revision  string         `json:"revision,omitempty"`
	Unchanged bool           `json:"unchanged"`
	Total     int            `json:"total"`
}

type pricingLeaderboardRow struct {
	Rank                int     `json:"rank"`
	ModelName           string  `json:"model_name"`
	Vendor              string  `json:"vendor,omitempty"`
	Unit                string  `json:"unit"`
	BillingMode         string  `json:"billing_mode,omitempty"`
	SiteInputPrice      float64 `json:"site_input_price"`
	SiteOutputPrice     float64 `json:"site_output_price"`
	SiteCachePrice      float64 `json:"site_cache_price,omitempty"`
	SiteRequestPrice    float64 `json:"site_request_price,omitempty"`
	OfficialInputPrice  float64 `json:"official_input_price,omitempty"`
	OfficialOutputPrice float64 `json:"official_output_price,omitempty"`
	OfficialSource      string  `json:"official_source,omitempty"`
	DiscountPct         float64 `json:"discount_pct,omitempty"`
	HasOfficial         bool    `json:"has_official"`
}

func roundPricingFloat(value float64) float64 {
	return math.Round(value*1e4) / 1e4
}

// quotaToUSD 把 quota 换算成美元。
func quotaToUSD(quota float64) float64 {
	if common.QuotaPerUnit <= 0 {
		return 0
	}
	return quota / common.QuotaPerUnit
}

// ratioToInputPrice 把本站倍率换算成「美元 / 百万 tokens」。
// 计费口径（service/task_billing.go）：quota = tokens * model_ratio，
// 因此 1e6 tokens 的 quota = 1e6 * ratio。
func ratioToInputPrice(ratio float64) float64 {
	return quotaToUSD(1e6 * ratio)
}

// exprToPrices 用计费引擎求值，返回输入/输出单价（美元 / 百万 tokens）。
// 表达式系数口径（pkg/billingexpr/settle.go quotaConversion）：v1 的系数就是
// 「美元 / 百万 tokens」，quota = cost / 1e6 * QuotaPerUnit，所以 USD = cost / 1e6。
// Len=1 表示按短上下文档取价——排行榜展示起价，不模拟长上下文加价。
func exprToPrices(expr string) (inputPrice float64, outputPrice float64, err error) {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return 0, 0, fmt.Errorf("empty billing expression")
	}
	inputCost, _, err := billingexpr.RunExpr(trimmed, billingexpr.TokenParams{P: 1e6, Len: 1})
	if err != nil {
		return 0, 0, err
	}
	outputCost, _, err := billingexpr.RunExpr(trimmed, billingexpr.TokenParams{C: 1e6, Len: 1})
	if err != nil {
		return 0, 0, err
	}
	return roundPricingFloat(inputCost / 1e6), roundPricingFloat(outputCost / 1e6), nil
}

func newOfficialPricingClient() *http.Client {
	transport := &http.Transport{
		// 跟随系统代理：境内直连 models.dev 基本握手超时，配了代理就该用上。
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          16,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: 45 * time.Second,
	}
	if common.TLSInsecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig.Clone()
	}
	return &http.Client{Transport: transport, Timeout: officialPricingFetchTimeout}
}

// officialHTTPResponse 是对上游的一次抓取结果。
// NotModified=true 表示源回了 304，调用方应沿用上一版条目。
type officialHTTPResponse struct {
	Body         []byte
	ETag         string
	LastModified string
	NotModified  bool
}

// fetchOfficialJSON 带 3 次重试：models.dev 在境内网络经常握手超时，
// 一次失败就整份快照缺失太脆。
// prev 里的 ETag/Last-Modified 会作为条件请求头发出去，源没变就省掉 700KB 传输。
func fetchOfficialJSON(ctx context.Context, client *http.Client, rawURL string, prev officialSourceState) (officialHTTPResponse, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return officialHTTPResponse{}, ctx.Err()
			case <-time.After(time.Duration(300*attempt) * time.Millisecond):
			}
		}
		resp, err := fetchOfficialJSONOnce(ctx, client, rawURL, prev)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return officialHTTPResponse{}, lastErr
}

func fetchOfficialJSONOnce(ctx context.Context, client *http.Client, rawURL string, prev officialSourceState) (officialHTTPResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return officialHTTPResponse{}, err
	}
	req.Header.Set("User-Agent", "new-api-official-pricing/1.0")
	req.Header.Set("Accept", "application/json")
	// 条件请求：源没改就只回 304，省带宽也省解析。
	if prev.ETag != "" {
		req.Header.Set("If-None-Match", prev.ETag)
	}
	if prev.LastMod != "" {
		req.Header.Set("If-Modified-Since", prev.LastMod)
	}
	resp, err := client.Do(req)
	if err != nil {
		return officialHTTPResponse{}, err
	}
	defer resp.Body.Close()

	result := officialHTTPResponse{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}
	if resp.StatusCode == http.StatusNotModified {
		result.NotModified = true
		// 304 不带新 ETag，保留旧值，下次还能继续条件请求。
		if result.ETag == "" {
			result.ETag = prev.ETag
		}
		if result.LastModified == "" {
			result.LastModified = prev.LastMod
		}
		return result, nil
	}
	if resp.StatusCode != http.StatusOK {
		return officialHTTPResponse{}, fmt.Errorf("%s: %s", rawURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, officialPricingMaxBytes))
	if err != nil {
		return officialHTTPResponse{}, err
	}
	if len(body) == 0 {
		return officialHTTPResponse{}, fmt.Errorf("%s: empty response", rawURL)
	}
	result.Body = body
	return result, nil
}

// basellm 的 ratio_config-v1-base.json：{success,data:{billing_mode,billing_expr}}
// 官方现在只对外给表达式定价，倍率部分由 models.dev 兜底。
func parseBasellmOfficialPricing(body []byte) (map[string]officialModelPrice, error) {
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			BillingMode map[string]string `json:"billing_mode"`
			BillingExpr map[string]string `json:"billing_expr"`
		} `json:"data"`
	}
	if err := common.DecodeJson(bytes.NewReader(body), &payload); err != nil {
		return nil, err
	}
	if len(payload.Data.BillingExpr) == 0 {
		return nil, fmt.Errorf("basellm payload carries no billing_expr entries")
	}
	prices := make(map[string]officialModelPrice, len(payload.Data.BillingExpr))
	for name, expr := range payload.Data.BillingExpr {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		inputPrice, outputPrice, err := exprToPrices(expr)
		if err != nil {
			logger.LogWarn(context.Background(), common.LogText("official pricing: skip %s: %s", name, err.Error()))
			continue
		}
		prices[name] = officialModelPrice{
			ModelName:   name,
			Source:      officialPricingSourceBasellm,
			BillingMode: billing_setting.BillingModeTieredExpr,
			BillingExpr: strings.TrimSpace(expr),
			InputPrice:  inputPrice,
			OutputPrice: outputPrice,
		}
	}
	return prices, nil
}

// models.dev 的 api.json：{provider:{models:{name:{cost:{input,output,cache_read}}}}}
// 复用 ratio_sync.go 的换算（ratio = input_cost_per_1M / 2），再换成美元单价。
func parseModelsDevOfficialPricing(body []byte) (map[string]officialModelPrice, error) {
	converted, err := convertModelsDevToRatioData(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	modelRatios := valueMap(converted["model_ratio"])
	completionRatios := valueMap(converted["completion_ratio"])
	cacheRatios := valueMap(converted["cache_ratio"])
	prices := make(map[string]officialModelPrice, len(modelRatios))
	for name, rawRatio := range modelRatios {
		ratio, ok := asFloat64(rawRatio)
		if !ok || ratio <= 0 {
			continue
		}
		inputPrice := roundPricingFloat(ratioToInputPrice(ratio))
		outputPrice := 0.0
		if completion, ok := asFloat64(completionRatios[name]); ok && completion > 0 {
			outputPrice = roundPricingFloat(inputPrice * completion)
		}
		cachePrice := 0.0
		if cache, ok := asFloat64(cacheRatios[name]); ok && cache > 0 {
			cachePrice = roundPricingFloat(inputPrice * cache)
		}
		prices[name] = officialModelPrice{
			ModelName:   name,
			Source:      officialPricingSourceModelsDev,
			BillingMode: billing_setting.BillingModeRatio,
			InputPrice:  inputPrice,
			OutputPrice: outputPrice,
			CachePrice:  cachePrice,
		}
	}
	return prices, nil
}

// mergeOfficialPrices 以 basellm（官方表达式）优先，models.dev 补齐缺口。
func mergeOfficialPrices(primary, fallback map[string]officialModelPrice) map[string]officialModelPrice {
	merged := make(map[string]officialModelPrice, len(primary)+len(fallback))
	for name, price := range fallback {
		merged[name] = price
	}
	for name, price := range primary {
		merged[name] = price
	}
	return merged
}

// parseLiteLLMOfficialPricing 解析 LiteLLM 的 model_prices_and_context_window.json。
// 结构：{ "<provider>/<model>": {input_cost_per_token, output_cost_per_token,
// cache_read_input_token_cost, litellm_provider} }，单价是「美元 / token」。
// 只收有正输入价的条目：免费/占位条目（sample_spec、input=0）对定价没有意义。
func parseLiteLLMOfficialPricing(body []byte) (map[string]officialModelPrice, error) {
	var payload map[string]struct {
		InputCostPerToken  *float64 `json:"input_cost_per_token"`
		OutputCostPerToken *float64 `json:"output_cost_per_token"`
		CacheReadInputCost *float64 `json:"cache_read_input_token_cost"`
		LiteLLMProvider    string   `json:"litellm_provider"`
		Mode               string   `json:"mode"`
	}
	if err := common.DecodeJson(bytes.NewReader(body), &payload); err != nil {
		return nil, err
	}
	prices := make(map[string]officialModelPrice, len(payload))
	for name, entry := range payload {
		if entry.InputCostPerToken == nil || !(*entry.InputCostPerToken > 0) {
			continue
		}
		inputPrice := roundPricingFloat(*entry.InputCostPerToken * 1e6)
		if inputPrice <= 0 {
			continue
		}
		outputPrice := 0.0
		if entry.OutputCostPerToken != nil && *entry.OutputCostPerToken > 0 {
			outputPrice = roundPricingFloat(*entry.OutputCostPerToken * 1e6)
		}
		cachePrice := 0.0
		if entry.CacheReadInputCost != nil && *entry.CacheReadInputCost > 0 {
			cachePrice = roundPricingFloat(*entry.CacheReadInputCost * 1e6)
		}
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		prices[trimmed] = officialModelPrice{
			ModelName:   trimmed,
			Source:      officialPricingSourceLiteLLM,
			BillingMode: billing_setting.BillingModeRatio,
			InputPrice:  inputPrice,
			OutputPrice: outputPrice,
			CachePrice:  cachePrice,
		}
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("litellm payload carries no usable pricing entries")
	}
	return prices, nil
}

// officialPricingSnapshotCache 按 option 原始字符串缓存解码结果。
// 快照有 3800+ 条、几百 KB，而价格榜是公开接口——每次请求都重新反序列化太浪费。
// 字符串比较命中时是 O(1)（同一底层数组），所以这里比「每次 decode」便宜得多。
type officialPricingSnapshotCacheEntry struct {
	raw      string
	snapshot officialPricingSnapshot
	ok       bool
}

var officialPricingSnapshotCache atomic.Pointer[officialPricingSnapshotCacheEntry]

// officialPriceIndexCache 缓存规范化匹配索引（3803 条的排序结果）。
type officialPriceIndexCacheEntry struct {
	revision string
	index    officialPriceIndex
}

var officialPriceIndexCache atomic.Pointer[officialPriceIndexCacheEntry]

func loadOfficialPricingSnapshot() (officialPricingSnapshot, bool) {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[officialPricingSnapshotOptionKey]
	common.OptionMapRWMutex.RUnlock()

	if cached := officialPricingSnapshotCache.Load(); cached != nil && cached.raw == raw {
		return cached.snapshot, cached.ok
	}

	cache := func(snapshot officialPricingSnapshot, ok bool) (officialPricingSnapshot, bool) {
		officialPricingSnapshotCache.Store(&officialPricingSnapshotCacheEntry{
			raw: raw, snapshot: snapshot, ok: ok,
		})
		return snapshot, ok
	}

	if strings.TrimSpace(raw) == "" {
		return cache(officialPricingSnapshot{}, false)
	}
	var snapshot officialPricingSnapshot
	if err := common.Unmarshal([]byte(raw), &snapshot); err != nil {
		logger.LogWarn(context.Background(), common.LogText("official pricing: snapshot decode failed: %s", err.Error()))
		return cache(officialPricingSnapshot{}, false)
	}
	if len(snapshot.Models) == 0 {
		return cache(officialPricingSnapshot{}, false)
	}
	return cache(snapshot, true)
}

// officialPriceIndexFor 拿到（并缓存）这份快照的匹配索引。
func officialPriceIndexFor(snapshot officialPricingSnapshot) officialPriceIndex {
	revision := snapshot.Revision
	if revision != "" {
		if cached := officialPriceIndexCache.Load(); cached != nil && cached.revision == revision {
			return cached.index
		}
	}
	index := buildOfficialPriceIndex(snapshot.Models)
	if revision != "" {
		officialPriceIndexCache.Store(&officialPriceIndexCacheEntry{revision: revision, index: index})
	}
	return index
}

// pricesBySource 从合并结果里挑出属于某个源的条目。
// 该源本次抓取失败时用它们兜底，避免「偶发成功一次 3764 条，下次超时就掉回 403 条」。
func pricesBySource(models map[string]officialModelPrice, source string) map[string]officialModelPrice {
	picked := make(map[string]officialModelPrice)
	for name, price := range models {
		if price.Source == source {
			picked[name] = price
		}
	}
	return picked
}

// snapshotRevision 给合并结果算内容指纹：条目没变就不必重写整个快照。
// 只对「源 + 名字 + 两个价格」做哈希，价格精度到小数点后 4 位。
func snapshotRevision(models map[string]officialModelPrice) string {
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	var builder strings.Builder
	builder.Grow(len(names) * 48)
	for _, name := range names {
		price := models[name]
		fmt.Fprintf(&builder, "%s|%s|%.4f|%.4f|%.4f\n",
			name, price.Source, price.InputPrice, price.OutputPrice, price.CachePrice)
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:8])
}

// ----------------------------------------------------------------------------
// 抓取与落库
// ----------------------------------------------------------------------------

var (
	// officialPricingRefreshLock 保证同一节点上只有一个刷新在跑（TryLock 非阻塞）。
	officialPricingRefreshLock sync.Mutex
	// officialPricingAsyncTrigger 记录上次「按需刷新」的触发时间，用来节流。
	officialPricingAsyncTrigger atomic.Int64
	// officialPricingAutoSyncOnce 保证后台任务只起一次。
	officialPricingAutoSyncOnce sync.Once
)

const (
	// 刷新整体预算 90s，锁给到 3 分钟留足重试与解析时间。
	officialPricingLockTTL = 3 * time.Minute
	// 启动后延迟这么久再跑第一次自动同步：容器刚起时 DNS/网络常常还没就绪。
	officialPricingStartupDelay = 90 * time.Second
	// 同一节点两次「按需刷新」之间至少隔这么久，避免排行榜被刷时反复打上游。
	officialPricingAsyncCooldown = 10 * time.Minute
	// 内容未变且刚写过库时，这个窗口内不重复落库。
	officialPricingRewriteWindow = 10 * time.Minute
)

// errOfficialPricingRefreshInFlight 表示已有一次刷新在跑，调用方应稍后重试。
var errOfficialPricingRefreshInFlight = errors.New("official pricing refresh already in flight")

const officialPricingLockReleaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`

// acquireOfficialPricingRefreshLock 抢刷新权。
// Redis 可用时跨节点互斥（多副本不至于一起去打上游）；否则只在本节点内互斥。
func acquireOfficialPricingRefreshLock(ctx context.Context) (release func(), ok bool) {
	if !officialPricingRefreshLock.TryLock() {
		return nil, false
	}
	if !common.RedisEnabled {
		return func() { officialPricingRefreshLock.Unlock() }, true
	}
	key := "official_pricing:refresh"
	owner := common.GetRandomString(16)
	acquired, err := common.RDB.SetNX(ctx, key, owner, officialPricingLockTTL).Result()
	if err != nil || !acquired {
		officialPricingRefreshLock.Unlock()
		return nil, false
	}
	return func() {
		common.RDB.Eval(context.Background(), officialPricingLockReleaseScript, []string{key}, owner)
		officialPricingRefreshLock.Unlock()
	}, true
}

// officialSourceFetch 是单个源的抓取结果（已解析）。
type officialSourceFetch struct {
	Source      string
	Prices      map[string]officialModelPrice
	ETag        string
	LastMod     string
	NotModified bool
	Err         error
}

func fetchOfficialSource(ctx context.Context, client *http.Client, source, url string,
	prev officialSourceState, parse func([]byte) (map[string]officialModelPrice, error)) officialSourceFetch {
	resp, err := fetchOfficialJSON(ctx, client, url, prev)
	if err != nil {
		return officialSourceFetch{Source: source, Err: err}
	}
	out := officialSourceFetch{
		Source:      source,
		ETag:        resp.ETag,
		LastMod:     resp.LastModified,
		NotModified: resp.NotModified,
	}
	if resp.NotModified {
		return out
	}
	prices, err := parse(resp.Body)
	if err != nil {
		out.Err = err
		return out
	}
	out.Prices = prices
	return out
}

// refreshOfficialPricing 拉取两个官方源、合并、落库。
// 关键容错：某个源这次失败时，按 Source 把上一版快照里该源的条目挑回来沿用，
// 而不是让快照缩水成只有另一个源。
// forcePersist=true 时即使内容没变也写库（管理端手动刷新用），
// 这样管理员点完按钮立刻能看到「刚刚同步」，不会被节流窗口吞掉。
// 后台定时与按需刷新传 false：内容没变就只回报 unchanged，不写库。
func refreshOfficialPricing(ctx context.Context, forcePersist bool) (*officialPricingRefreshResult, error) {
	release, ok := acquireOfficialPricingRefreshLock(ctx)
	if !ok {
		return nil, errOfficialPricingRefreshInFlight
	}
	defer release()

	previous, hasPrevious := loadOfficialPricingSnapshot()
	prevState := func(source string) officialSourceState {
		if !hasPrevious || previous.SourceState == nil {
			return officialSourceState{}
		}
		return previous.SourceState[source]
	}

	client := newOfficialPricingClient()
	fetches := make([]officialSourceFetch, 3)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		fetches[0] = fetchOfficialSource(ctx, client, officialPricingSourceBasellm,
			officialPricingBasellmURL,
			prevState(officialPricingSourceBasellm), parseBasellmOfficialPricing)
	}()
	go func() {
		defer wg.Done()
		fetches[1] = fetchOfficialSource(ctx, client, officialPricingSourceModelsDev,
			officialPricingModelsDevURL,
			prevState(officialPricingSourceModelsDev), parseModelsDevOfficialPricing)
	}()
	go func() {
		defer wg.Done()
		fetches[2] = fetchOfficialSource(ctx, client, officialPricingSourceLiteLLM,
			officialPricingLiteLLMURL,
			prevState(officialPricingSourceLiteLLM), parseLiteLLMOfficialPricing)
	}()
	wg.Wait()

	snapshot, failures, unchanged, err := buildOfficialPricingSnapshot(previous, hasPrevious, fetches, time.Now().Unix())
	if err != nil {
		return nil, err
	}

	// 内容没变且刚写过：不重复写库，减少 option 表的无效写入。
	if !forcePersist && unchanged && previous.FetchedAt > 0 &&
		time.Now().Unix()-previous.FetchedAt < int64(officialPricingRewriteWindow.Seconds()) {
		return &officialPricingRefreshResult{
			FetchedAt: snapshot.FetchedAt,
			UpdatedAt: snapshot.UpdatedAt,
			Sources:   snapshot.Sources,
			Counts:    snapshot.Counts,
			Carried:   snapshot.Carried,
			Revision:  snapshot.Revision,
			Unchanged: true,
			Total:     len(snapshot.Models),
		}, nil
	}
	// 部分源失败但沿用了旧数据：记一条 warning，方便运维看出「为什么条数没涨」。
	if len(failures) > 0 {
		logger.LogWarn(ctx, common.LogText(
			"official pricing: partial fetch failure (previous entries carried): %s",
			strings.Join(failures, "; ")))
	}

	encoded, err := common.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("encode snapshot: %w", err)
	}
	if err := model.UpdateOption(officialPricingSnapshotOptionKey, string(encoded)); err != nil {
		return nil, fmt.Errorf("persist snapshot: %w", err)
	}

	return &officialPricingRefreshResult{
		FetchedAt: snapshot.FetchedAt,
		UpdatedAt: snapshot.UpdatedAt,
		Sources:   snapshot.Sources,
		Counts:    snapshot.Counts,
		Carried:   snapshot.Carried,
		Revision:  snapshot.Revision,
		Unchanged: unchanged,
		Total:     len(snapshot.Models),
	}, nil
}

// buildOfficialPricingSnapshot 把抓取结果和上一版快照合成新快照。
// 纯函数，不碰网络和数据库——「某源失败时沿用上一版条目」的分支靠它保证。
//
// 返回的 failures 是失败源的可读描述；两个源都失败且无旧数据可用时返回 error。
func buildOfficialPricingSnapshot(previous officialPricingSnapshot, hasPrevious bool,
	fetches []officialSourceFetch, now int64) (officialPricingSnapshot, []string, bool, error) {
	snapshot := officialPricingSnapshot{
		FetchedAt:   now,
		UpdatedAt:   now,
		Sources:     make([]string, 0, len(fetches)),
		Counts:      map[string]int{},
		SourceState: map[string]officialSourceState{},
	}
	resolved := make(map[string]map[string]officialModelPrice, len(fetches))
	failures := make([]string, 0, len(fetches))

	for _, fetch := range fetches {
		source := fetch.Source
		state := officialSourceState{}
		if hasPrevious && previous.SourceState != nil {
			state = previous.SourceState[source]
		}
		var prices map[string]officialModelPrice

		switch {
		case fetch.Err != nil:
			failures = append(failures, fmt.Sprintf("%s: %v", source, fetch.Err))
			if hasPrevious {
				prices = pricesBySource(previous.Models, source)
			}
			if len(prices) > 0 {
				snapshot.Carried = append(snapshot.Carried, source)
			}
			state.Count = len(prices)
			state.LastError = fetch.Err.Error()
		case fetch.NotModified:
			// 源没变：沿用上一版条目，但这次检查是成功的，刷新 FetchedAt。
			if hasPrevious {
				prices = pricesBySource(previous.Models, source)
			}
			if fetch.ETag != "" {
				state.ETag = fetch.ETag
			}
			if fetch.LastMod != "" {
				state.LastMod = fetch.LastMod
			}
			state.Count = len(prices)
			state.FetchedAt = now
			state.LastError = ""
		default:
			prices = fetch.Prices
			state = officialSourceState{
				Count:     len(prices),
				FetchedAt: now,
				ETag:      fetch.ETag,
				LastMod:   fetch.LastMod,
			}
		}
		snapshot.SourceState[source] = state
		if len(prices) == 0 {
			continue
		}
		resolved[source] = prices
		snapshot.Sources = append(snapshot.Sources, source)
		snapshot.Counts[source] = len(prices)
	}

	if len(failures) == len(fetches) {
		return officialPricingSnapshot{}, failures, false, fmt.Errorf(
			"all official pricing sources failed (previous snapshot kept): %s",
			strings.Join(failures, "; "))
	}

	merged := mergeOfficialPrices(
		resolved[officialPricingSourceBasellm],
		mergeOfficialPrices(
			resolved[officialPricingSourceModelsDev],
			resolved[officialPricingSourceLiteLLM],
		),
	)
	if len(merged) == 0 {
		return officialPricingSnapshot{}, failures, false, errors.New(
			"official pricing sources returned no usable entries (previous snapshot kept)")
	}
	snapshot.Models = merged
	snapshot.Revision = snapshotRevision(merged)

	unchanged := hasPrevious && previous.Revision != "" && previous.Revision == snapshot.Revision
	if unchanged {
		// 内容没变：UpdatedAt 保持在「内容真正变化」的那一刻。
		snapshot.UpdatedAt = previous.UpdatedAt
	}
	return snapshot, failures, unchanged, nil
}

// RefreshOfficialPricing 管理端手动刷新入口。
// 只写「官方参考价」快照，不改本站任何倍率——改价仍走 ratio_sync 的确认流程。
func RefreshOfficialPricing(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), officialPricingFetchTimeout)
	defer cancel()

	result, err := refreshOfficialPricing(ctx, true)
	if err != nil {
		if errors.Is(err, errOfficialPricingRefreshInFlight) {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": common.LogText("Official pricing refresh is already running, retry in a moment"),
			})
			return
		}
		logger.LogError(c.Request.Context(), common.LogText("official pricing refresh failed: %s", err.Error()))
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": common.LogText("Failed to fetch official pricing: %s", err.Error()),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// ----------------------------------------------------------------------------
// 自动同步：定时器 + 按需兜底
// ----------------------------------------------------------------------------

// officialPricingSnapshotIsStale 判断快照是否已经「该换了」。
func officialPricingSnapshotIsStale(snapshot officialPricingSnapshot, hasSnapshot bool) bool {
	if !hasSnapshot {
		return true
	}
	threshold := common.OfficialPricingStaleSeconds
	if threshold <= 0 {
		threshold = 3600
	}
	return time.Now().Unix()-snapshot.FetchedAt > int64(threshold)
}

// maybeRefreshOfficialPricingAsync 在快照过期时后台补一次，绝不阻塞当前请求。
// 冷却窗口内最多触发一次，避免排行榜被频繁刷新时反复打上游。
func maybeRefreshOfficialPricingAsync() {
	if common.OfficialPricingSyncInterval <= 0 {
		return
	}
	snapshot, hasSnapshot := loadOfficialPricingSnapshot()
	if !officialPricingSnapshotIsStale(snapshot, hasSnapshot) {
		return
	}
	now := time.Now().Unix()
	for {
		last := officialPricingAsyncTrigger.Load()
		if last != 0 && now-last < int64(officialPricingAsyncCooldown.Seconds()) {
			return
		}
		if officialPricingAsyncTrigger.CompareAndSwap(last, now) {
			break
		}
	}
	gopool.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), officialPricingFetchTimeout)
		defer cancel()
		result, err := refreshOfficialPricing(ctx, false)
		if err != nil {
			logger.LogWarn(context.Background(), common.LogText(
				"official pricing background refresh failed: %s", err.Error()))
			return
		}
		logger.LogInfo(context.Background(), common.LogText(
			"official pricing background refresh done: total=%d sources=%v carried=%v unchanged=%v",
			result.Total, result.Sources, result.Carried, result.Unchanged))
	})
}

// StartOfficialPricingAutoSync 起后台定时同步。
// 间隔由 OFFICIAL_PRICING_SYNC_INTERVAL 控制（秒，0 关闭）；非 master 节点不跑。
func StartOfficialPricingAutoSync() {
	interval := common.OfficialPricingSyncInterval
	if interval <= 0 {
		logger.LogInfo(context.Background(), common.LogText(
			"official pricing auto sync disabled (OFFICIAL_PRICING_SYNC_INTERVAL=0)"))
		return
	}
	officialPricingAutoSyncOnce.Do(func() {
		if !common.IsMasterNode {
			logger.LogInfo(context.Background(), common.LogText(
				"official pricing auto sync skipped: not master node"))
			return
		}
		gopool.Go(func() {
			ticker := time.NewTicker(time.Duration(interval) * time.Second)
			defer ticker.Stop()
			startup := time.NewTimer(officialPricingStartupDelay)
			defer startup.Stop()
			logger.LogInfo(context.Background(), common.LogText(
				"official pricing auto sync started: interval=%ds startup_delay=%s",
				interval, officialPricingStartupDelay))

			for {
				select {
				case <-startup.C:
					runOfficialPricingAutoSync()
				case <-ticker.C:
					runOfficialPricingAutoSync()
				}
			}
		})
	})
}

func runOfficialPricingAutoSync() {
	// 快照还新鲜就不必拉：升级上来时旧快照可能才刚刷过。
	if snapshot, ok := loadOfficialPricingSnapshot(); ok && !officialPricingSnapshotIsStale(snapshot, true) {
		logger.LogInfo(context.Background(), common.LogText(
			"official pricing auto sync: snapshot still fresh (age=%ds), skip",
			time.Now().Unix()-snapshot.FetchedAt))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), officialPricingFetchTimeout)
	defer cancel()
	result, err := refreshOfficialPricing(ctx, false)
	if err != nil {
		logger.LogWarn(context.Background(), common.LogText(
			"official pricing auto sync failed: %s", err.Error()))
		return
	}
	logger.LogInfo(context.Background(), common.LogText(
		"official pricing auto sync done: total=%d sources=%v carried=%v unchanged=%v updated_at=%d",
		result.Total, result.Sources, result.Carried, result.Unchanged, result.UpdatedAt))
}

func sitePriceOf(pricing model.Pricing) (inputPrice, outputPrice, cachePrice, requestPrice float64, ok bool) {
	if pricing.BillingMode == billing_setting.BillingModeTieredExpr && strings.TrimSpace(pricing.BillingExpr) != "" {
		inputPrice, outputPrice, err := exprToPrices(pricing.BillingExpr)
		if err != nil {
			return 0, math.NaN(), 0, 0, false
		}
		return inputPrice, outputPrice, 0, 0, true
	}
	if pricing.QuotaType == 1 {
		if pricing.ModelPrice <= 0 {
			return 0, math.NaN(), 0, 0, false
		}
		return 0, math.NaN(), 0, roundPricingFloat(pricing.ModelPrice), true
	}
	if pricing.ModelRatio <= 0 {
		return 0, math.NaN(), 0, 0, false
	}
	inputPrice = roundPricingFloat(ratioToInputPrice(pricing.ModelRatio))
	if pricing.CompletionRatio > 0 {
		outputPrice = roundPricingFloat(inputPrice * pricing.CompletionRatio)
	}
	if pricing.CacheRatio != nil && *pricing.CacheRatio > 0 {
		cachePrice = roundPricingFloat(inputPrice * *pricing.CacheRatio)
	}
	return inputPrice, outputPrice, cachePrice, 0, true
}

// canonicalModelName 去掉厂商前缀、统一大小写，用来跨命名空间对齐官方价。
// 本站常写 `openai/gpt-4o`，官方表里是 `gpt-4o`，精确匹配会漏掉一大片。
func canonicalModelName(name string) string {
	lowered := strings.ToLower(strings.TrimSpace(name))
	if idx := strings.LastIndex(lowered, "/"); idx >= 0 && idx+1 < len(lowered) {
		return lowered[idx+1:]
	}
	return lowered
}

// officialLooseTailTokens 是「同一个模型的可选尾巴」：量化标记、对话微调标记、
// 上下文长度标记等。去掉它们再比对，能把 `granite-8b-code-instruct` 和
// `granite-8b-code-instruct-128k` 这种同一模型的两种写法对上。
var officialLooseTailTokens = []string{
	"-instruct", "-it", "-chat", "-hf", "-base", "-fp8", "-fp16", "-bf16",
	":free", "-128k", "-32k", "-8k",
}

// officialLooseKey 在 canonicalModelName 之上再剥掉版本号/日期/可选尾巴，
// 最后只保留字母数字。用于「官方表里名字带版本、本站名字不带」这类情况。
func officialLooseKey(name string) string {
	trimmed := canonicalModelName(name)
	trimmed = strings.TrimSuffix(trimmed, ":free")
	// 反复剥尾巴：granite-8b-code-instruct-128k → granite-8b-code
	for changed := true; changed; {
		changed = false
		for _, token := range officialLooseTailTokens {
			if strings.HasSuffix(trimmed, token) && len(trimmed) > len(token)+2 {
				trimmed = strings.TrimSuffix(trimmed, token)
				changed = true
			}
		}
	}
	// 版本尾巴：-v1 / -v1.0 / -v0.3
	if idx := strings.LastIndex(trimmed, "-v"); idx > 0 && idx+2 < len(trimmed) {
		tail := trimmed[idx+2:]
		if tail != "" && strings.Trim(tail, "0123456789.") == "" {
			trimmed = trimmed[:idx]
		}
	}
	// 日期尾巴：-2025-04-14 / -20250414
	if idx := strings.LastIndex(trimmed, "-"); idx > 0 {
		tail := strings.Trim(trimmed[idx+1:], "0123456789-")
		if tail == "" && len(trimmed)-idx > 9 {
			trimmed = trimmed[:idx]
		}
	}
	var builder strings.Builder
	builder.Grow(len(trimmed))
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.':
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// officialPriceSourceRank 决定同名冲突时谁更可信：官方表达式表 > models.dev > LiteLLM。
func officialPriceSourceRank(source string) int {
	switch source {
	case officialPricingSourceBasellm:
		return 3
	case officialPricingSourceModelsDev:
		return 2
	case officialPricingSourceLiteLLM:
		return 1
	default:
		return 0
	}
}

// officialPriceIndex 三级查找：精确 → 去厂商前缀 → 去版本/量化尾巴。
// 规范化重名时按来源可信度取其一，其次字典序最小，保证结果稳定。
type officialPriceIndex struct {
	exact     map[string]officialModelPrice
	canonical map[string]officialModelPrice
	loose     map[string]officialModelPrice
}

func buildOfficialPriceIndex(models map[string]officialModelPrice) officialPriceIndex {
	index := officialPriceIndex{
		exact:     models,
		canonical: make(map[string]officialModelPrice, len(models)),
		loose:     make(map[string]officialModelPrice, len(models)),
	}
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		price := models[name]
		index.canonical = preferOfficialPrice(index.canonical, canonicalModelName(name), price)
		index.loose = preferOfficialPrice(index.loose, officialLooseKey(name), price)
	}
	return index
}

// preferOfficialPrice 往索引里放一条价：已存在时按「有价优先 → 来源可信度」替换，
// 避免某个源把模型标成 0 元（免费/占位）时盖掉另一源的真实报价。
func preferOfficialPrice(target map[string]officialModelPrice, key string, price officialModelPrice) map[string]officialModelPrice {
	if key == "" {
		return target
	}
	current, exists := target[key]
	if !exists {
		target[key] = price
		return target
	}
	currentPriced := current.InputPrice > 0
	nextPriced := price.InputPrice > 0
	if currentPriced != nextPriced {
		if nextPriced {
			target[key] = price
		}
		return target
	}
	if officialPriceSourceRank(price.Source) > officialPriceSourceRank(current.Source) {
		target[key] = price
	}
	return target
}

func (index officialPriceIndex) lookup(modelName string) (officialModelPrice, bool) {
	if price, ok := index.exact[modelName]; ok {
		return price, true
	}
	price, ok := index.canonical[canonicalModelName(modelName)]
	return price, ok
}

// lookupLoose 在 lookup 基础上再放宽一档：版本号、量化后缀、上下文长度后缀都忽略。
// 价格榜和「同步官方价」共用它，保证展示的官方价与落库倍率是同一口径。
func (index officialPriceIndex) lookupLoose(modelName string) (officialModelPrice, bool) {
	if price, ok := index.lookup(modelName); ok {
		return price, true
	}
	price, ok := index.loose[officialLooseKey(modelName)]
	return price, ok
}

func discountPercent(sitePrice, officialPrice float64) float64 {
	if officialPrice <= 0 || sitePrice <= 0 {
		return 0
	}
	return roundPricingFloat((sitePrice/officialPrice - 1) * 100)
}

// buildPricingLeaderboard 生成排行榜行：本站有价的模型按本站输入价升序，
// 按次计费的模型排在按 token 计费的模型之后。官方价存在时附带折扣。
func buildPricingLeaderboard(pricing []model.Pricing, vendors map[int]string, snapshot officialPricingSnapshot) []pricingLeaderboardRow {
	rows := make([]pricingLeaderboardRow, 0, len(pricing))
	officialIndex := officialPriceIndexFor(snapshot)
	for _, item := range pricing {
		inputPrice, outputPrice, cachePrice, requestPrice, ok := sitePriceOf(item)
		if !ok {
			continue
		}
		row := pricingLeaderboardRow{
			ModelName:        item.ModelName,
			Vendor:           vendors[item.VendorID],
			Unit:             pricingUnitToken,
			BillingMode:      item.BillingMode,
			SiteInputPrice:   inputPrice,
			SiteOutputPrice:  outputPrice,
			SiteCachePrice:   cachePrice,
			SiteRequestPrice: requestPrice,
		}
		if item.QuotaType == 1 {
			row.Unit = pricingUnitRequest
		}
		// 与「同步官方价」用同一套降级匹配，价格榜才能和落库倍率对得上。
		// 官方价 0 表示源把该模型标成免费/占位，按「无官方价」展示，别显示 0 折扣。
		if official, exists := officialIndex.lookupLoose(item.ModelName); exists && official.InputPrice > 0 {
			row.HasOfficial = true
			row.OfficialSource = official.Source
			row.OfficialInputPrice = official.InputPrice
			row.OfficialOutputPrice = official.OutputPrice
			if row.Unit == pricingUnitToken {
				row.DiscountPct = discountPercent(row.SiteInputPrice, official.InputPrice)
			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Unit != rows[j].Unit {
			return rows[i].Unit == pricingUnitToken
		}
		left, right := rows[i].SiteInputPrice, rows[j].SiteInputPrice
		if rows[i].Unit == pricingUnitRequest {
			left, right = rows[i].SiteRequestPrice, rows[j].SiteRequestPrice
		}
		if left != right {
			return left < right
		}
		return rows[i].ModelName < rows[j].ModelName
	})
	for idx := range rows {
		rows[idx].Rank = idx + 1
	}
	if len(rows) > pricingLeaderboardLimit {
		rows = rows[:pricingLeaderboardLimit]
	}
	return rows
}

// GetPricingLeaderboard 公开接口：给普通用户看的价格排行榜。
func GetPricingLeaderboard(c *gin.Context) {
	snapshot, hasSnapshot := loadOfficialPricingSnapshot()

	// 快照过期就顺手在后台补一次，但绝不阻塞这次响应：
	// 用户立刻拿到旧数据，下一次刷新（或前端轮询）就已经是新的。
	maybeRefreshOfficialPricingAsync()

	vendors := make(map[int]string)
	for _, vendor := range model.GetVendors() {
		vendors[vendor.ID] = vendor.Name
	}

	rows := buildPricingLeaderboard(model.GetPricing(), vendors, snapshot)

	data := gin.H{
		"rows":               rows,
		"total":              len(rows),
		"official_available": hasSnapshot,
		"limit":              pricingLeaderboardLimit,
		"auto_sync":          common.OfficialPricingSyncInterval > 0,
		"sync_interval":      common.OfficialPricingSyncInterval,
		"stale_after":        common.OfficialPricingStaleSeconds,
	}
	if hasSnapshot {
		data["fetched_at"] = snapshot.FetchedAt
		data["updated_at"] = snapshot.UpdatedAt
		data["sources"] = snapshot.Sources
		data["counts"] = snapshot.Counts
		data["official_total"] = len(snapshot.Models)
		data["revision"] = snapshot.Revision
		data["stale"] = officialPricingSnapshotIsStale(snapshot, true)
		data["age_seconds"] = time.Now().Unix() - snapshot.FetchedAt
		if len(snapshot.Carried) > 0 {
			data["carried"] = snapshot.Carried
		}
		if len(snapshot.SourceState) > 0 {
			data["source_state"] = snapshot.SourceState
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
