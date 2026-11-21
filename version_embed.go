package main

import (
	_ "embed"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// 版本号注入兜底（v1.1.0 M1 · B06）。
//
// 正式镜像始终用 `-ldflags -X ...common.Version=$ver` 注入（Dockerfile 从
// VERSION 文件读取，也支持 --build-arg APP_VERSION 覆盖）。但本地
// `go build` / `go run` / 二次编译二进制不带 ldflags 时，common.Version
// 会一直是常量默认值 v0.0.0，对外（/api/status、启动日志、版本检查）就
// 报不出真实版本。
//
// 这里把仓库根的 VERSION 文件在编译期 embed 进二进制作为兜底：只有
// ldflags 没注入（值为空或仍是默认 v0.0.0）时才回落到文件内容，注入值
// 优先，避免两条来源不一致时以文件为准。
//
//go:embed VERSION
var embeddedVersionFile string

// defaultCommonVersion 与 common/constants.go 里的常量默认值保持一致。
const defaultCommonVersion = "v0.0.0"

// applyEmbeddedVersion 把 embed 的 VERSION 内容写进 common.Version。
// 抽成纯函数 resolveVersion 以便单测覆盖优先级规则。
func applyEmbeddedVersion() {
	common.Version = resolveVersion(common.Version, embeddedVersionFile)
}

func resolveVersion(injected, embedded string) string {
	current := strings.TrimSpace(injected)
	if current != "" && current != defaultCommonVersion {
		return current
	}
	if fallback := strings.TrimSpace(embedded); fallback != "" {
		return fallback
	}
	return current
}
