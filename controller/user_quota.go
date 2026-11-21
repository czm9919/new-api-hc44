package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func manageUserQuota(c *gin.Context, req ManageRequest) {
	action := "generic"
	params := model.AuditFields{
		"target_user_id":  req.Id,
		"mode":            req.Mode,
		"requested_quota": req.Value,
	}
	switch req.Mode {
	case "add":
		action = "user.quota_add"
	case "subtract":
		action = "user.quota_subtract"
	case "override":
		action = "user.quota_override"
	default:
		params["action"] = "add_quota"
		params["method"] = c.Request.Method
		params["route"] = c.FullPath()
	}
	success := false
	defer func() {
		content := auditContentEN(action, params)
		if !success {
			// Failed requests have no committed balance changes to render.
			content = "Failed user quota adjustment"
		}
		model.RecordOperationAuditLog(c.GetInt("id"), c.GetInt("role"), content, c.ClientIP(), action, params,
			auditOperatorInfo(c), &model.AuditRequestInfo{
				Method: c.Request.Method, Route: c.FullPath(), Status: c.Writer.Status(), Success: success,
			}, c)
		markAuditLogged(c)
	}()

	adjustment, err := model.AdjustUserQuota(req.Id, c.GetInt("role"), req.Mode, req.Value)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrInvalidUserQuotaAdjustment):
			params["failure_reason"] = "invalid_parameters"
			if (req.Mode == "add" || req.Mode == "subtract") && req.Value <= 0 {
				common.ApiErrorT(c, "Quota change amount cannot be zero")
			} else {
				common.ApiErrorT(c, "Invalid parameters")
			}
		case errors.Is(err, model.ErrUserQuotaPermission):
			params["failure_reason"] = "permission_denied"
			common.ApiErrorT(c, "No permission to update users of same or higher permission level")
		case errors.Is(err, gorm.ErrRecordNotFound):
			params["failure_reason"] = "target_not_found"
			common.ApiErrorT(c, "User does not exist")
		case errors.Is(err, model.ErrWalletQuotaLimitExceeded):
			params["failure_reason"] = "quota_limit_exceeded"
			common.ApiError(c, err)
		default:
			params["failure_reason"] = "database_error"
			common.ApiError(c, err)
		}
		return
	}

	params["target_username"] = adjustment.Username
	params["from"] = adjustment.Before
	params["to"] = adjustment.After
	if req.Mode != "override" {
		params["quota"] = req.Value
	}
	success = true
	operation := model.AuditOperation{Action: action, Params: params}
	model.RecordLogWithAdminInfo(adjustment.UserID, model.LogTypeTopup,
		auditContentEN(action, params), auditOperatorInfo(c), &operation, c)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// BatchGrantUserQuota 按用户 ID 批量发放额度（v1.1.0 M2 · B58）。
//
// 场景：活动补偿、故障赔付、给一批内测用户发额度。逐个走 AdjustUserQuota，
// 复用单用户的权限校验（不能调同级/上级）与额度上限校验；单个用户失败不
// 影响其余用户，最终返回逐人明细让运营知道谁没发上。
const batchGrantQuotaMaxUsers = 200

type BatchGrantQuotaRequest struct {
	UserIds []int  `json:"user_ids"`
	Quota   int    `json:"quota"`
	Note    string `json:"note"`
}

func BatchGrantUserQuota(c *gin.Context) {
	var req BatchGrantQuotaRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if len(req.UserIds) == 0 || len(req.UserIds) > batchGrantQuotaMaxUsers {
		common.ApiErrorT(c, "Invalid user count")
		return
	}
	if req.Quota <= 0 {
		common.ApiErrorT(c, "Quota change amount cannot be zero")
		return
	}
	// 去重，防止同一用户在一次请求里被发两份。
	seen := make(map[int]struct{}, len(req.UserIds))
	ids := make([]int, 0, len(req.UserIds))
	for _, id := range req.UserIds {
		if id > 0 {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}

	operatorId := c.GetInt("id")
	operatorRole := c.GetInt("role")
	granted := make([]int, 0, len(ids))
	failed := make([]gin.H, 0)
	for _, id := range ids {
		adjustment, err := model.AdjustUserQuota(id, operatorRole, "add", req.Quota)
		if err != nil {
			reason := "database_error"
			switch {
			case errors.Is(err, model.ErrInvalidUserQuotaAdjustment):
				reason = "invalid_parameters"
			case errors.Is(err, model.ErrUserQuotaPermission):
				reason = "permission_denied"
			case errors.Is(err, gorm.ErrRecordNotFound):
				reason = "target_not_found"
			case errors.Is(err, model.ErrWalletQuotaLimitExceeded):
				reason = "quota_limit_exceeded"
			}
			failed = append(failed, gin.H{"user_id": id, "reason": reason})
			continue
		}
		granted = append(granted, id)
		operation := model.AuditOperation{Action: "user.quota_add", Params: model.AuditFields{
			"target_user_id": adjustment.UserID,
			"quota":          req.Quota,
			"mode":           "add",
			"batch":          true,
			"note":           req.Note,
		}}
		model.RecordLogWithAdminInfo(adjustment.UserID, model.LogTypeTopup,
			auditContentEN("user.quota_add", operation.Params), auditOperatorInfo(c), &operation, c)
	}

	model.RecordOperationAuditLog(operatorId, operatorRole,
		auditContentEN("user.quota_batch_add", model.AuditFields{
			"requested": len(req.UserIds),
			"granted":   len(granted),
			"failed":    len(failed),
			"quota":     req.Quota,
			"note":      req.Note,
		}),
		c.ClientIP(), "user.quota_batch_add",
		model.AuditFields{"requested": len(req.UserIds), "granted": len(granted), "failed": len(failed), "quota": req.Quota},
		auditOperatorInfo(c), &model.AuditRequestInfo{
			Method: c.Request.Method, Route: c.FullPath(), Status: c.Writer.Status(), Success: len(failed) == 0,
		}, c)
	markAuditLogged(c)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"granted": granted,
			"failed":  failed,
		},
	})
}
