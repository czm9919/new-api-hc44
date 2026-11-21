package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// B64 模型公告位（下架/停服/维护通知）
// ---------------------------------------------------------------------------

// GetActiveModelNotices 公开接口：按查询参数 ?model_name= 返回当前生效的公告
// （不传只返回全站公告）。匿名可读，供价目页/控制台顶栏展示。
func GetActiveModelNotices(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	notices, err := model.GetActiveModelNotices(c.Query("model_name"), limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, notices)
}

// GetAllModelNotices 后台列表：包含未生效与已过期公告。
func GetAllModelNotices(c *gin.Context) {
	notices, err := model.GetAllModelNotices()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, notices)
}

// AddModelNotice 后台新建公告。
func AddModelNotice(c *gin.Context) {
	var notice model.ModelNotice
	if err := common.DecodeJson(c.Request.Body, &notice); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	notice.Id = 0
	if err := model.InsertModelNotice(&notice); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, notice)
}

// UpdateModelNotice 后台修改公告。
func UpdateModelNotice(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var notice model.ModelNotice
	if err := common.DecodeJson(c.Request.Body, &notice); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	notice.Id = id
	if err := model.UpdateModelNotice(&notice); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// DeleteModelNotice 后台删除公告。
func DeleteModelNotice(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := model.DeleteModelNotice(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
