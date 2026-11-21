package controller

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// B222 用户批量导入
// ---------------------------------------------------------------------------

// batchImportUserItem 单个导入项。字段与「创建用户」接口保持一致，
// 额外支持 group 与 quota_usd（初始赠送额度，按 USD 填写更直观）。
type batchImportUserItem struct {
	Username    string  `json:"username"`
	Password    string  `json:"password"`
	Email       string  `json:"email"`
	DisplayName string  `json:"display_name"`
	Group       string  `json:"group"`
	Role        int     `json:"role"`
	QuotaUSD    float64 `json:"quota_usd"`
}

type batchImportUsersRequest struct {
	Users  []batchImportUserItem `json:"users"`
	DryRun bool                  `json:"dry_run"`
}

type batchImportRowResult struct {
	Index    int    `json:"index"`
	Username string `json:"username"`
	Ok       bool   `json:"ok"`
	Message  string `json:"message"`
	UserId   int    `json:"user_id,omitempty"`
}

// validateBatchImportItem 校验单条导入项，返回可写入的用户对象。
// 与 CreateUser 保持同一套校验口径：用户名/密码必填、结构 tag 校验、
// 角色不得高于操作者、分组必须已配置。
func validateBatchImportItem(item batchImportUserItem, callerRole int) (*model.User, string) {
	username := strings.TrimSpace(item.Username)
	if username == "" {
		return nil, "username is required"
	}
	if strings.TrimSpace(item.Password) == "" {
		return nil, "password is required"
	}
	role := item.Role
	if role == 0 {
		role = common.RoleCommonUser
	}
	if !common.IsValidateRole(role) {
		return nil, "invalid role"
	}
	if role >= callerRole {
		return nil, "cannot import users with permission level equal to or higher than yourself"
	}
	displayName := strings.TrimSpace(item.DisplayName)
	if displayName == "" {
		displayName = username
	}
	group := strings.TrimSpace(item.Group)
	if group == "" {
		group = "default"
	}
	if _, ok := ratio_setting.GetGroupRatioCopy()[group]; !ok {
		return nil, fmt.Sprintf("group %q is not configured", group)
	}
	if item.QuotaUSD < 0 {
		return nil, "quota_usd must be non-negative"
	}
	user := &model.User{
		Username:    username,
		Password:    item.Password,
		DisplayName: displayName,
		Email:       strings.TrimSpace(item.Email),
		Role:        role,
		Group:       group,
	}
	if err := common.Validate.Struct(user); err != nil {
		return nil, err.Error()
	}
	return user, ""
}

// BatchImportUsers 管理员批量创建用户（B222）。
// 支持 dry_run 预检：只做校验并返回每行的结果，不写库。
// 每一行使用独立事务，单行失败不影响其他行；返回逐行成功/失败明细。
func BatchImportUsers(c *gin.Context) {
	var req batchImportUsersRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if len(req.Users) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	const maxBatchImportUsers = 500
	if len(req.Users) > maxBatchImportUsers {
		common.ApiError(c, fmt.Errorf("at most %d users per batch", maxBatchImportUsers))
		return
	}

	callerRole := c.GetInt("role")
	seen := make(map[string]struct{}, len(req.Users))
	results := make([]batchImportRowResult, 0, len(req.Users))
	created := 0

	for i, item := range req.Users {
		row := batchImportRowResult{Index: i, Username: strings.TrimSpace(item.Username)}
		user, reason := validateBatchImportItem(item, callerRole)
		if reason != "" {
			row.Message = reason
			results = append(results, row)
			continue
		}
		// 批内重名直接拒绝，避免逐行落库时才暴露唯一索引冲突。
		if _, dup := seen[user.Username]; dup {
			row.Message = "duplicated username inside this batch"
			results = append(results, row)
			continue
		}
		seen[user.Username] = struct{}{}

		if req.DryRun {
			row.Ok = true
			row.Message = "ready to create"
			results = append(results, row)
			continue
		}

		if err := user.Insert(0); err != nil {
			row.Message = err.Error()
			results = append(results, row)
			continue
		}
		if item.QuotaUSD > 0 {
			quota := int(item.QuotaUSD * common.QuotaPerUnit)
			if quota > 0 {
				if err := model.IncreaseUserQuota(user.Id, quota, true); err != nil {
					row.Message = fmt.Sprintf("user created but granting quota failed: %s", err.Error())
					row.Ok = true
					row.UserId = user.Id
					results = append(results, row)
					created++
					continue
				}
			}
		}
		row.Ok = true
		row.UserId = user.Id
		row.Message = "created"
		results = append(results, row)
		created++
	}

	failed := len(results) - created
	common.ApiSuccess(c, gin.H{
		"dry_run": req.DryRun,
		"total":   len(req.Users),
		"created": created,
		"failed":  failed,
		"results": results,
	})
}
