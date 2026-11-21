package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertNoticeForTest(t *testing.T, mutate func(*ModelNotice)) *ModelNotice {
	t.Helper()
	notice := &ModelNotice{
		ModelName: "gpt-4o",
		Level:     ModelNoticeLevelWarning,
		Title:     "测试公告",
		Content:   "内容",
		Enabled:   true,
	}
	if mutate != nil {
		mutate(notice)
	}
	require.NoError(t, InsertModelNotice(notice))
	return notice
}

func TestModelNoticeValidation(t *testing.T) {
	truncateTables(t)

	// 空标题拒绝
	require.Error(t, InsertModelNotice(&ModelNotice{Level: ModelNoticeLevelInfo}))
	// 非法级别拒绝
	require.Error(t, InsertModelNotice(&ModelNotice{Title: "x", Level: "urgent"}))
	// 时间窗倒置拒绝
	now := common.GetTimestamp()
	require.Error(t, InsertModelNotice(&ModelNotice{
		Title: "x", Level: ModelNoticeLevelInfo, StartTime: now, EndTime: now - 10,
	}))
	// 空级别默认 info
	notice := &ModelNotice{Title: "默认级别"}
	require.NoError(t, InsertModelNotice(notice))
	assert.Equal(t, ModelNoticeLevelInfo, notice.Level)
}

func TestGetActiveModelNoticesWindowAndModelFilter(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()

	// 未生效（开始时间在未来）→ 不出现
	insertNoticeForTest(t, func(n *ModelNotice) {
		n.Title = "未来公告"
		n.StartTime = now + 3600
	})
	// 已过期 → 不出现
	insertNoticeForTest(t, func(n *ModelNotice) {
		n.Title = "过期公告"
		n.EndTime = now - 10
	})
	// 禁用 → 不出现
	insertNoticeForTest(t, func(n *ModelNotice) {
		n.Title = "禁用公告"
		n.Enabled = false
	})
	// 全站公告
	insertNoticeForTest(t, func(n *ModelNotice) {
		n.Title = "全站公告"
		n.ModelName = ""
	})
	// 精确命中 gpt-4o
	insertNoticeForTest(t, func(n *ModelNotice) {
		n.Title = "模型公告"
		n.ModelName = "gpt-4o"
	})
	// 通配 openai/*
	insertNoticeForTest(t, func(n *ModelNotice) {
		n.Title = "厂商公告"
		n.ModelName = "openai/*"
	})
	// 其他模型
	insertNoticeForTest(t, func(n *ModelNotice) {
		n.Title = "无关公告"
		n.ModelName = "claude-3"
	})

	// 匿名全站公告：只取 model_name = ''
	site, err := GetActiveModelNotices("", 0)
	require.NoError(t, err)
	require.Len(t, site, 1)
	assert.Equal(t, "全站公告", site[0].Title)

	// 精确命中：全站公告 + 该模型公告（厂商通配不参与，因为名字里没有 /）
	exact, err := GetActiveModelNotices("gpt-4o", 0)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"全站公告", "模型公告"}, noticeTitles(exact))

	// 带厂商前缀：全站公告 + 厂商通配公告（openai/*）
	vendor, err := GetActiveModelNotices("openai/gpt-4o", 0)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"全站公告", "厂商公告"}, noticeTitles(vendor))

	// 其他模型只拿到自己的公告 + 全站公告
	other, err := GetActiveModelNotices("claude-3", 0)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"全站公告", "无关公告"}, noticeTitles(other))

	// 完全没配置的模型只拿到全站公告
	unknown, err := GetActiveModelNotices("brand-new-model", 0)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"全站公告"}, noticeTitles(unknown))
}

func noticeTitles(notices []*ModelNotice) []string {
	titles := make([]string, 0, len(notices))
	for _, n := range notices {
		titles = append(titles, n.Title)
	}
	return titles
}

func TestFormatWildcardNoticeModel(t *testing.T) {
	assert.Equal(t, "openai/*", FormatWildcardNoticeModel("openai/gpt-4o"))
	assert.Equal(t, "gpt-4o", FormatWildcardNoticeModel("gpt-4o"))
	assert.Equal(t, "", FormatWildcardNoticeModel(""))
}

func TestUpdateModelNoticeRejectsMissing(t *testing.T) {
	truncateTables(t)

	notice := insertNoticeForTest(t, nil)
	notice.Title = "改后标题"
	require.NoError(t, UpdateModelNotice(notice))

	all, err := GetAllModelNotices()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "改后标题", all[0].Title)

	// Id 不存在必须失败
	missing := *notice
	missing.Id = notice.Id + 999
	require.Error(t, UpdateModelNotice(&missing))
}

func TestDeleteModelNotice(t *testing.T) {
	truncateTables(t)

	notice := insertNoticeForTest(t, nil)
	require.NoError(t, DeleteModelNotice(notice.Id))
	require.Error(t, DeleteModelNotice(notice.Id)) // 二次删除报不存在

	all, err := GetAllModelNotices()
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestGetActiveModelNoticesRespectsLimit(t *testing.T) {
	truncateTables(t)

	for i := 0; i < 5; i++ {
		insertNoticeForTest(t, func(n *ModelNotice) {
			n.Title = "全站公告"
			n.ModelName = ""
			n.CreateTime = common.GetTimestamp() + int64(i) // 保证顺序
		})
	}
	// 强制让 CreateTime 递增（Insert 覆盖为同一秒）
	for i, n := range mustAllSiteNotices(t) {
		require.NoError(t, DB.Model(&ModelNotice{}).Where("id = ?", n.Id).
			Update("create_time", time.Now().Unix()+int64(i)).Error)
	}

	notices, err := GetActiveModelNotices("", 3)
	require.NoError(t, err)
	require.Len(t, notices, 3)
}

func mustAllSiteNotices(t *testing.T) []*ModelNotice {
	t.Helper()
	notices, err := GetAllModelNotices()
	require.NoError(t, err)
	return notices
}
