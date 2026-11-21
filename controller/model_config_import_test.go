package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectImportFormat(t *testing.T) {
	assert.Equal(t, "litellm", detectImportFormat([]byte(`{"model_list":[]}`)))
	assert.Equal(t, "openrouter", detectImportFormat([]byte(`{"data":[]}`)))
	assert.Equal(t, "", detectImportFormat([]byte(`{"other":[]}`)))
	assert.Equal(t, "", detectImportFormat([]byte(`not json`)))
}

func TestConvertLiteLLMModels(t *testing.T) {
	data := []byte(`{
		"model_list": [
			{
				"model_name": "gpt-4o",
				"litellm_params": {"input_cost_per_token": 0.000003, "output_cost_per_token": 0.000012}
			},
			{
				"model_name": "per-second-model",
				"litellm_params": {"input_cost_per_second": 0.0001}
			},
			{
				"model_name": "no-output-model",
				"litellm_params": {"input_cost_per_token": 0.000002}
			},
			{"model_name": "", "litellm_params": {"input_cost_per_token": 0.001}}
		]
	}`)
	items, skipped := convertLiteLLMModels(data)
	require.Len(t, items, 2)
	assert.Len(t, skipped, 1)
	assert.Contains(t, skipped[0], "per-second-model")

	byName := map[string]importedModelPrice{}
	for _, item := range items {
		byName[item.ModelName] = item
	}
	gpt, ok := byName["gpt-4o"]
	require.True(t, ok)
	assert.Equal(t, 0.000003, gpt.InputPerToken)
	assert.Equal(t, 0.000012, gpt.OutputPerToken)

	// output 缺省时回落为 input
	noOut, ok := byName["no-output-model"]
	require.True(t, ok)
	assert.Equal(t, 0.000002, noOut.OutputPerToken)
}

func TestConvertLiteLLMModelsInvalidJSON(t *testing.T) {
	items, skipped := convertLiteLLMModels([]byte(`{bad`))
	assert.Nil(t, items)
	require.Len(t, skipped, 1)
	assert.Contains(t, skipped[0], "invalid LiteLLM JSON")
}

func TestConvertOpenRouterModels(t *testing.T) {
	data := []byte(`{
		"data": [
			{"id": "openai/gpt-4o", "pricing": {"prompt": "0.000003", "completion": "0.000012"}},
			{"id": "free/model", "pricing": {"prompt": "0", "completion": "0"}},
			{"id": "minus/model", "pricing": {"prompt": "-1", "completion": "-1"}},
			{"id": "garbage/model", "pricing": {"prompt": "abc", "completion": "0.001"}},
			{"id": "output-free/model", "pricing": {"prompt": "0.000002", "completion": "0"}}
		]
	}`)
	items, skipped := convertOpenRouterModels(data)
	require.Len(t, items, 2)
	require.Len(t, skipped, 3)

	byName := map[string]importedModelPrice{}
	for _, item := range items {
		byName[item.ModelName] = item
	}
	gpt, ok := byName["openai/gpt-4o"]
	require.True(t, ok)
	assert.Equal(t, 0.000003, gpt.InputPerToken)
	assert.Equal(t, 0.000012, gpt.OutputPerToken)

	outFree, ok := byName["output-free/model"]
	require.True(t, ok)
	assert.Equal(t, 0.000002, outFree.OutputPerToken, "output 为 0 时回落为 input")
}

func TestConvertOpenRouterModelsInvalidJSON(t *testing.T) {
	items, skipped := convertOpenRouterModels([]byte(`nope`))
	assert.Nil(t, items)
	require.Len(t, skipped, 1)
	assert.Contains(t, skipped[0], "invalid OpenRouter JSON")
}

func TestApplyImportedPrices(t *testing.T) {
	// 预置现有倍率，验证冲突标注；结束时恢复原状，避免污染其他用例
	savedRatio := ratio_setting.ModelRatio2JSONString()
	savedCompletion := ratio_setting.CompletionRatio2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(savedRatio)
		_ = ratio_setting.UpdateCompletionRatioByJSONString(savedCompletion)
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"openai/gpt-4o": 1.0}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"openai/gpt-4o": 2.0}`))

	items := []importedModelPrice{
		// 3 USD / M input → ratio = 3 × 500000 / 1e6 = 1.5
		{ModelName: "openai/gpt-4o", InputPerToken: 0.000003, OutputPerToken: 0.000006},
		// 2 USD / M input → ratio = 1.0；output/input = 3 → completion 3
		{ModelName: "fresh/model", InputPerToken: 0.000002, OutputPerToken: 0.000006},
	}
	items = applyImportedPrices(items)
	require.Len(t, items, 2)

	gpt := items[0]
	assert.Equal(t, 1.0, gpt.ExistingRatio)
	assert.Equal(t, 2.0, gpt.ExistingCompletion)
	assert.Equal(t, 1.5, gpt.NewRatio)
	assert.Equal(t, 2.0, gpt.NewCompletion)

	fresh := items[1]
	assert.Equal(t, 0.0, fresh.ExistingRatio)
	assert.Equal(t, 1.0, fresh.NewRatio)
	assert.Equal(t, 3.0, fresh.NewCompletion)
}

func TestApplyImportedPricesCompletionFloor(t *testing.T) {
	// output 低于 input 时补全倍率至少为 1（输出不可能比输入便宜）
	items := applyImportedPrices([]importedModelPrice{
		{ModelName: "cheap-out/model", InputPerToken: 0.000004, OutputPerToken: 0.000001},
	})
	require.Len(t, items, 1)
	assert.Equal(t, 1.0, items[0].NewCompletion)
}

func TestRoundTo(t *testing.T) {
	assert.Equal(t, 1.5, roundTo(1.50000001, 4))
	assert.Equal(t, 1.2346, roundTo(1.23456, 4))
	assert.Equal(t, 3.0, roundTo(3.0000001, 6))
}
