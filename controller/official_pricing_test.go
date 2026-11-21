package controller

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
)

func withQuotaPerUnit(t *testing.T, value float64) {
	t.Helper()
	original := common.QuotaPerUnit
	common.QuotaPerUnit = value
	t.Cleanup(func() { common.QuotaPerUnit = original })
}

func nearly(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 1e-4 {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

// 倍率口径必须和官方站点一致：ratio 1.25 就是 $2.5 / 1M tokens。
func TestRatioToInputPriceUsesBillingFormula(t *testing.T) {
	withQuotaPerUnit(t, 500*1000.0)

	nearly(t, ratioToInputPrice(1.25), 2.5, "ratio 1.25")
	nearly(t, ratioToInputPrice(30), 60, "ratio 30")
	nearly(t, ratioToInputPrice(0), 0, "ratio 0")
}

// 表达式系数在 v1 就是「美元 / 百万 tokens」，必须直接用真实计费引擎求值。
func TestExprToPricesMatchesOfficialCoefficients(t *testing.T) {
	withQuotaPerUnit(t, 500*1000.0)

	cases := []struct {
		name       string
		expr       string
		wantInput  float64
		wantOutput float64
	}{
		{"gpt-4 flat tier", `tier("standard", p * 30 + c * 60)`, 30, 60},
		{"gpt-4.1 with cache", `tier("standard", p * 2 + cr * 0.5 + c * 8)`, 2, 8},
		{"no tier", `p * 0.15 + c * 0.6`, 0.15, 0.6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, output, err := exprToPrices(tc.expr)
			if err != nil {
				t.Fatalf("exprToPrices(%q) error: %v", tc.expr, err)
			}
			nearly(t, input, tc.wantInput, "input price")
			nearly(t, output, tc.wantOutput, "output price")
		})
	}
}

func TestExprToPricesRejectsEmptyOrBrokenExpression(t *testing.T) {
	if _, _, err := exprToPrices("   "); err == nil {
		t.Fatal("empty expression must fail")
	}
	if _, _, err := exprToPrices(`tier("standard", p *`); err == nil {
		t.Fatal("broken expression must fail")
	}
}

func TestParseBasellmOfficialPricing(t *testing.T) {
	body := []byte(`{"success":true,"message":"","data":{
		"billing_mode":{"deepseek-v3":"tiered_expr","gpt-4":"tiered_expr"},
		"billing_expr":{
			"deepseek-v3":"tier(\"standard\", p * 0.15 + c * 0.6)",
			"gpt-4":"tier(\"standard\", p * 30 + c * 60)",
			"broken-model":"tier(\"standard\", p *"
		}
	}}`)

	prices, err := parseBasellmOfficialPricing(body)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(prices) != 2 {
		t.Fatalf("want 2 usable entries (broken skipped), got %d: %v", len(prices), prices)
	}
	deepseek, ok := prices["deepseek-v3"]
	if !ok {
		t.Fatal("deepseek-v3 missing")
	}
	if deepseek.Source != officialPricingSourceBasellm {
		t.Fatalf("source = %q", deepseek.Source)
	}
	nearly(t, deepseek.InputPrice, 0.15, "deepseek input")
	nearly(t, deepseek.OutputPrice, 0.6, "deepseek output")
	nearly(t, prices["gpt-4"].InputPrice, 30, "gpt-4 input")
}

func TestParseBasellmRejectsPayloadWithoutExpressions(t *testing.T) {
	if _, err := parseBasellmOfficialPricing([]byte(`{"success":true,"data":{"model_ratio":{}}}`)); err == nil {
		t.Fatal("payload without billing_expr must fail")
	}
}

func TestParseModelsDevOfficialPricing(t *testing.T) {
	body := []byte(`{
		"openai":{"models":{"gpt-4o":{"cost":{"input":2.5,"output":10,"cache_read":1.25}}}},
		"reseller":{"models":{"gpt-4o":{"cost":{"input":5,"output":20}}}}
	}`)

	prices, err := parseModelsDevOfficialPricing(body)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	row, ok := prices["gpt-4o"]
	if !ok {
		t.Fatalf("gpt-4o missing: %v", prices)
	}
	if row.Source != officialPricingSourceModelsDev {
		t.Fatalf("source = %q", row.Source)
	}
	// 重复模型取最便宜的非零输入价（openai $2.5/1M）。
	nearly(t, row.InputPrice, 2.5, "input")
	nearly(t, row.OutputPrice, 10, "output")
	nearly(t, row.CachePrice, 1.25, "cache")
}

func TestMergeOfficialPricesPrefersBasellm(t *testing.T) {
	primary := map[string]officialModelPrice{
		"gpt-4": {ModelName: "gpt-4", Source: officialPricingSourceBasellm, InputPrice: 30},
	}
	fallback := map[string]officialModelPrice{
		"gpt-4":  {ModelName: "gpt-4", Source: officialPricingSourceModelsDev, InputPrice: 28},
		"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceModelsDev, InputPrice: 2.5},
	}
	merged := mergeOfficialPrices(primary, fallback)
	if len(merged) != 2 {
		t.Fatalf("want 2 merged entries, got %d", len(merged))
	}
	if merged["gpt-4"].Source != officialPricingSourceBasellm {
		t.Fatalf("basellm must win, got %q", merged["gpt-4"].Source)
	}
	if merged["gpt-4o"].Source != officialPricingSourceModelsDev {
		t.Fatalf("models.dev must backfill, got %q", merged["gpt-4o"].Source)
	}
}

func TestBuildPricingLeaderboardOrderingAndDiscount(t *testing.T) {
	withQuotaPerUnit(t, 500*1000.0)

	cacheRatio := 0.5
	pricing := []model.Pricing{
		{ModelName: "expensive", VendorID: 1, ModelRatio: 1.25, CompletionRatio: 4, CacheRatio: &cacheRatio},
		{ModelName: "cheap", ModelRatio: 0.075, CompletionRatio: 4},
		{ModelName: "expression", BillingMode: billing_setting.BillingModeTieredExpr, BillingExpr: `tier("standard", p * 1 + c * 2)`},
		{ModelName: "per-request", QuotaType: 1, ModelPrice: 0.5},
		{ModelName: "unpriced"},
	}
	vendors := map[int]string{1: "OpenAI"}
	snapshot := officialPricingSnapshot{
		Models: map[string]officialModelPrice{
			"expensive": {ModelName: "expensive", Source: officialPricingSourceBasellm, InputPrice: 5},
			"cheap":     {ModelName: "cheap", Source: officialPricingSourceModelsDev, InputPrice: 0.1},
		},
	}

	rows := buildPricingLeaderboard(pricing, vendors, snapshot)
	if len(rows) != 4 {
		t.Fatalf("want 4 priced rows, got %d: %+v", len(rows), rows)
	}
	order := []string{"cheap", "expression", "expensive", "per-request"}
	for idx, want := range order {
		if rows[idx].ModelName != want {
			t.Fatalf("row %d = %q, want %q (rows=%+v)", idx, rows[idx].ModelName, want, rows)
		}
		if rows[idx].Rank != idx+1 {
			t.Fatalf("row %d rank = %d", idx, rows[idx].Rank)
		}
	}

	nearly(t, rows[0].SiteInputPrice, 0.15, "cheap input price")
	nearly(t, rows[0].SiteOutputPrice, 0.6, "cheap output price")
	nearly(t, rows[0].DiscountPct, 50, "cheap discount vs official 0.1")
	if rows[0].Vendor != "" {
		t.Fatalf("cheap has no vendor id, got %q", rows[0].Vendor)
	}

	nearly(t, rows[1].SiteInputPrice, 1, "expression input price")
	nearly(t, rows[1].SiteOutputPrice, 2, "expression output price")
	if rows[1].HasOfficial {
		t.Fatal("expression model has no official entry")
	}

	nearly(t, rows[2].SiteInputPrice, 2.5, "expensive input price")
	nearly(t, rows[2].SiteOutputPrice, 10, "expensive output price")
	nearly(t, rows[2].SiteCachePrice, 1.25, "expensive cache price")
	nearly(t, rows[2].DiscountPct, -50, "expensive discount vs official 5")
	if rows[2].Vendor != "OpenAI" {
		t.Fatalf("expensive vendor = %q", rows[2].Vendor)
	}

	if rows[3].Unit != pricingUnitRequest {
		t.Fatalf("per-request unit = %q", rows[3].Unit)
	}
	nearly(t, rows[3].SiteRequestPrice, 0.5, "per-request price")
}

func TestBuildPricingLeaderboardCapsRows(t *testing.T) {
	pricing := make([]model.Pricing, 0, pricingLeaderboardLimit+25)
	for idx := 0; idx < pricingLeaderboardLimit+25; idx++ {
		pricing = append(pricing, model.Pricing{ModelName: "m", ModelRatio: float64(idx+1) / 100})
	}
	rows := buildPricingLeaderboard(pricing, nil, officialPricingSnapshot{})
	if len(rows) != pricingLeaderboardLimit {
		t.Fatalf("want %d rows, got %d", pricingLeaderboardLimit, len(rows))
	}
	nearly(t, rows[0].SiteInputPrice, 0.02, "first row cheapest")
}

func TestOfficialPricingSnapshotRoundTrip(t *testing.T) {
	snapshot := officialPricingSnapshot{
		FetchedAt: 1_700_000_000,
		Sources:   []string{officialPricingSourceBasellm},
		Counts:    map[string]int{officialPricingSourceBasellm: 1},
		Models: map[string]officialModelPrice{
			"gpt-4": {ModelName: "gpt-4", Source: officialPricingSourceBasellm, InputPrice: 30, OutputPrice: 60},
		},
	}
	encoded, err := common.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	original, hadOriginal := common.OptionMap[officialPricingSnapshotOptionKey]
	common.OptionMap[officialPricingSnapshotOptionKey] = string(encoded)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadOriginal {
			common.OptionMap[officialPricingSnapshotOptionKey] = original
		} else {
			delete(common.OptionMap, officialPricingSnapshotOptionKey)
		}
		common.OptionMapRWMutex.Unlock()
	})

	loaded, ok := loadOfficialPricingSnapshot()
	if !ok {
		t.Fatal("snapshot should load")
	}
	if loaded.FetchedAt != snapshot.FetchedAt || len(loaded.Models) != 1 {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	nearly(t, loaded.Models["gpt-4"].InputPrice, 30, "loaded input price")
}

