package controller

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// B63 邀请排行榜
// ---------------------------------------------------------------------------

// maskUsername 排行榜对外展示用的用户名脱敏：
// 保留前 2 个字符（不足则全保留）+ "****" + 最后 1 个字符，防止完整用户名泄露。
// 输入可能包含中文/emoji，按 rune 处理。
func maskUsername(username string) string {
	name := strings.TrimSpace(username)
	if name == "" {
		return "***"
	}
	runes := []rune(name)
	if len(runes) <= 3 {
		// 短名字全部保留首字符 + 星号，避免几乎完整泄露。
		return string(runes[0]) + "****"
	}
	return string(runes[:2]) + "****" + string(runes[len(runes)-1:])
}

// GetAffLeaderboard 全站邀请排行榜（B63）：
// 登录用户可见；用户名脱敏；默认 10 条，上限 50 条。
func GetAffLeaderboard(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	entries, err := model.GetAffLeaderboard(limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	type publicEntry struct {
		Rank     int    `json:"rank"`
		UserId   int    `json:"user_id"`
		Username string `json:"username"`
		AffCount int    `json:"aff_count"`
	}
	list := make([]publicEntry, 0, len(entries))
	for i, entry := range entries {
		list = append(list, publicEntry{
			Rank:     i + 1,
			UserId:   entry.UserId,
			Username: maskUsername(entry.Username),
			AffCount: entry.AffCount,
		})
	}
	userId := c.GetInt("id")
	rank, total, affCount, err := model.GetUserAffRank(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"leaderboard": list,
		"self": gin.H{
			"rank":      rank,
			"total":     total,
			"aff_count": affCount,
		},
	})
}
