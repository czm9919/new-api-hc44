package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
)

// hc26 回归：/api/user/self 的 DTO 必须携带 free_quota。
// 签到奖励写入 users.free_quota，但 buildSelfUserData 曾经漏掉该字段，
// 前端「免费额度」永远显示 0，看起来像「签到成功但免费额度不增加」。
func TestBuildSelfUserDataIncludesFreeQuota(t *testing.T) {
	user := &model.User{
		Id:        1,
		Username:  "alice",
		Quota:     100000000,
		FreeQuota: 15705,
	}

	data := buildSelfUserData(user)

	assert.Equal(t, 100000000, data["quota"])
	assert.Equal(t, 15705, data["free_quota"], "self DTO must expose free_quota so the dashboard can display it")
}
