package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPoolChannel 在测试库里造一个号池渠道。
func newPoolChannel(t *testing.T, name string, keys string, models string) *model.Channel {
	t.Helper()
	base := "https://integrate.api.nvidia.com"
	ch := &model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Name:    name,
		Status:  1,
		BaseURL: &base,
		Models:  models,
		Group:   "default",
		Tag:     strPtr("keypool-nvidia"),
		Key:     keys,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:   true,
			MultiKeyMode: constant.MultiKeyModePolling,
			MultiKeySize: len([]string{keys}),
		},
	}
	require.NoError(t, ch.Insert())
	return ch
}

// 号池设置能读能写，且写完立刻生效。
func TestKeyPoolSettingsRoundTrip(t *testing.T) {
	setupKeyPoolTestDB(t)

	// 默认值
	s := GetKeyPoolSettings()
	assert.Equal(t, KeyPoolUserMaxTokensPerSubmitDefault, s.UserMaxTokensPerSubmit)
	assert.True(t, s.AllowUserContribute)
	assert.True(t, s.AutoBindModels)

	// 写入
	require.NoError(t, UpdateKeyPoolSettings(KeyPoolSettings{
		DefaultDailyGiftQuota:  12345,
		UserMaxTokensPerSubmit: 3,
		AllowUserContribute:    false,
		AutoBindModels:         false,
	}))

	s = GetKeyPoolSettings()
	assert.Equal(t, 12345, s.DefaultDailyGiftQuota)
	assert.Equal(t, 3, s.UserMaxTokensPerSubmit)
	assert.False(t, s.AllowUserContribute)
	assert.False(t, s.AutoBindModels)
	assert.False(t, IsUserContributeAllowed())
}

// 非法值必须被拒绝：负额度、0 上限。
func TestUpdateKeyPoolSettingsRejectsInvalidValues(t *testing.T) {
	setupKeyPoolTestDB(t)
	assert.Error(t, UpdateKeyPoolSettings(KeyPoolSettings{
		DefaultDailyGiftQuota:  -1,
		UserMaxTokensPerSubmit: 1,
	}))
	assert.Error(t, UpdateKeyPoolSettings(KeyPoolSettings{
		DefaultDailyGiftQuota:  1,
		UserMaxTokensPerSubmit: 0,
	}))
}

// 渠道视图要正确反映密钥健康度：总数、可用数、停用数。
func TestGetKeyPoolChannelsReportsKeyHealth(t *testing.T) {
	setupKeyPoolTestDB(t)
	ch := newPoolChannel(t, "英伟达NVIDIA-号池-1", "k1\nk2\nk3", "model-a,model-b")
	require.True(t, ch.ChannelInfo.IsMultiKey, "前置条件：多密钥渠道")

	// 手动停用第 2 把
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{1: common.ChannelStatusManuallyDisabled}
	ch.ChannelInfo.MultiKeyDisabledReason = map[int]string{1: "管理员手动停用"}
	require.NoError(t, ch.SaveChannelInfo())

	channels, err := GetKeyPoolChannels()
	require.NoError(t, err)
	require.Len(t, channels, 1)
	c := channels[0]
	assert.Equal(t, ch.Id, c.Id)
	assert.Equal(t, "nvidia", c.Provider)
	assert.Equal(t, 3, c.KeyCount)
	assert.Equal(t, 2, c.EnabledKeys)
	assert.Equal(t, 1, c.DisabledKeys)
	assert.Equal(t, 2, c.ModelCount)
	assert.True(t, c.IsMultiKey)
	assert.Equal(t, "https://integrate.api.nvidia.com", c.BaseURL)
}

// 单把密钥启停后，渠道视图与密钥明细都要同步。
func TestSetPoolChannelKeyStatus(t *testing.T) {
	setupKeyPoolTestDB(t)
	ch := newPoolChannel(t, "英伟达NVIDIA-号池-1", "k1\nk2", "model-a")

	// 停用第 0 把
	require.NoError(t, SetPoolChannelKeyStatus(ch.Id, 0, false))
	keys, err := GetPoolChannelKeys(ch.Id)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.False(t, keys[0].Enabled)
	assert.True(t, keys[1].Enabled)
	assert.NotEmpty(t, keys[0].Reason)

	// 重新启用
	require.NoError(t, SetPoolChannelKeyStatus(ch.Id, 0, true))
	keys, err = GetPoolChannelKeys(ch.Id)
	require.NoError(t, err)
	assert.True(t, keys[0].Enabled)
	assert.Empty(t, keys[0].Reason)

	// 越界索引必须报错
	assert.Error(t, SetPoolChannelKeyStatus(ch.Id, 99, false))
	assert.Error(t, SetPoolChannelKeyStatus(ch.Id, -1, false))
}

