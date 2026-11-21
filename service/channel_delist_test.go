package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
)

func newModelCheckError(status int, msg string) *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeBadResponseStatusCode, status)
}

// 404 明确表示模型不存在 → 下架。
func TestShouldDelistModel404(t *testing.T) {
	assert.True(t, ShouldDelistModel(newModelCheckError(404, "model not found")))
}

// 响应体/错误信息里明确写了模型不存在或未授权 → 下架。
func TestShouldDelistModelKeywords(t *testing.T) {
	cases := []struct {
		status int
		msg    string
	}{
		{400, `{"error":{"code":"model_not_found","message":"The model 'gpt-9x' does not exist"}}`},
		{403, "you are not authorized for model nvidia/nemotron-70b"},
		{422, "unknown model foo"},
		{400, "no permission for model bar"},
	}
	for _, c := range cases {
		assert.True(t, ShouldDelistModel(newModelCheckError(c.status, c.msg)), "status=%d msg=%s", c.status, c.msg)
	}
}

// 瞬态/渠道级错误绝不能把模型下架。
func TestShouldDelistModelTransientKeep(t *testing.T) {
	cases := []struct {
		status int
		msg    string
	}{
		{0, "connection refused"},
		{401, "invalid api key"},
		{429, "rate limit exceeded"},
		{500, "internal server error"},
		{503, "upstream overloaded"},
		{400, "context length exceeded"}, // 非模型级 4xx，无关键词命中
	}
	for _, c := range cases {
		assert.False(t, ShouldDelistModel(newModelCheckError(c.status, c.msg)), "status=%d msg=%s", c.status, c.msg)
	}
	assert.False(t, ShouldDelistModel(nil))
}
