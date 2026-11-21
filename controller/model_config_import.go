package controller

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// B223 模型配置导入（LiteLLM / OpenRouter → new-api 倍率）
// ---------------------------------------------------------------------------

// 换算口径：
//   new-api 的模型倍率 ratio 满足「百万 token 美元单价 = ratio / QuotaPerUnit × 1e6」，
//   因此 ratio = 单 token 美元价 × QuotaPerUnit；
//   补全倍率 = output_cost_per_token / input_cost_per_token。
// 只导入价格口径清晰（input_cost_per_token / output_cost_per_token）的条目，
// LiteLLM 的 input_cost_per_second 等其他计价方式不支持，直接跳过。

const importMaxModels = 2000

type importedModelPrice struct {
	ModelName          string
	InputPerToken      float64 // USD / token
	OutputPerToken     float64 // USD / token
	ExistingRatio      float64 // 当前已配置的模型倍率（未配置为 0）
	ExistingCompletion float64
	NewRatio           float64
	NewCompletion      float64
}

type importModelConfigsRequest struct {
	Format string          `json:"format"` // "litellm" | "openrouter" | "auto"
	Data   json.RawMessage `json:"data"`
	DryRun bool            `json:"dry_run"`
}

// convertLiteLLMModels 解析 LiteLLM config 的 model_list。
func convertLiteLLMModels(data []byte) ([]importedModelPrice, []string) {
	type litellmParams struct {
		InputCostPerToken  float64 `json:"input_cost_per_token"`
		OutputCostPerToken float64 `json:"output_cost_per_token"`
	}
	type litellmEntry struct {
		ModelName     string        `json:"model_name"`
		LitellmParams litellmParams `json:"litellm_params"`
	}
	type litellmConfig struct {
		ModelList []litellmEntry `json:"model_list"`
	}
	var cfg litellmConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, []string{"invalid LiteLLM JSON: " + err.Error()}
	}
	skipped := []string{}
	result := make([]importedModelPrice, 0, len(cfg.ModelList))
	for _, entry := range cfg.ModelList {
		name := entry.ModelName
		if name == "" {
			continue
		}
		if entry.LitellmParams.InputCostPerToken <= 0 {
			skipped = append(skipped, name+" (no input_cost_per_token)")
			continue
		}
		output := entry.LitellmParams.OutputCostPerToken
		if output <= 0 {
			output = entry.LitellmParams.InputCostPerToken
		}
		result = append(result, importedModelPrice{
			ModelName:      name,
			InputPerToken:  entry.LitellmParams.InputCostPerToken,
			OutputPerToken: output,
		})
	}
	return result, skipped
}

// convertOpenRouterModels 解析 OpenRouter /api/v1/models 的 data 数组。
func convertOpenRouterModels(data []byte) ([]importedModelPrice, []string) {
	type openRouterPricing struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	}
	type openRouterEntry struct {
		Id      string            `json:"id"`
		Pricing openRouterPricing `json:"pricing"`
	}
	type openRouterResponse struct {
		Data []openRouterEntry `json:"data"`
	}
	var cfg openRouterResponse
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, []string{"invalid OpenRouter JSON: " + err.Error()}
	}
	skipped := []string{}
	result := make([]importedModelPrice, 0, len(cfg.Data))
	for _, entry := range cfg.Data {
		name := entry.Id
		if name == "" {
			continue
		}
		// OpenRouter 的价格字段是字符串，"-1" 表示免费/不适用，"0" 表示免费。
		input, err1 := strconv.ParseFloat(entry.Pricing.Prompt, 64)
		output, err2 := strconv.ParseFloat(entry.Pricing.Completion, 64)
		if err1 != nil || err2 != nil || input < 0 || output < 0 {
			skipped = append(skipped, name+" (unparsable pricing)")
			continue
		}
		if input <= 0 {
			skipped = append(skipped, name+" (free or zero input price)")
			continue
		}
		if output <= 0 {
			output = input
		}
		result = append(result, importedModelPrice{
			ModelName:      name,
			InputPerToken:  input,
			OutputPerToken: output,
		})
	}
	return result, skipped
}

// applyImportedPrices 把导入的逐 token 美元价换算成 new-api 倍率，并标注与现有配置的冲突。
func applyImportedPrices(items []importedModelPrice) []importedModelPrice {
	existingRatio := ratio_setting.GetModelRatioCopy()
	existingCompletion := ratio_setting.GetCompletionRatioCopy()
	quotaPerUnit := float64(common.QuotaPerUnit)
	for i := range items {
		items[i].ExistingRatio = existingRatio[items[i].ModelName]
		items[i].ExistingCompletion = existingCompletion[items[i].ModelName]
		items[i].NewRatio = roundTo(items[i].InputPerToken*quotaPerUnit, 4)
		if items[i].InputPerToken > 0 {
			items[i].NewCompletion = roundTo(items[i].OutputPerToken/items[i].InputPerToken, 4)
		}
		if items[i].NewCompletion < 1 {
			items[i].NewCompletion = 1
		}
	}
	return items
}