func TestLoadOfficialPricingSnapshotRejectsGarbage(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	original, hadOriginal := common.OptionMap[officialPricingSnapshotOptionKey]
	common.OptionMap[officialPricingSnapshotOptionKey] = "{not-json"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadOriginal {
			common.OptionMap[officialPricingSnapshotOptionKey] = original
		} else {
			delete(common.OptionMap, officialPricingSnapshotOptionKey)
		}
		common.OptionMapRWMutex.Unlock()
	})

	if _, ok := loadOfficialPricingSnapshot(); ok {
		t.Fatal("garbage snapshot must not load")
	}
}

// TestLiveOfficialPricingSources 走真实上游，只在 HC41_LIVE=1 时运行。
// 用来确认官方价目表的**真实结构**没变（字段名、单位、条目量级）。
func TestLiveOfficialPricingSources(t *testing.T) {
	if os.Getenv("HC41_LIVE") != "1" {
		t.Skip("set HC41_LIVE=1 to hit the live official pricing sources")
	}
	withQuotaPerUnit(t, 500*1000.0)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := newOfficialPricingClient()

	basellmFetch := fetchOfficialSource(ctx, client, officialPricingSourceBasellm,
		officialPricingBasellmURL, officialSourceState{}, parseBasellmOfficialPricing)
	if basellmFetch.Err != nil {
		t.Fatalf("basellm fetch/parse: %v", basellmFetch.Err)
	}
	official := basellmFetch.Prices
	if len(official) < 100 {
		t.Fatalf("basellm entries = %d, expected the official expression list", len(official))
	}
	gpt4, ok := official["gpt-4"]
	if !ok {
		t.Fatalf("gpt-4 missing from basellm list (%d entries)", len(official))
	}
	// 官方 gpt-4 是 $30/1M 输入、$60/1M 输出；容差放手到 20% 以适配官方调价。
	if gpt4.InputPrice < 24 || gpt4.InputPrice > 36 {
		t.Fatalf("gpt-4 input price = %v, expected roughly $30/1M", gpt4.InputPrice)
	}

	modelsDevFetch := fetchOfficialSource(ctx, client, officialPricingSourceModelsDev,
		officialPricingModelsDevURL, officialSourceState{}, parseModelsDevOfficialPricing)
	if modelsDevFetch.Err != nil {
		// models.dev 在境内直连经常超时；这条测试的价值在 basellm 那半边，
		// 拿不到 models.dev 就只校验合并不会丢条目，不判失败。
		t.Logf("models.dev unavailable (expected on some networks): %v", modelsDevFetch.Err)
	}
	fallback := modelsDevFetch.Prices
	if modelsDevFetch.Err == nil && len(fallback) < 50 {
		t.Fatalf("models.dev entries = %d", len(fallback))
	}

	merged := mergeOfficialPrices(official, fallback)
	if len(merged) < len(official) {
		t.Fatalf("merge lost entries: %d < %d", len(merged), len(official))
	}
	t.Logf("official=%d models.dev=%d merged=%d", len(official), len(fallback), len(merged))
}