// 非号池渠道不允许这些操作。
func TestPoolAdminOpsRejectNonPoolChannel(t *testing.T) {
	setupKeyPoolTestDB(t)
	base := "https://example.com"
	normal := &model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Name:    "普通渠道",
		Status:  1,
		BaseURL: &base,
		Models:  "gpt-4o",
		Group:   "default",
		Key:     "sk-xxx",
	}
	require.NoError(t, normal.Insert())

	assert.Error(t, SetPoolChannelKeyStatus(normal.Id, 0, false))
	_, err := GetPoolChannelKeys(normal.Id)
	assert.Error(t, err)
	_, err = TestKeyPoolChannel(normal.Id)
	assert.Error(t, err)
}

// 号池 Token 列表：按供应商筛选、总数统计、密钥脱敏。
func TestGetKeyPoolTokensListsPoolTokens(t *testing.T) {
	setupKeyPoolTestDB(t)
	ch := newPoolChannel(t, "英伟达NVIDIA-号池-1", "k1\nk2", "model-a")

	tokens := []model.Token{
		{UserId: 1, Key: "pool-token-aaaaaaaa", Status: 1, Name: "英伟达NVIDIA-号池token-1",
			SourceChannelId: ch.Id, DailyGiftQuota: 50000, RemainQuota: 20000, FreeQuota: 20000},
		{UserId: 2, Key: "pool-token-bbbbbbbb", Status: 1, Name: "英伟达NVIDIA-号池token-2",
			SourceChannelId: ch.Id, DailyGiftQuota: 10000, RemainQuota: 0},
	}
	for i := range tokens {
		require.NoError(t, tokens[i].Insert())
	}

	rows, total, err := GetKeyPoolTokens("", 0, "", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, rows, 2)
	assert.Equal(t, "nvidia", rows[0].Provider)
	// 密钥必须脱敏，不能返回明文
	assert.NotContains(t, rows[0].KeyMask, "pool-token-aaaaaaaa")
	assert.Contains(t, rows[0].KeyMask, "****")

	// 按用户筛选
	rows, total, err = GetKeyPoolTokens("", 1, "", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, 1, rows[0].UserId)

	// 按供应商筛选（不存在的供应商返回空，而不是全表）
	rows, total, err = GetKeyPoolTokens("sensenova", 0, "", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, rows)
}

// 总览统计：渠道数、密钥数、Token 数与存活数。
func TestGetKeyPoolOverview(t *testing.T) {
	setupKeyPoolTestDB(t)
	ch := newPoolChannel(t, "英伟达NVIDIA-号池-1", "k1\nk2", "model-a")
	require.NoError(t, (&model.Token{
		UserId: 1, Key: "pool-token-cccccccc", Status: 1, Name: "t1",
		SourceChannelId: ch.Id, DailyGiftQuota: 1000, RemainQuota: 500,
	}).Insert())

	ov, err := GetKeyPoolOverview()
	require.NoError(t, err)
	assert.Equal(t, 1, ov.ChannelCount)
	assert.Equal(t, 2, ov.TotalKeys)
	assert.Equal(t, 1, ov.TokenCount)
	assert.Equal(t, 1, ov.AliveTokens)
	assert.Equal(t, 0, ov.DeadTokens)
	assert.Equal(t, 500, ov.TokenRemain)
}

// 手动重置号池 Token 额度：余额回到每日额度上限。
func TestResetKeyPoolTokenQuota(t *testing.T) {
	setupKeyPoolTestDB(t)
	ch := newPoolChannel(t, "英伟达NVIDIA-号池-1", "k1", "model-a")
	tk := &model.Token{
		UserId: 1, Key: "pool-token-dddddddd", Status: 1, Name: "t1",
		SourceChannelId: ch.Id, DailyGiftQuota: 8888, RemainQuota: 100, FreeQuota: 100,
	}
	require.NoError(t, tk.Insert())

	require.NoError(t, ResetKeyPoolTokenQuota(tk.Id))
	var got model.Token
	require.NoError(t, model.DB.First(&got, "id = ?", tk.Id).Error)
	assert.Equal(t, 8888, got.RemainQuota)
	assert.Equal(t, 8888, got.FreeQuota)

	// 非号池 Token（没有每日额度）不允许重置
	plain := &model.Token{UserId: 1, Key: "plain-token-eeeeeeee", Status: 1, Name: "t2"}
	require.NoError(t, plain.Insert())
	assert.Error(t, ResetKeyPoolTokenQuota(plain.Id))
}
