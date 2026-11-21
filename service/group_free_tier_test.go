package service

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// hc20 免费/付费 key 隔离：免费级 key（号池发放）的 auto 组只能在
// 免费分组白名单（FreeQuotaAllowedGroups）内展开。
func TestFilterGroupsForFreeTierToken(t *testing.T) {
	original := model.GetFreeQuotaAllowedGroups()
	defer model.SetFreeQuotaAllowedGroups(original)

	model.SetFreeQuotaAllowedGroups("免费分组,公益模型组")

	// 免费级 key：auto 组过滤到白名单内（保序）
	c := gin.Context{}
	common.SetContextKey(&c, constant.ContextKeyTokenFreeGroupRestricted, true)
	got := filterGroupsForFreeTierToken(&c, []string{"default", "免费分组", "paid", "公益模型组"})
	assert.Equal(t, []string{"免费分组", "公益模型组"}, got)

	// 白名单内的组全部不在候选里 → 空（请求将无可用渠道，符合隔离语义）
	got = filterGroupsForFreeTierToken(&c, []string{"default", "paid"})
	assert.Empty(t, got)

	// 非免费 key：不过滤
	c2 := gin.Context{}
	got = filterGroupsForFreeTierToken(&c2, []string{"default", "paid"})
	assert.Equal(t, []string{"default", "paid"}, got)

	// 标记为非 bool / false：不过滤
	c3 := gin.Context{}
	common.SetContextKey(&c3, constant.ContextKeyTokenFreeGroupRestricted, false)
	got = filterGroupsForFreeTierToken(&c3, []string{"default"})
	assert.Equal(t, []string{"default"}, got)

	// 白名单为空 = 不限制免费 key
	model.SetFreeQuotaAllowedGroups("")
	c4 := gin.Context{}
	common.SetContextKey(&c4, constant.ContextKeyTokenFreeGroupRestricted, true)
	got = filterGroupsForFreeTierToken(&c4, []string{"default", "paid"})
	assert.Equal(t, []string{"default", "paid"}, got)
}

// IsFreeTier：号池 token（SourceChannelId>0）= 免费级 key。
func TestTokenIsFreeTier(t *testing.T) {
	assert.False(t, (*model.Token)(nil).IsFreeTier())
	assert.False(t, (&model.Token{}).IsFreeTier(), "普通付费 key 不是免费级")
	assert.True(t, (&model.Token{SourceChannelId: 3}).IsFreeTier(), "号池发放的 key 是免费级")
}