func TestCanonicalModelNameStripsVendorPrefix(t *testing.T) {
	cases := map[string]string{
		"openai/gpt-4o":             "gpt-4o",
		"OpenAI/GPT-4o":             "gpt-4o",
		"gpt-4o":                    "gpt-4o",
		"nvidia/nemotron-parse-2.0": "nemotron-parse-2.0",
		"trailing/":                 "trailing/",
		"":                          "",
	}
	for input, want := range cases {
		if got := canonicalModelName(input); got != want {
			t.Fatalf("canonicalModelName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestOfficialPriceIndexPrefersExactThenCanonical(t *testing.T) {
	index := buildOfficialPriceIndex(map[string]officialModelPrice{
		"gpt-4o":             {ModelName: "gpt-4o", Source: officialPricingSourceModelsDev, InputPrice: 2.5},
		"openai/gpt-4o-mini": {ModelName: "openai/gpt-4o-mini", Source: officialPricingSourceBasellm, InputPrice: 0.15},
		"gpt-4o-mini":        {ModelName: "gpt-4o-mini", Source: officialPricingSourceModelsDev, InputPrice: 0.2},
		"azure/gpt-4o-mini":  {ModelName: "azure/gpt-4o-mini", Source: officialPricingSourceModelsDev, InputPrice: 0.3},
	})

	if price, ok := index.lookup("gpt-4o"); !ok || price.InputPrice != 2.5 {
		t.Fatalf("exact lookup failed: %+v %v", price, ok)
	}
	// 本站写 openai/gpt-4o，官方表只有 gpt-4o ——规范化后要能对上。
	if price, ok := index.lookup("openai/gpt-4o"); !ok || price.InputPrice != 2.5 {
		t.Fatalf("canonical lookup failed: %+v %v", price, ok)
	}
	// 规范化重名时官方表达式优先（0.15 < 0.2 且来源是 basellm）。
	if price, ok := index.lookup("somevendor/gpt-4o-mini"); !ok || price.InputPrice != 0.15 {
		t.Fatalf("basellm should win the canonical tie: %+v %v", price, ok)
	}
	if _, ok := index.lookup("not-a-model"); ok {
		t.Fatal("unknown model must not match")
	}
}

func TestBuildPricingLeaderboardMatchesPrefixedModelNames(t *testing.T) {
	withQuotaPerUnit(t, 500*1000.0)
	pricing := []model.Pricing{{ModelName: "openai/gpt-4o", ModelRatio: 1.25, CompletionRatio: 4}}
	snapshot := officialPricingSnapshot{Models: map[string]officialModelPrice{
		"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceBasellm, InputPrice: 2.5},
	}}
	rows := buildPricingLeaderboard(pricing, nil, snapshot)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if !rows[0].HasOfficial {
		t.Fatal("prefixed site name must still match the official entry")
	}
	nearly(t, rows[0].OfficialInputPrice, 2.5, "official input")
	nearly(t, rows[0].DiscountPct, 0, "official price equals site price")
}

// ----------------------------------------------------------------------------
// hc42：自动同步 / 源失败沿用
// ----------------------------------------------------------------------------

func TestSnapshotRevisionIsStableAndSensitive(t *testing.T) {
	base := map[string]officialModelPrice{
		"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceBasellm, InputPrice: 2.5, OutputPrice: 10},
		"o3":     {ModelName: "o3", Source: officialPricingSourceModelsDev, InputPrice: 1, OutputPrice: 4},
	}
	first := snapshotRevision(base)
	if first == "" {
		t.Fatal("revision must not be empty")
	}
	// map 迭代顺序随机，两次必须一样。
	for i := 0; i < 20; i++ {
		if got := snapshotRevision(base); got != first {
			t.Fatalf("revision unstable: %q vs %q", got, first)
		}
	}

	changed := map[string]officialModelPrice{
		"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceBasellm, InputPrice: 2.6, OutputPrice: 10},
		"o3":     {ModelName: "o3", Source: officialPricingSourceModelsDev, InputPrice: 1, OutputPrice: 4},
	}
	if snapshotRevision(changed) == first {
		t.Fatal("price change must change the revision")
	}
	// 精度到小数点后 4 位，第 5 位变化不影响指纹。
	tiny := map[string]officialModelPrice{
		"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceBasellm, InputPrice: 2.50001, OutputPrice: 10},
		"o3":     {ModelName: "o3", Source: officialPricingSourceModelsDev, InputPrice: 1, OutputPrice: 4},
	}
	if snapshotRevision(tiny) != first {
		t.Fatal("sub-4-decimal noise must not change the revision")
	}
}

func TestPricesBySourcePicksOnlyThatSource(t *testing.T) {
	models := map[string]officialModelPrice{
		"a": {ModelName: "a", Source: officialPricingSourceBasellm},
		"b": {ModelName: "b", Source: officialPricingSourceModelsDev},
		"c": {ModelName: "c", Source: officialPricingSourceModelsDev},
	}
	if got := pricesBySource(models, officialPricingSourceBasellm); len(got) != 1 {
		t.Fatalf("basellm picks = %d, want 1", len(got))
	}
	if got := pricesBySource(models, officialPricingSourceModelsDev); len(got) != 2 {
		t.Fatalf("models.dev picks = %d, want 2", len(got))
	}
	if got := pricesBySource(models, "unknown"); len(got) != 0 {
		t.Fatalf("unknown source picks = %d, want 0", len(got))
	}
}

func fetchOK(source string, prices map[string]officialModelPrice) officialSourceFetch {
	return officialSourceFetch{Source: source, Prices: prices, ETag: `"v1"`, LastMod: "Fri, 21 Nov 2026 00:00:00 GMT"}
}

func TestBuildOfficialPricingSnapshotMergesBothSources(t *testing.T) {
	basellm := map[string]officialModelPrice{
		"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceBasellm, InputPrice: 2.5, OutputPrice: 10},
	}
	modelsDev := map[string]officialModelPrice{
		"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceModelsDev, InputPrice: 9.9, OutputPrice: 9.9},
		"o3":     {ModelName: "o3", Source: officialPricingSourceModelsDev, InputPrice: 1, OutputPrice: 4},
	}
	snapshot, failures, unchanged, err := buildOfficialPricingSnapshot(
		officialPricingSnapshot{}, false,
		[]officialSourceFetch{
			fetchOK(officialPricingSourceBasellm, basellm),
			fetchOK(officialPricingSourceModelsDev, modelsDev),
		},
		1700000000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
	if unchanged {
		t.Fatal("first snapshot cannot be unchanged")
	}
	if len(snapshot.Models) != 2 {
		t.Fatalf("merged = %d models, want 2", len(snapshot.Models))
	}
	// basellm 优先：gpt-4o 必须是官方表达式那条 2.5，而不是 models.dev 的 9.9。
	if got := snapshot.Models["gpt-4o"]; got.InputPrice != 2.5 || got.Source != officialPricingSourceBasellm {
		t.Fatalf("basellm must win the merge: %+v", got)
	}
	if snapshot.Counts[officialPricingSourceBasellm] != 1 || snapshot.Counts[officialPricingSourceModelsDev] != 2 {
		t.Fatalf("counts = %+v", snapshot.Counts)
	}
	if snapshot.SourceState[officialPricingSourceBasellm].ETag != `"v1"` {
		t.Fatalf("etag not recorded: %+v", snapshot.SourceState)
	}
}

func TestBuildOfficialPricingSnapshotCarriesFailedSource(t *testing.T) {
	previous := officialPricingSnapshot{
		FetchedAt: 1699999000,
		UpdatedAt: 1699999000,
		Revision:  "old",
		Models: map[string]officialModelPrice{
			"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceBasellm, InputPrice: 2.5, OutputPrice: 10},
			"o3":     {ModelName: "o3", Source: officialPricingSourceModelsDev, InputPrice: 1, OutputPrice: 4},
		},
		SourceState: map[string]officialSourceState{
			officialPricingSourceBasellm: {Count: 1, FetchedAt: 1699999000, ETag: `"old-etag"`},
		},
	}
	// basellm 这次挂了，models.dev 正常。
	snapshot, failures, _, err := buildOfficialPricingSnapshot(
		previous, true,
		[]officialSourceFetch{
			{Source: officialPricingSourceBasellm, Err: context.DeadlineExceeded},
			fetchOK(officialPricingSourceModelsDev, map[string]officialModelPrice{
				"o3": {ModelName: "o3", Source: officialPricingSourceModelsDev, InputPrice: 1.2, OutputPrice: 4.8},
			}),
		},
		1700000000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("failures = %v, want 1", failures)
	}
	// 关键：basellm 的旧条目必须被沿用，不能让快照缩水成只剩 models.dev。
	if len(snapshot.Models) != 2 {
		t.Fatalf("carried snapshot has %d models, want 2", len(snapshot.Models))
	}
	if got, ok := snapshot.Models["gpt-4o"]; !ok || got.InputPrice != 2.5 {
		t.Fatalf("basellm entry not carried: %+v ok=%v", got, ok)
	}
	if len(snapshot.Carried) != 1 || snapshot.Carried[0] != officialPricingSourceBasellm {
		t.Fatalf("carried = %v, want [basellm]", snapshot.Carried)
	}
	state := snapshot.SourceState[officialPricingSourceBasellm]
	if state.LastError == "" {
		t.Fatal("failed source must record LastError")
	}
	if state.ETag != `"old-etag"` {
		t.Fatalf("failed source must keep the old etag, got %q", state.ETag)
	}
	// 价格变了：models.dev 的 o3 从 1 涨到 1.2，指纹必须变。
	if snapshot.Revision == previous.Revision {
		t.Fatal("content change must change the revision")
	}
}

func TestBuildOfficialPricingSnapshotNotModifiedKeepsEntries(t *testing.T) {
	previous := officialPricingSnapshot{
		FetchedAt: 1699999000,
		UpdatedAt: 1699999000,
		Models: map[string]officialModelPrice{
			"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceBasellm, InputPrice: 2.5},
		},
	}
	snapshot, failures, _, err := buildOfficialPricingSnapshot(
		previous, true,
		[]officialSourceFetch{
			{Source: officialPricingSourceBasellm, NotModified: true, ETag: `"v1"`},
		},
		1700000000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("304 must not count as a failure: %v", failures)
	}
	if len(snapshot.Models) != 1 || snapshot.Models["gpt-4o"].InputPrice != 2.5 {
		t.Fatalf("304 must keep previous entries: %+v", snapshot.Models)
	}
	state := snapshot.SourceState[officialPricingSourceBasellm]
	if state.FetchedAt != 1700000000 {
		t.Fatalf("304 should refresh FetchedAt, got %d", state.FetchedAt)
	}
	if state.LastError != "" {
		t.Fatalf("304 must clear LastError, got %q", state.LastError)
	}
	if len(snapshot.Carried) != 0 {
		t.Fatalf("304 is not 'carried': %v", snapshot.Carried)
	}
}

func TestBuildOfficialPricingSnapshotFailsWhenEverySourceFails(t *testing.T) {
	previous := officialPricingSnapshot{
		Models: map[string]officialModelPrice{
			"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceBasellm, InputPrice: 2.5},
		},
	}
	// 唯一的源失败且旧数据也没有可沿用的 -> 必须报错，让调用方保留原快照。
	_, failures, _, err := buildOfficialPricingSnapshot(
		officialPricingSnapshot{}, false,
		[]officialSourceFetch{
			{Source: officialPricingSourceBasellm, Err: context.DeadlineExceeded},
			{Source: officialPricingSourceModelsDev, Err: context.Canceled},
		},
		1700000000)
	if err == nil {
		t.Fatal("all-failed refresh must return an error")
	}
	if len(failures) != 2 {
		t.Fatalf("failures = %v, want 2", failures)
	}
	_ = previous
}

func TestBuildOfficialPricingSnapshotMarksUnchanged(t *testing.T) {
	same := map[string]officialModelPrice{
		"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceBasellm, InputPrice: 2.5, OutputPrice: 10},
	}
	previous := officialPricingSnapshot{
		FetchedAt: 1700000000,
		UpdatedAt: 1699990000,
		Revision:  snapshotRevision(same),
		Models:    same,
	}
	snapshot, _, unchanged, err := buildOfficialPricingSnapshot(
		previous, true,
		[]officialSourceFetch{fetchOK(officialPricingSourceBasellm, same)},
		1700003600)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !unchanged {
		t.Fatal("identical content must be reported as unchanged")
	}
	if snapshot.UpdatedAt != 1699990000 {
		t.Fatalf("UpdatedAt must stay at the last real change, got %d", snapshot.UpdatedAt)
	}
}

func TestFetchOfficialJSONSendsConditionalHeadersAndHandles304(t *testing.T) {
	prev := officialSourceState{ETag: `"v1"`, LastMod: "Fri, 21 Nov 2026 00:00:00 GMT"}

	t.Run("304 with fresh etag adopts it", func(t *testing.T) {
		var gotIfNoneMatch, gotIfModifiedSince string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotIfNoneMatch = r.Header.Get("If-None-Match")
			gotIfModifiedSince = r.Header.Get("If-Modified-Since")
			w.Header().Set("ETag", `"v2"`)
			w.WriteHeader(http.StatusNotModified)
		}))
		defer server.Close()

		resp, err := fetchOfficialJSON(context.Background(), server.Client(), server.URL, prev)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotIfNoneMatch != `"v1"` {
			t.Fatalf("If-None-Match = %q, want %q", gotIfNoneMatch, `"v1"`)
		}
		if gotIfModifiedSince != prev.LastMod {
			t.Fatalf("If-Modified-Since = %q, want %q", gotIfModifiedSince, prev.LastMod)
		}
		if !resp.NotModified {
			t.Fatal("304 must be reported as NotModified")
		}
		if len(resp.Body) != 0 {
			t.Fatalf("304 must carry no body, got %d bytes", len(resp.Body))
		}
		if resp.ETag != `"v2"` {
			t.Fatalf("etag on 304 = %q, want the fresh one", resp.ETag)
		}
	})

	t.Run("304 without etag keeps the previous one", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotModified)
		}))
		defer server.Close()

		resp, err := fetchOfficialJSON(context.Background(), server.Client(), server.URL, prev)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !resp.NotModified {
			t.Fatal("304 must be reported as NotModified")
		}
		// 不带新 ETag 时必须保留旧的，否则下一次就没法继续条件请求了。
		if resp.ETag != `"v1"` {
			t.Fatalf("etag on bare 304 = %q, want %q", resp.ETag, `"v1"`)
		}
		if resp.LastModified != prev.LastMod {
			t.Fatalf("Last-Modified on bare 304 = %q, want %q", resp.LastModified, prev.LastMod)
		}
	})
}

