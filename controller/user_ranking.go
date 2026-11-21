package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// GetUserRankingLeaderboard GET /api/rankings/users?period=week&limit=20
//
// 用户消耗排行榜（hc47）：/rankings 页面上的「用户榜」区块。
//
// 与管理员接口 /api/user/consumption_rank 的分工：
//   - 这里给普通用户看，用户名一律打码，不暴露余额、累计消耗等运营信息；
//   - 登录用户额外拿到 self（名次 / 已用额度 / 距上一名差多少 / 超过多少人），
//     匿名访问只返回榜单，self 不下发。
//
// 周期取值 today / week / month / year，缺省 week；未知取值返回 400。
func GetUserRankingLeaderboard(c *gin.Context) {
	period := c.DefaultQuery("period", "week")
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 {
		limit = service.UserRankingDefaultLimit
	}
	if limit > service.UserRankingMaxLimit {
		limit = service.UserRankingMaxLimit
	}

	// HeaderNavModuleAuth 内部走 TryUserAuth，未登录时这里拿到 0。
	viewerId := c.GetInt("id")

	resp, err := service.GetUserRanking(period, limit, viewerId)
	if err != nil {
		if errors.Is(err, service.ErrUserRankingBadPeriod) {
			// 参数错误直接给 400，避免前端把「拼错 period」当成服务端故障。
			common.ApiErrorStatus(c, http.StatusBadRequest,
				errors.New("unsupported period, expected today/week/month/year"))
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, resp)
}
