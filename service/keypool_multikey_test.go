package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupKeyPoolTestDB 用内存 SQLite 起一个干净的 channels/abilities/tokens 表。
func setupKeyPoolTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Token{}, &model.Option{}))
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous })

	// Option 更新会写 common.OptionMap，未初始化时会 panic（生产环境由 InitOptionMap 保证）。
	prevOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	t.Cleanup(func() { common.OptionMap = prevOptionMap })
}

// mockPoolUpstream 起一个假的 OpenAI 兼容上游：
//   - POST /v1/chat/completions → 200（NVIDIA 的 key 验活探针走这里）
//   - GET  /v1/models            → 固定模型列表
func mockPoolUpstream(t *testing.T, models []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "ok"}}},
				"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			})
		case strings.HasSuffix(r.URL.Path, "/models"):
			data := make([]map[string]string, 0, len(models))
			for _, m := range models {
				data = append(data, map[string]string{"id": m})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// useMockPoolProvider 把某个供应商的验活地址指向 mock 上游，测试结束后还原。
func useMockPoolProvider(t *testing.T, provider KeyPoolProvider, baseURL string) {
	t.Helper()
	original, existed := providerBaseURL[provider]
	providerBaseURL[provider] = baseURL
	t.Cleanup(func() {
		if existed {
			providerBaseURL[provider] = original
		} else {
			delete(providerBaseURL, provider)
		}
	})
}

// loadPoolChannel 从库里读回号池渠道，验证真正落库的数据（而不是内存里的对象）。
func loadPoolChannel(t *testing.T, id int) *model.Channel {
	t.Helper()
	var ch model.Channel
	require.NoError(t, model.DB.First(&ch, "id = ?", id).Error)
	return &ch
}

// 号池渠道必须落库为「多密钥渠道」：is_multi_key=true、multi_key_mode=polling、
// multi_key_size 等于 key 条数。否则 GetNextEnabledKey 会把整串 "k1\nk2\nk3"
// 当成一个密钥发给上游，请求必然全部鉴权失败。
func TestGenerateKeyPoolPersistsMultiKeyChannel(t *testing.T) {
	setupKeyPoolTestDB(t)
	srv := mockPoolUpstream(t, []string{"model-a", "model-b"})
	useMockPoolProvider(t, ProviderNVIDIA, srv.URL+"/v1")

	result, err := GenerateKeyPool(KeyPoolRequest{
		Provider:      string(ProviderNVIDIA),
		Keys:          "k1\nk2\nk3",
		UserId:        1,
		TokenCount:    1,
		QuotaPerToken: 100,
		Group:         "default",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 3, result.ValidCount, "三条 key 都应通过验活")
	assert.Equal(t, "model-a,model-b", result.Models, "模型列表应来自上游 /models")
	assert.Equal(t, 1, result.ChannelsCreated, "首次应新建渠道")

	ch := loadPoolChannel(t, result.PoolChannelId)
	assert.True(t, ch.ChannelInfo.IsMultiKey, "号池渠道必须落库为多密钥渠道")
	assert.Equal(t, constant.MultiKeyModePolling, ch.ChannelInfo.MultiKeyMode,
		"号池渠道应按轮询取 key")
	assert.Equal(t, 3, ch.ChannelInfo.MultiKeySize, "multi_key_size 应等于 key 条数")
	assert.Equal(t, 3, countChannelKeys(ch.Key), "库里应存 3 条 key")
}

// GetNextEnabledKey 必须逐条返回 key，而不是把整串换行分隔的 key 当成一个密钥。
func TestPoolChannelGetNextEnabledKeyReturnsSingleKey(t *testing.T) {
	setupKeyPoolTestDB(t)
	srv := mockPoolUpstream(t, []string{"model-a"})
	useMockPoolProvider(t, ProviderNVIDIA, srv.URL+"/v1")

	result, err := GenerateKeyPool(KeyPoolRequest{
		Provider:      string(ProviderNVIDIA),
		Keys:          "k1\nk2",
		UserId:        1,
		TokenCount:    1,
		QuotaPerToken: 100,
		Group:         "default",
	})
	require.NoError(t, err)

	ch := loadPoolChannel(t, result.PoolChannelId)
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		key, idx, err := ch.GetNextEnabledKey()
		// 注意：GetNextEnabledKey 返回的是 *types.NewAPIError（具体类型），
		// 成功时是「类型化 nil」。直接传进 require.NoError 会被当成非 nil 的 error，
		// 所以这里必须按指针比较，不能当 error 接口传。
		require.Nilf(t, err, "第 %d 次取 key 失败（mode=%q, isMultiKey=%v）",
			i, ch.ChannelInfo.MultiKeyMode, ch.ChannelInfo.IsMultiKey)
		t.Logf("诊断: 取得 key=%q idx=%d", key, idx)
		assert.NotContains(t, key, "\n", "返回的应是单条 key，而不是整串")
		assert.Contains(t, []string{"k1", "k2"}, key)
		seen[key] = true
	}
	assert.Len(t, seen, 2, "轮询应覆盖到全部 key")
}

// 第二次生成时合并进已有渠道，多密钥标记与 key 条数都要保持。
func TestGenerateKeyPoolMergesIntoExistingMultiKeyChannel(t *testing.T) {
	setupKeyPoolTestDB(t)
	srv := mockPoolUpstream(t, []string{"model-a"})
	useMockPoolProvider(t, ProviderNVIDIA, srv.URL+"/v1")

	first, err := GenerateKeyPool(KeyPoolRequest{
		Provider:      string(ProviderNVIDIA),
		Keys:          "k1\nk2",
		UserId:        1,
		TokenCount:    1,
		QuotaPerToken: 100,
		Group:         "default",
	})
	require.NoError(t, err)

	second, err := GenerateKeyPool(KeyPoolRequest{
		Provider:      string(ProviderNVIDIA),
		Keys:          "k3",
		UserId:        1,
		TokenCount:    1,
		QuotaPerToken: 100,
		Group:         "default",
	})
	require.NoError(t, err)

	assert.Equal(t, first.PoolChannelId, second.PoolChannelId, "应复用同一个号池渠道")
	assert.Equal(t, 0, second.ChannelsCreated, "第二次不应新建渠道")

	ch := loadPoolChannel(t, second.PoolChannelId)
	assert.True(t, ch.ChannelInfo.IsMultiKey, "合并后仍应是多密钥渠道")
	assert.Equal(t, 3, ch.ChannelInfo.MultiKeySize)
	assert.Equal(t, 3, countChannelKeys(ch.Key))
	assert.Equal(t, "k1\nk2\nk3", ch.Key, "合并应保持原有顺序并追加新 key")
}

// 只提交一把 key 时不应被标成多密钥渠道（整串只有一条，取 key 行为一致）。
func TestGenerateKeyPoolSingleKeyIsNotMultiKey(t *testing.T) {
	setupKeyPoolTestDB(t)
	srv := mockPoolUpstream(t, []string{"model-a"})
	useMockPoolProvider(t, ProviderNVIDIA, srv.URL+"/v1")

	result, err := GenerateKeyPool(KeyPoolRequest{
		Provider:      string(ProviderNVIDIA),
		Keys:          "solo",
		UserId:        1,
		TokenCount:    1,
		QuotaPerToken: 100,
		Group:         "default",
	})
	require.NoError(t, err)

	ch := loadPoolChannel(t, result.PoolChannelId)
	assert.False(t, ch.ChannelInfo.IsMultiKey, "单 key 渠道无需标记为多密钥")
	assert.Equal(t, 1, countChannelKeys(ch.Key))
}

// 历史号池渠道可能有多把 key 却没标 is_multi_key（is_multi_key=false）。
// 这种渠道会被 GetNextEnabledKey 整串当成一把密钥发给上游，请求全部鉴权失败。
// MigrateKeyPoolChannels 必须把它自愈成多密钥渠道。
func TestMigrateKeyPoolChannelsHealsMissingMultiKeyFlag(t *testing.T) {
	setupKeyPoolTestDB(t)
	srv := mockPoolUpstream(t, []string{"model-a"})
	useMockPoolProvider(t, ProviderNVIDIA, srv.URL+"/v1")

	// 先造一个「多把 key 但没标多密钥」的历史渠道。
	base := "http://127.0.0.1:9999"
	legacy := &model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Name:    "英伟达NVIDIA-号池-1",
		Status:  1,
		BaseURL: &base,
		Models:  "model-a",
		Group:   "default",
		Tag:     strPtr("keypool-nvidia"),
		Key:     "k1\nk2\nk3",
	}
	require.NoError(t, legacy.Insert())

	ch := loadPoolChannel(t, legacy.Id)
	require.False(t, ch.ChannelInfo.IsMultiKey, "前置条件：这条渠道起初没标多密钥")
	require.Equal(t, 3, countChannelKeys(ch.Key))

	require.NoError(t, MigrateKeyPoolChannels())

	ch = loadPoolChannel(t, legacy.Id)
	assert.True(t, ch.ChannelInfo.IsMultiKey, "迁移应把渠道自愈成多密钥渠道")
	assert.Equal(t, constant.MultiKeyModePolling, ch.ChannelInfo.MultiKeyMode)
	assert.Equal(t, 3, ch.ChannelInfo.MultiKeySize, "multi_key_size 应与实际 key 条数一致")

	// 自愈后必须能逐条取到 key。
	key, _, err := ch.GetNextEnabledKey()
	require.Nil(t, err)
	assert.NotContains(t, key, "\n")
	assert.Contains(t, []string{"k1", "k2", "k3"}, key)
}

