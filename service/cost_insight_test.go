package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v1.1.0 M2 · B90 成本优化建议：近 N 天逐模型消耗 + 当前倍率 → 更便宜替代。
func TestBuildCostInsightSuggestsCheaperAlternative(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))

	// 保存并恢复倍率配置
	origRatio := ratio_setting.GetModelRatioCopy()
	origCompletion := ratio_setting.GetCompletionRatioCopy()
	restore := func() {
		b, _ := json.Marshal(origRatio)
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(b)))
		b, _ = json.Marshal(origCompletion)
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(string(b)))
	}
	setRatio := func(m map[string]float64) {
		b, _ := json.Marshal(m)
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(b)))
	}
	setRatio(map[string]float64{"expensive-model": 15, "cheap-model": 3})
	{
		b, _ := json.Marshal(map[string]float64{"expensive-model": 1, "cheap-model": 1})
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(string(b)))
	}
	t.Cleanup(restore)

	// 用户 + 可用模型（default 分组提供两个模型）
	user := &model.User{Username: "cost_insight_user", Password: "123456", Role: common.RoleCommonUser, Group: "default", Quota: 1000000}
	require.NoError(t, model.DB.Create(user).Error)
	t.Cleanup(func() { model.DB.Unscoped().Delete(user) })
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "expensive-model", Enabled: true, ChannelId: 9901}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "cheap-model", Enabled: true, ChannelId: 9901}).Error)
	t.Cleanup(func() {
		model.DB.Where("channel_id = ?", 9901).Delete(&model.Ability{})
		model.DB.Where("user_id = ?", user.Id).Delete(&model.Log{})
	})

	now := time.Now().Unix()
	seedCostLog := func(modelName string, quota, prompt, completion int64, cnt int) {
		for i := 0; i < cnt; i++ {
			require.NoError(t, model.LOG_DB.Create(&model.Log{
				UserId:           user.Id,
				Type:             model.LogTypeConsume,
				ModelName:        modelName,
				Quota:            int(quota),
				PromptTokens:     int(prompt),
				CompletionTokens: int(completion),
				CreatedAt:        now - 3600,
			}).Error)
		}
	}
	seedCostLog("expensive-model", 2000, 1000, 500, 3) // 6000 总消耗
	seedCostLog("cheap-model", 500, 1000, 500, 2)      // 1000 总消耗
	seedCostLog("noise-model", 10, 10, 10, 1)          // 长尾，占比 < 10%

	insight, err := BuildCostInsight(user.Id, 7)
	require.NoError(t, err)
	require.NotNil(t, insight)

	assert.Equal(t, 7, insight.WindowDays)
	assert.Equal(t, int64(7010), insight.TotalUsedQuota)
	require.Len(t, insight.Models, 3)
	// 占比降序
	assert.Equal(t, "expensive-model", insight.Models[0].ModelName)
	assert.InDelta(t, 6000.0/7010, insight.Models[0].QuotaShare, 1e-6)

	// expensive-model 有更便宜的替代 cheap-model：1 - 3/15 = 0.8
	require.Len(t, insight.Suggestions, 1)
	sug := insight.Suggestions[0]
	assert.Equal(t, "expensive-model", sug.ModelName)
	// 占比 6000/7010 ≈ 85.6% ≥ 50% → dominant_model，且附带更便宜的替代
	assert.Equal(t, "dominant_model", sug.ReasonCode)
	require.NotEmpty(t, sug.Alternatives)
	alt := sug.Alternatives[0]
	assert.Equal(t, "cheap-model", alt.ModelName)
	assert.InDelta(t, 0.8, alt.EstimatedSavingPercent, 1e-6)
	assert.Equal(t, int64(4800), alt.EstimatedSavingQuota)

	// noise-model 占比 10/7010 ≈ 0.14% < 10% 不出建议；
	// cheap-model 已是最便宜替代，自身不再出建议。
	for _, s := range insight.Suggestions {
		assert.NotEqual(t, "noise-model", s.ModelName)
		assert.NotEqual(t, "cheap-model", s.ModelName)
	}
}

// 无消耗记录时返回空建议而非报错。
func TestBuildCostInsightEmptyUsage(t *testing.T) {
	user := &model.User{Username: "cost_insight_empty", Password: "123456", Role: common.RoleCommonUser, Group: "default", Quota: 100}
	require.NoError(t, model.DB.Create(user).Error)
	t.Cleanup(func() { model.DB.Unscoped().Delete(user) })

	insight, err := BuildCostInsight(user.Id, 30)
	require.NoError(t, err)
	assert.Equal(t, int64(0), insight.TotalUsedQuota)
	assert.Empty(t, insight.Suggestions)
}

// NormalizeCostInsightDays 校正窗口。
func TestNormalizeCostInsightDays(t *testing.T) {
	assert.Equal(t, 30, NormalizeCostInsightDays(0))
	assert.Equal(t, 30, NormalizeCostInsightDays(-5))
	assert.Equal(t, 15, NormalizeCostInsightDays(15))
	assert.Equal(t, 90, NormalizeCostInsightDays(365))
}
