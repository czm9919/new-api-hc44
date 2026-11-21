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
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// v1.1.0 M1 · B22 倍率变更 diff 预览：改倍率前算影响面（多少模型、
// 近 7 天少收/多收多少）。估算口径：假设近 7 天流量结构不变，按
// 「该维度消耗额度 × (新倍率-旧倍率)/旧倍率」线性外推。

const ratioDiffPreviewWindowDays = 7

type RatioDiffPreviewRequest struct {
	// model | completion | group
	RatioType string             `json:"ratio_type"`
	NewRatio  map[string]float64 `json:"new_ratio"`
}

type RatioDiffItem struct {
	Name          string  `json:"name"`
	OldRatio      float64 `json:"old_ratio"`
	NewRatio      float64 `json:"new_ratio"`
	UsedQuota     int64   `json:"used_quota"`
	PromptTokens  int64   `json:"prompt_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	EstDeltaQuota int64   `json:"estimated_delta_quota"`
}

type RatioDiffPreviewData struct {
	RatioType     string          `json:"ratio_type"`
	TotalEntries  int             `json:"total_entries"`
	ChangedModels int             `json:"changed_models"`
	Days          int             `json:"days"`
	BeforeQuota   int64           `json:"before_quota"`
	AfterQuota    int64           `json:"after_quota"`
	DeltaQuota    int64           `json:"delta_quota"`
	Items         []RatioDiffItem `json:"items"`
}

func loadRatioMap(ratioType string) (map[string]float64, error) {
	var jsonStr string
	switch ratioType {
	case "model":
		jsonStr = ratio_setting.ModelRatio2JSONString()
	case "completion":
		jsonStr = ratio_setting.CompletionRatio2JSONString()
	case "group":
		return ratio_setting.GetGroupRatioCopy(), nil
	default:
		return nil, fmt.Errorf("unsupported ratio_type %q", ratioType)
	}
	result := make(map[string]float64)
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse current %s ratio: %w", ratioType, err)
	}
	return result, nil
}

func PreviewRatioDiff(c *gin.Context) {
	var req RatioDiffPreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.RatioType == "" {
		req.RatioType = "model"
	}
	if req.NewRatio == nil {
		common.ApiError(c, fmt.Errorf("new_ratio is required"))
		return
	}

	current, err := loadRatioMap(req.RatioType)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	since := time.Now().AddDate(0, 0, -ratioDiffPreviewWindowDays).Unix()
	var usage []model.ModelUsageStat
	switch req.RatioType {
	case "group":
		usage, err = model.GetGroupUsageSince(since)
	default:
		usage, err = model.GetModelUsageSince(since)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	data := RatioDiffPreviewData{
		RatioType:    req.RatioType,
		TotalEntries: len(current),
		Days:         ratioDiffPreviewWindowDays,
		Items:        make([]RatioDiffItem, 0),
	}

	for _, row := range usage {
		oldRatio := current[row.Name]
		newRatio, changed := req.NewRatio[row.Name]
		if !changed || newRatio == oldRatio {
			continue
		}
		item := RatioDiffItem{
			Name:         row.Name,
			OldRatio:     oldRatio,
			NewRatio:     newRatio,
			UsedQuota:    row.UsedQuota,
			PromptTokens: row.PromptTokens,
			OutputTokens: row.CompletionTokens,
		}
		if oldRatio > 0 {
			item.EstDeltaQuota = int64(float64(row.UsedQuota) * (newRatio - oldRatio) / oldRatio)
		} else {
			// 旧倍率为 0：之前免费/漏计，新倍率下全部消耗都算增量收入。
			item.EstDeltaQuota = row.UsedQuota
		}
		data.ChangedModels++
		data.BeforeQuota += row.UsedQuota
		data.AfterQuota += row.UsedQuota + item.EstDeltaQuota
		data.Items = append(data.Items, item)
	}
	data.DeltaQuota = data.AfterQuota - data.BeforeQuota

	// 影响面按额度排序，预览弹窗直接展示最需要关注的条目。
	for i := 1; i < len(data.Items); i++ {
		for j := i; j > 0 && absInt64(data.Items[j].EstDeltaQuota) > absInt64(data.Items[j-1].EstDeltaQuota); j-- {
			data.Items[j], data.Items[j-1] = data.Items[j-1], data.Items[j]
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
