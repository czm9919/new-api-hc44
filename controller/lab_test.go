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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lab 展示配置的公开接口行为：未配置/损坏 JSON 必须降级为
// enabled=false，前端展示「暂未开放」而不是白屏或 500。

// setLabOption 注入 LabSetting 测试值。测试进程里 common.OptionMap 可能尚未初始化，
// 这里负责建表并原样恢复。
func setLabOption(t *testing.T, value string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	original, existed := common.OptionMap[labSettingOptionKey]
	common.OptionMap[labSettingOptionKey] = value
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if existed {
			common.OptionMap[labSettingOptionKey] = original
		} else {
			delete(common.OptionMap, labSettingOptionKey)
		}
		common.OptionMapRWMutex.Unlock()
	})
}

func callLabPublicConfig(t *testing.T, optionValue string) map[string]any {
	t.Helper()
	setLabOption(t, optionValue)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/lab/public/config", nil)
	GetLabPublicConfig(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	return resp.Data
}

func TestLabPublicConfigEmptyOptionServesDisabledPage(t *testing.T) {
	data := callLabPublicConfig(t, "")
	assert.Equal(t, false, data["enabled"])
}

func TestLabPublicConfigInvalidJSONServesDisabledPage(t *testing.T) {
	data := callLabPublicConfig(t, "{not-json")
	assert.Equal(t, false, data["enabled"])
}

func TestLabPublicConfigValidJSONDefaultsEnabled(t *testing.T) {
	data := callLabPublicConfig(t,
		`{"model_name":"Delta-7B","tagline":"从零手写"}`)
	assert.Equal(t, true, data["enabled"])
	assert.Equal(t, "Delta-7B", data["model_name"])
}

func TestLabPublicConfigExplicitDisabledRespected(t *testing.T) {
	data := callLabPublicConfig(t,
		`{"model_name":"Delta-7B","enabled":false}`)
	assert.Equal(t, false, data["enabled"])
}

func TestLabStatusURLValidation(t *testing.T) {
	cases := []struct {
		raw    string
		expect string
	}{
		{"", ""},
		{`{"status_url":"http://127.0.0.1:8100/status"}`, "http://127.0.0.1:8100/status"},
		{`{"status_url":"https://gpu.example.com/status"}`, "https://gpu.example.com/status"},
		{`{"status_url":"file:///etc/passwd"}`, ""},
		{`{"status_url":"ftp://x"}`, ""},
		{`{not-json}`, ""},
	}
	for _, tc := range cases {
		setLabOption(t, tc.raw)
		assert.Equal(t, tc.expect, labStatusURL(), tc.raw)
	}
}
