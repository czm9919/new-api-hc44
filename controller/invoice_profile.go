package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// B99 发票抬头管理（买家侧）
// ---------------------------------------------------------------------------

// GetUserInvoiceProfiles 列出本人全部抬头（默认在前）。
func GetUserInvoiceProfiles(c *gin.Context) {
	userId := c.GetInt("id")
	profiles, err := model.GetUserInvoiceProfiles(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, profiles)
}

// AddInvoiceProfile 新建本人抬头。
func AddInvoiceProfile(c *gin.Context) {
	var profile model.InvoiceProfile
	if err := common.DecodeJson(c.Request.Body, &profile); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	profile.Id = 0
	profile.UserId = c.GetInt("id")
	if err := model.InsertInvoiceProfile(&profile); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, profile)
}

// UpdateInvoiceProfile 修改本人抬头（路径参数 id）。
func UpdateInvoiceProfile(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var profile model.InvoiceProfile
	if err := common.DecodeJson(c.Request.Body, &profile); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	profile.Id = id
	if err := model.UpdateInvoiceProfile(c.GetInt("id"), &profile); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// DeleteInvoiceProfile 删除本人抬头；默认抬头被删后自动补位。
func DeleteInvoiceProfile(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := model.DeleteInvoiceProfile(c.GetInt("id"), id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