func roundTo(v float64, digits int) float64 {
	shift := math.Pow(10, float64(digits))
	return math.Round(v*shift) / shift
}

// ImportModelConfigs 管理员导入第三方价格表（B223）。
// dry_run=true 只返回换算预览（含冲突标注），不落库；
// dry_run=false 把「现有配置 ∪ 导入配置」写回 ModelRatio / CompletionRatio 选项。
func ImportModelConfigs(c *gin.Context) {
	var req importModelConfigsRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if len(req.Data) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	var items []importedModelPrice
	var skipped []string
	format := req.Format
	if format == "" {
		format = "auto"
	}
	if format == "auto" {
		format = detectImportFormat(req.Data)
	}
	switch format {
	case "litellm":
		items, skipped = convertLiteLLMModels(req.Data)
	case "openrouter":
		items, skipped = convertOpenRouterModels(req.Data)
	default:
		common.ApiError(c, fmt.Errorf("unsupported format %q (litellm / openrouter / auto)", req.Format))
		return
	}
	if len(items) > importMaxModels {
		common.ApiError(c, fmt.Errorf("at most %d models per import", importMaxModels))
		return
	}
	items = applyImportedPrices(items)

	sort.Slice(items, func(i, j int) bool { return items[i].ModelName < items[j].ModelName })
	type rowOut struct {
		ModelName          string  `json:"model_name"`
		InputPerTokenUSD   float64 `json:"input_per_token_usd"`
		OutputPerTokenUSD  float64 `json:"output_per_token_usd"`
		InputPricePerM     float64 `json:"input_price_per_million_usd"`
		OutputPricePerM    float64 `json:"output_price_per_million_usd"`
		ExistingRatio      float64 `json:"existing_model_ratio"`
		ExistingCompletion float64 `json:"existing_completion_ratio"`
		NewRatio           float64 `json:"new_model_ratio"`
		NewCompletion      float64 `json:"new_completion_ratio"`
		Conflict           bool    `json:"conflict"`
	}
	rows := make([]rowOut, 0, len(items))
	conflicts := 0
	for _, item := range items {
		conflict := item.ExistingRatio > 0 && math.Abs(item.ExistingRatio-item.NewRatio) > 1e-9
		if conflict {
			conflicts++
		}
		rows = append(rows, rowOut{
			ModelName:          item.ModelName,
			InputPerTokenUSD:   item.InputPerToken,
			OutputPerTokenUSD:  item.OutputPerToken,
			InputPricePerM:     roundTo(item.InputPerToken*1e6, 6),
			OutputPricePerM:    roundTo(item.OutputPerToken*1e6, 6),
			ExistingRatio:      item.ExistingRatio,
			ExistingCompletion: item.ExistingCompletion,
			NewRatio:           item.NewRatio,
			NewCompletion:      item.NewCompletion,
			Conflict:           conflict,
		})
	}

	response := gin.H{
		"format":    format,
		"total":     len(items),
		"skipped":   skipped,
		"conflicts": conflicts,
		"rows":      rows,
	}

	if req.DryRun {
		response["dry_run"] = true
		common.ApiSuccess(c, response)
		return
	}

	// 合并写回：以现有配置为底，导入条目覆盖同名模型。
	mergedRatio := ratio_setting.GetModelRatioCopy()
	mergedCompletion := ratio_setting.GetCompletionRatioCopy()
	for _, item := range items {
		mergedRatio[item.ModelName] = item.NewRatio
		mergedCompletion[item.ModelName] = item.NewCompletion
	}
	ratioJSON, err := common.Marshal(mergedRatio)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	completionJSON, err := common.Marshal(mergedCompletion)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOption("ModelRatio", string(ratioJSON)); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOption("CompletionRatio", string(completionJSON)); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

// detectImportFormat 通过顶层结构特征自动识别格式：
// 含 "model_list" → LiteLLM；含 "data" → OpenRouter。
func detectImportFormat(data []byte) string {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return ""
	}
	if _, ok := probe["model_list"]; ok {
		return "litellm"
	}
	if _, ok := probe["data"]; ok {
		return "openrouter"
	}
	return ""
}