func TestFetchOfficialJSONRetriesThenSucceeds(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	resp, err := fetchOfficialJSON(context.Background(), server.Client(), server.URL, officialSourceState{})
	if err != nil {
		t.Fatalf("second attempt should succeed: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if string(resp.Body) != `{"ok":true}` {
		t.Fatalf("body = %q", resp.Body)
	}
}

func TestOfficialPricingSnapshotIsStale(t *testing.T) {
	original := common.OfficialPricingStaleSeconds
	common.OfficialPricingStaleSeconds = 3600
	t.Cleanup(func() { common.OfficialPricingStaleSeconds = original })

	if !officialPricingSnapshotIsStale(officialPricingSnapshot{}, false) {
		t.Fatal("missing snapshot must be stale")
	}
	fresh := officialPricingSnapshot{FetchedAt: time.Now().Unix() - 60}
	if officialPricingSnapshotIsStale(fresh, true) {
		t.Fatal("60s old snapshot must not be stale with a 1h threshold")
	}
	old := officialPricingSnapshot{FetchedAt: time.Now().Unix() - 7200}
	if !officialPricingSnapshotIsStale(old, true) {
		t.Fatal("2h old snapshot must be stale with a 1h threshold")
	}
	// 阈值配成 0 或负数时回落到 1 小时，不能让「0」被理解成「永远新鲜」。
	common.OfficialPricingStaleSeconds = 0
	if officialPricingSnapshotIsStale(old, true) != true {
		t.Fatal("zero threshold must fall back to 1h")
	}
}

func TestLoadOfficialPricingSnapshotCachesAndInvalidates(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	previous, hadPrevious := common.OptionMap[officialPricingSnapshotOptionKey]
	common.OptionMap[officialPricingSnapshotOptionKey] =
		`{"fetched_at":1,"revision":"r1","models":{"gpt-4o":{"model_name":"gpt-4o","source":"basellm","input_price":2.5}}}`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadPrevious {
			common.OptionMap[officialPricingSnapshotOptionKey] = previous
		} else {
			delete(common.OptionMap, officialPricingSnapshotOptionKey)
		}
		common.OptionMapRWMutex.Unlock()
		// 别把测试内容留在缓存里影响后面的用例。
		officialPricingSnapshotCache.Store(nil)
		officialPriceIndexCache.Store(nil)
	})

	first, ok := loadOfficialPricingSnapshot()
	if !ok || len(first.Models) != 1 {
		t.Fatalf("first load = %+v ok=%v", first, ok)
	}
	// 第二次应命中缓存，返回同样的内容。
	second, ok := loadOfficialPricingSnapshot()
	if !ok || second.Revision != "r1" || len(second.Models) != 1 {
		t.Fatalf("cached load = %+v ok=%v", second, ok)
	}

	// 换掉 option 内容后必须重新解码，不能继续吃旧缓存。
	common.OptionMapRWMutex.Lock()
	common.OptionMap[officialPricingSnapshotOptionKey] =
		`{"fetched_at":2,"revision":"r2","models":{"o3":{"model_name":"o3","source":"models.dev","input_price":1}}}`
	common.OptionMapRWMutex.Unlock()
	third, ok := loadOfficialPricingSnapshot()
	if !ok || third.Revision != "r2" || len(third.Models) != 1 {
		t.Fatalf("invalidated load = %+v ok=%v", third, ok)
	}
	if _, exists := third.Models["gpt-4o"]; exists {
		t.Fatal("stale cached snapshot leaked through")
	}

	// 清空 option 后要立刻变成「无快照」。
	common.OptionMapRWMutex.Lock()
	common.OptionMap[officialPricingSnapshotOptionKey] = ""
	common.OptionMapRWMutex.Unlock()
	if _, ok := loadOfficialPricingSnapshot(); ok {
		t.Fatal("empty option must decode to no snapshot")
	}
}

