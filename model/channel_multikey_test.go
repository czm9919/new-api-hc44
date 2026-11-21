package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

// 号池渠道会把多个上游 key 合并进同一个渠道的 key 字段（换行分隔）。
// 如果忘了把渠道标记成多密钥渠道，GetNextEnabledKey 会走单密钥分支，
// 把 "key1\nkey2\nkey3" 整串当成一个密钥发给上游——所有请求都会鉴权失败。
// 这个用例把该约定钉死。
func TestGetNextEnabledKeyNeverReturnsJoinedKeyInMultiKeyMode(t *testing.T) {
	joined := "key-a\nkey-b\nkey-c"

	ch := &Channel{Id: 1, Key: joined}

	// 未开启多密钥：原样返回整串，说明「合并了 key 却没置 IsMultiKey」必然是坏的
	got, _, err := ch.GetNextEnabledKey()
	require.Nil(t, err) // 返回的是 *types.NewAPIError，typed-nil 用 Nil 判断
	require.Equal(t, joined, got, "单密钥分支会原样返回整串，这正是必须置 IsMultiKey 的原因")

	// 开启多密钥 + 随机：每次只返回其中一条，绝不含换行
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModeRandom
	ch.ChannelInfo.MultiKeySize = 3
	seen := make(map[string]struct{})
	for i := 0; i < 30; i++ {
		key, _, err := ch.GetNextEnabledKey()
		require.Nil(t, err) // 返回的是 *types.NewAPIError，typed-nil 用 Nil 判断
		require.False(t, strings.Contains(key, "\n"), "多密钥模式下不应返回拼接串，实际=%q", key)
		seen[key] = struct{}{}
	}
	require.Len(t, seen, 3, "多次取样应覆盖到全部 3 条 key")
}

// 全部 key 被禁用时不能退化成返回第一条，必须报错让上层停用渠道。
func TestGetNextEnabledKeyRejectsAllDisabledKeys(t *testing.T) {
	ch := &Channel{Id: 2, Key: "key-a\nkey-b"}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModeRandom
	ch.ChannelInfo.MultiKeySize = 2
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{
		0: common.ChannelStatusManuallyDisabled,
		1: common.ChannelStatusManuallyDisabled,
	}

	_, _, err := ch.GetNextEnabledKey()
	require.NotNil(t, err, "所有 key 都被禁用时应返回错误")
}

// GetKeys 负责把换行分隔（或 JSON 数组）的 key 串拆开，是轮询与状态索引的基础。
func TestGetKeysSplitsNewlineList(t *testing.T) {
	ch := &Channel{Key: "k1\nk2\n\nk3\n"}
	require.Equal(t, []string{"k1", "k2", "", "k3"}, ch.GetKeys())
}