// 迁移是幂等的：第二次执行不应再产生变更。
func TestMigrateKeyPoolChannelsIsIdempotent(t *testing.T) {
	setupKeyPoolTestDB(t)
	srv := mockPoolUpstream(t, []string{"model-a"})
	useMockPoolProvider(t, ProviderNVIDIA, srv.URL+"/v1")

	base := "http://127.0.0.1:9999"
	legacy := &model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Name:    "英伟达NVIDIA-号池-1",
		Status:  1,
		BaseURL: &base,
		Models:  "*",
		Group:   "default",
		Tag:     strPtr("keypool-nvidia"),
		Key:     "k1\nk2",
	}
	require.NoError(t, legacy.Insert())

	require.NoError(t, MigrateKeyPoolChannels())
	first := loadPoolChannel(t, legacy.Id)

	require.NoError(t, MigrateKeyPoolChannels())
	second := loadPoolChannel(t, legacy.Id)

	assert.Equal(t, first.ChannelInfo.IsMultiKey, second.ChannelInfo.IsMultiKey)
	assert.Equal(t, first.ChannelInfo.MultiKeyMode, second.ChannelInfo.MultiKeyMode)
	assert.Equal(t, first.Models, second.Models)
	assert.Equal(t, first.BaseURL, second.BaseURL)
}
