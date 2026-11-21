package main

import (
	"testing"
)

// B06：版本号优先级——ldflags 注入值 > embed 的 VERSION 文件 > 原值。
func TestResolveVersionPrefersInjected(t *testing.T) {
	cases := []struct {
		name     string
		injected string
		embedded string
		want     string
	}{
		{"injected wins", "v1.0.0-rc.51+hc38", "v1.0.0-rc.51+hc38", "v1.0.0-rc.51+hc38"},
		{"embedded used when ldflags missing", "v0.0.0", "v1.0.0-rc.51+hc38", "v1.0.0-rc.51+hc38"},
		{"embedded used when injected blank", "", "v1.0.0-rc.51+hc38", "v1.0.0-rc.51+hc38"},
		{"whitespace tolerated", "  v0.0.0  ", "\n v9.9.9 \n", "v9.9.9"},
		{"empty embedded keeps sentinel", "v0.0.0", "", "v0.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVersion(tc.injected, tc.embedded); got != tc.want {
				t.Fatalf("resolveVersion(%q, %q) = %q, want %q", tc.injected, tc.embedded, got, tc.want)
			}
		})
	}
}
