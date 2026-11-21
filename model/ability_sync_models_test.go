package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

// SyncChannelModelList：模型列表全量同步的增量语义——
// 新增模型插入 ability（默认上架）、上游下线的模型删除 ability、
// 既有模型的单模型下架状态（enabled=false）不被重置。
func TestSyncChannelModelList(t *testing.T) {
	setupFreeQuotaTestDB(t)
	require.NoError(t, DB.AutoMigrate(&Ability{}, &Channel{}))

	tag := "keypool-nvidia"
	ch := &Channel{
		Id:     11,
		Name:   "英伟达号池",
		Models: "m-a,m-b",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
		Tag:    &tag,
	}
	require.NoError(t, DB.Create(ch).Error)
	require.NoError(t, DB.Create(&Ability{Group: "default", Model: "m-a", ChannelId: 11, Enabled: true}).Error)
	require.NoError(t, DB.Create(&Ability{Group: "default", Model: "m-b", ChannelId: 11, Enabled: true}).Error)

	// 先把 m-b 自动下架，验证后续刷新不会把它重新上架
	changed, err := SetChannelModelEnabled(11, "m-b", false)
	require.NoError(t, err)
	require.True(t, changed)

	// 刷新：m-a/m-b 保留，新增 m-c/m-d（含去重、去空白、去空项）
	added, removed, err := SyncChannelModelList(ch, []string{"m-a", "m-b", "m-c", " m-d ", "m-c", ""})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"m-c", "m-d"}, added)
	assert.Empty(t, removed)

	var b Ability
	require.NoError(t, DB.Where("channel_id = ? AND model = ?", 11, "m-b").First(&b).Error)
	assert.False(t, b.Enabled, "既有模型的下架状态不被刷新重置")

	var c Ability
	require.NoError(t, DB.Where("channel_id = ? AND model = ?", 11, "m-c").First(&c).Error)
	assert.True(t, c.Enabled, "新模型默认上架且跟随渠道状态")

	var d Ability
	require.NoError(t, DB.Where("channel_id = ? AND model = ?", 11, "m-d").First(&d).Error)
	assert.True(t, d.Enabled)

	assert.Equal(t, "m-a,m-b,m-c,m-d", ch.Models, "渠道 models 字段同步为刷新后的列表")

	// 上游已下线 m-a、m-d：ability 行删除、渠道 models 同步移除
	added, removed, err = SyncChannelModelList(ch, []string{"m-b", "m-c"})
	require.NoError(t, err)
	assert.Empty(t, added)
	assert.ElementsMatch(t, []string{"m-a", "m-d"}, removed)

	var a Ability
	err = DB.Where("channel_id = ? AND model = ?", 11, "m-a").First(&a).Error
	assert.Error(t, err, "被移除模型的 ability 行应删除")
	assert.Equal(t, "m-b,m-c", ch.Models)

	// 列表无变化 → 空操作，不产生任何写放大
	added, removed, err = SyncChannelModelList(ch, []string{"m-b", "m-c"})
	require.NoError(t, err)
	assert.Empty(t, added)
	assert.Empty(t, removed)

	// 渠道被禁用时新增的模型 ability 也应为禁用（跟随渠道状态）
	ch.Status = common.ChannelStatusManuallyDisabled
	added, removed, err = SyncChannelModelList(ch, []string{"m-b", "m-c", "m-e"})
	require.NoError(t, err)
	assert.Equal(t, []string{"m-e"}, added)
	var e Ability
	require.NoError(t, DB.Where("channel_id = ? AND model = ?", 11, "m-e").First(&e).Error)
	assert.False(t, e.Enabled, "禁用渠道上新增的模型默认不上架")

	// 空列表拒绝
	_, _, err = SyncChannelModelList(ch, []string{"", " "})
	assert.Error(t, err)
}
