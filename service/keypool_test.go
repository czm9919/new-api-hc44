package service

import (
	"strings"
	"testing"
)

// 渠道的轮询下标与单 key 状态表都是按位置索引的，合并 key 时必须保持稳定顺序：
// 已有 key 原序在前，新 key 按提交顺序追加。否则坏 key 会被当成可用。
func TestMergeChannelKeysKeepsOrder(t *testing.T) {
	existing := "k1\nk2\nk3"
	got := mergeChannelKeys(existing, []string{"k4", "k5"})
	want := "k1\nk2\nk3\nk4\nk5"
	if got != want {
		t.Fatalf("合并后顺序不对\n got=%q\nwant=%q", got, want)
	}
}

func TestMergeChannelKeysDedupesWithoutReordering(t *testing.T) {
	existing := "k1\nk2"
	got := mergeChannelKeys(existing, []string{"k2", "k3", "k1"})
	want := "k1\nk2\nk3"
	if got != want {
		t.Fatalf("去重后顺序不对\n got=%q\nwant=%q", got, want)
	}
}

func TestMergeChannelKeysHandlesEmptyInput(t *testing.T) {
	if got := mergeChannelKeys("", nil); got != "" {
		t.Fatalf("空输入应返回空串，got=%q", got)
	}
	if got := mergeChannelKeys("", []string{" a ", "\n", "b"}); got != "a\nb" {
		t.Fatalf("应清理空白并丢弃空行，got=%q", got)
	}
}

func TestCountChannelKeys(t *testing.T) {
	cases := map[string]int{
		"":           0,
		"k1":         1,
		"k1\nk2":     2,
		"k1\n\nk2\n": 2,
	}
	for input, want := range cases {
		if got := countChannelKeys(input); got != want {
			t.Errorf("countChannelKeys(%q)=%d，期望 %d", input, got, want)
		}
	}
}

// 多 key 合并后必须能被识别为多密钥渠道，否则 GetNextEnabledKey 会把整串
// "k1\nk2" 当成一个密钥发给上游。
func TestMergedKeysAreMultiKey(t *testing.T) {
	merged := mergeChannelKeys("k1", []string{"k2"})
	if countChannelKeys(merged) <= 1 {
		t.Fatalf("两条 key 合并后应判定为多 key，got=%q", merged)
	}
	if strings.Contains(merged, "\r") {
		t.Fatalf("key 串里不应出现回车符")
	}
}
