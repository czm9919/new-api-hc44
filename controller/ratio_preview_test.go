package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupRatioPreviewTest wires an in-memory SQLite LOG_DB and restores the
// current model-ratio map afterwards (v1.1.0 M1 · B22).
func setupRatioPreviewTest(t *testing.T) {
	t.Helper()
	originalLogDB := model.LOG_DB
	savedRatios := ratio_setting.ModelRatio2JSONString()
	savedGroups := ratio_setting.GroupRatio2JSONString()

	logDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := logDB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	model.LOG_DB = logDB
	t.Cleanup(func() {
		model.LOG_DB = originalLogDB
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroups))
	})
}

func callRatioPreview(t *testing.T, body any) RatioDiffPreviewData {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/option/ratio_diff_preview", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	PreviewRatioDiff(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Success bool                 `json:"success"`
		Data    RatioDiffPreviewData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Success, w.Body.String())
	return resp.Data
}

func TestPreviewRatioDiffModelLinearEstimate(t *testing.T) {
	setupRatioPreviewTest(t)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4": 5, "cheap": 1}`))
	seedLogs := []model.Log{
		{Type: model.LogTypeConsume, Quota: 1000, ModelName: "gpt-4"},
		{Type: model.LogTypeConsume, Quota: 400, ModelName: "cheap"},
		{Type: model.LogTypeError, Quota: 0, ModelName: "gpt-4"},
		{Type: model.LogTypeConsume, Quota: 12345, ModelName: "untouched"},
	}
	for i := range seedLogs {
		require.NoError(t, model.LOG_DB.Create(&seedLogs[i]).Error)
	}

	data := callRatioPreview(t, gin.H{
		"ratio_type": "model",
		"new_ratio":  gin.H{"gpt-4": 10, "cheap": 0.5},
	})

	assert.Equal(t, "model", data.RatioType)
	assert.Equal(t, 2, data.ChangedModels)
	assert.Equal(t, 7, data.Days)

	// gpt-4: 1000 * (10-5)/5 = +1000；cheap: 400 * (0.5-1)/1 = -200
	assert.Equal(t, int64(1000+400), data.BeforeQuota)
	assert.Equal(t, int64(1000+400+1000-200), data.AfterQuota)
	assert.Equal(t, int64(800), data.DeltaQuota)

	// 按预估影响绝对值降序：gpt-4(+1000) 在 cheap(-200) 之前
	require.Len(t, data.Items, 2)
	assert.Equal(t, "gpt-4", data.Items[0].Name)
	assert.Equal(t, int64(1000), data.Items[0].EstDeltaQuota)
	assert.Equal(t, int64(-200), data.Items[1].EstDeltaQuota)
}

func TestPreviewRatioDiffZeroOldRatioCountsFullQuota(t *testing.T) {
	setupRatioPreviewTest(t)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"free-model": 0}`))
	require.NoError(t, model.LOG_DB.Create(&model.Log{Type: model.LogTypeConsume, Quota: 700, ModelName: "free-model"}).Error)

	data := callRatioPreview(t, gin.H{
		"ratio_type": "model",
		"new_ratio":  gin.H{"free-model": 2},
	})
	require.Len(t, data.Items, 1)
	// 旧倍率为 0（此前免费/漏计）：增量按全部用量计。
	assert.Equal(t, int64(700), data.Items[0].EstDeltaQuota)
}

func TestPreviewRatioDiffGroup(t *testing.T) {
	setupRatioPreviewTest(t)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default": 1, "vip": 2}`))
	require.NoError(t, model.LOG_DB.Create(&model.Log{Type: model.LogTypeConsume, Quota: 2000, Group: "vip", ModelName: "m"}).Error)
	require.NoError(t, model.LOG_DB.Create(&model.Log{Type: model.LogTypeConsume, Quota: 100, Group: "default", ModelName: "m"}).Error)

	data := callRatioPreview(t, gin.H{
		"ratio_type": "group",
		"new_ratio":  gin.H{"vip": 3},
	})
	require.Len(t, data.Items, 1)
	assert.Equal(t, "vip", data.Items[0].Name)
	// 2000 * (3-2)/2 = +1000
	assert.Equal(t, int64(1000), data.Items[0].EstDeltaQuota)
}

func TestPreviewRatioDiffRejectsUnknownType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, err := json.Marshal(gin.H{"ratio_type": "bogus", "new_ratio": gin.H{"a": 1}})
	require.NoError(t, err)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/option/ratio_diff_preview", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	PreviewRatioDiff(c)
	// ApiError 保持 HTTP 200，以 success:false 表达失败。
	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success)
}
