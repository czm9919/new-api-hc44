package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SetChannelModelEnabled：单模型下架/恢复只影响该 (channel, model) 行。
func TestSetChannelModelEnabled(t *testing.T) {
	setupFreeQuotaTestDB(t)
	require.NoError(t, DB.AutoMigrate(&Ability{}))
	require.NoError(t, DB.Create(&Ability{
		Group: "default", Model: "m-a", ChannelId: 7, Enabled: true,
	}).Error)
	require.NoError(t, DB.Create(&Ability{
		Group: "default", Model: "m-b", ChannelId: 7, Enabled: true,
	}).Error)

	// 下架 m-a
	changed, err := SetChannelModelEnabled(7, "m-a", false)
	require.NoError(t, err)
	assert.True(t, changed)

	var a, b Ability
	require.NoError(t, DB.Where("channel_id = ? AND model = ?", 7, "m-a").First(&a).Error)
	require.NoError(t, DB.Where("channel_id = ? AND model = ?", 7, "m-b").First(&b).Error)
	assert.False(t, a.Enabled)
	assert.True(t, b.Enabled, "同一渠道的其他模型不受影响")

	// 幂等：同值重复下架不算变化
	changed, err = SetChannelModelEnabled(7, "m-a", false)
	require.NoError(t, err)
	assert.False(t, changed)

	// 恢复上架
	changed, err = SetChannelModelEnabled(7, "m-a", true)
	require.NoError(t, err)
	assert.True(t, changed)
	require.NoError(t, DB.Where("channel_id = ? AND model = ?", 7, "m-a").First(&a).Error)
	assert.True(t, a.Enabled)

	// 不存在的 ability 行
	changed, err = SetChannelModelEnabled(7, "m-x", false)
	require.NoError(t, err)
	assert.False(t, changed)

	// 非法参数
	changed, err = SetChannelModelEnabled(0, "m-a", false)
	require.NoError(t, err)
	assert.False(t, changed)
}