func TestOfficialPriceIndexForReusesIndexByRevision(t *testing.T) {
	officialPriceIndexCache.Store(nil)
	t.Cleanup(func() { officialPriceIndexCache.Store(nil) })

	snapshot := officialPricingSnapshot{
		Revision: "same-revision",
		Models: map[string]officialModelPrice{
			"gpt-4o": {ModelName: "gpt-4o", Source: officialPricingSourceBasellm, InputPrice: 2.5},
		},
	}
	if _, ok := officialPriceIndexFor(snapshot).lookup("gpt-4o"); !ok {
		t.Fatal("index must contain the original entry")
	}
	// 同一个 revision 意味着内容指纹相同，索引应直接复用——
	// 所以即使 Models 换了，也不该重建出新的匹配项。
	reused := officialPricingSnapshot{
		Revision: "same-revision",
		Models: map[string]officialModelPrice{
			"brand-new": {ModelName: "brand-new", Source: officialPricingSourceBasellm, InputPrice: 9},
		},
	}
	if _, ok := officialPriceIndexFor(reused).lookup("brand-new"); ok {
		t.Fatal("index should be reused for the same revision")
	}
	// revision 变了才重建。
	fresh := officialPricingSnapshot{
		Revision: "different",
		Models: map[string]officialModelPrice{
			"brand-new": {ModelName: "brand-new", Source: officialPricingSourceBasellm, InputPrice: 9},
		},
	}
	if _, ok := officialPriceIndexFor(fresh).lookup("brand-new"); !ok {
		t.Fatal("changing the revision must rebuild the index")
	}
}
