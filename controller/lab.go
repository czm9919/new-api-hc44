/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// v1.1.0 Lab 自研模型展示页（/lab）：纯展示 + 求赞助，与主站路由、计费、
// abilities 完全隔离——该模型不进渠道表，任何请求都不会被路由到它。
//
// 配置持久化在 option `LabSetting`（JSON），管理员在「扩展设置 → 自研实验室」
// 卡片里填写。对外只暴露两个匿名只读接口：
//   - GET /api/lab/public/config  静态配置（模型名/时间线/赞助进度/Demo 地址）
//   - GET /api/lab/public/status  可选：代理推理服务器上的状态端点，避免 CORS
//
// 状态代理的上游地址仅管理员可配置（/api/option/ 需 AdminAuth），因此内网地址
// 是合法用法（推理服务常与主站同机/同局域网）；这里只校验 scheme。

const labSettingOptionKey = "LabSetting"

// labStatusCacheTTL 状态代理缓存窗口：状态端点被公开页轮询，缓存后既不压垮
// 自家服务器，也不让匿名请求成为放大攻击的跳板。
const labStatusCacheTTL = 5 * time.Second

// labStatusMaxBytes 上游状态响应体上限，防止异常端点撑爆转发。
const labStatusMaxBytes = 64 << 10

var (
	labStatusMu        sync.Mutex
	labStatusCacheAt   time.Time
	labStatusCacheBody []byte
	labStatusCacheErr  string
)

// GetLabPublicConfig 返回实验室展示配置。未配置或 JSON 损坏时返回空对象，
// 前端据此展示「暂未开放」，不会白屏。
func GetLabPublicConfig(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	raw := strings.TrimSpace(common.OptionMap[labSettingOptionKey])
	common.OptionMapRWMutex.RUnlock()

	if raw == "" {
		common.ApiSuccess(c, gin.H{"enabled": false})
		return
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil || cfg == nil {
		common.SysLog(common.LogText("lab setting is not valid json, serving disabled page: error=%v", err))
		common.ApiSuccess(c, gin.H{"enabled": false})
		return
	}
	// 显式 enabled=false 时才下线；配了内容默认视为开放。
	if _, ok := cfg["enabled"]; !ok {
		cfg["enabled"] = true
	}
	common.ApiSuccess(c, cfg)
}

// GetLabPublicStatus 代理推理服务器的状态端点，返回其 JSON 原文。
// 未配置 status_url 时返回 online=false 的占位，前端显示「状态不可用」。
func GetLabPublicStatus(c *gin.Context) {
	statusURL := labStatusURL()
	if statusURL == "" {
		common.ApiSuccess(c, gin.H{"online": false, "configured": false})
		return
	}

	labStatusMu.Lock()
	if time.Since(labStatusCacheAt) < labStatusCacheTTL {
		body, errMsg := labStatusCacheBody, labStatusCacheErr
		labStatusMu.Unlock()
		serveLabStatus(c, body, errMsg)
		return
	}
	labStatusMu.Unlock()

	body, err := fetchLabStatus(statusURL)

	labStatusMu.Lock()
	labStatusCacheAt = time.Now()
	if err != nil {
		labStatusCacheBody = nil
		labStatusCacheErr = err.Error()
	} else {
		labStatusCacheBody = body
		labStatusCacheErr = ""
	}
	labStatusMu.Unlock()

	if err != nil {
		common.ApiSuccess(c, gin.H{"online": false, "configured": true, "error": err.Error()})
		return
	}
	serveLabStatus(c, body, "")
}

// labStatusURL 从配置里取出状态端点地址（仅接受 http/https）。
func labStatusURL() string {
	common.OptionMapRWMutex.RLock()
	raw := strings.TrimSpace(common.OptionMap[labSettingOptionKey])
	common.OptionMapRWMutex.RUnlock()
	if raw == "" {
		return ""
	}
	var cfg struct {
		StatusURL string `json:"status_url"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return ""
	}
	target := strings.TrimSpace(cfg.StatusURL)
	if target == "" {
		return ""
	}
	parsed, err := url.Parse(target)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		common.SysLog(common.LogText("lab status_url is not a valid http(s) url: %s", target))
		return ""
	}
	return target
}

func fetchLabStatus(target string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, labStatusMaxBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lab status upstream returned %d", resp.StatusCode)
	}
	return body, nil
}

// serveLabStatus 把上游 JSON 原样透传（前端自行解析），保证未知字段不丢。
func serveLabStatus(c *gin.Context, body []byte, errMsg string) {
	if errMsg != "" || len(body) == 0 {
		common.ApiSuccess(c, gin.H{"online": false, "configured": true, "error": errMsg})
		return
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		common.ApiSuccess(c, gin.H{"online": false, "configured": true, "error": "upstream returned non-json"})
		return
	}
	common.ApiSuccess(c, gin.H{"online": true, "configured": true, "status": payload})
}
