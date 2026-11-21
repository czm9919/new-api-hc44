package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func formatNotifyType(channelId int, status int) string {
	return fmt.Sprintf("%s_%d_%d", dto.NotifyTypeChannelUpdate, channelId, status)
}

func shouldCloseActiveWebSocketsAfterDisable(channelId int) (bool, *model.Channel) {
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		common.SysLog(common.LogText("failed to check channel status before closing active websockets: channel_id=%d, error=%v", channelId, err))
		return true, nil
	}
	return channel.Status != common.ChannelStatusEnabled, channel
}

// disable & notify
func DisableChannel(channelError types.ChannelError, reason string) {
	common.SysLog(common.LogText("channel %q (#%d) failed, disabling it, reason: %s", channelError.ChannelName, channelError.ChannelId, common.LocalLogPreview(reason)))

	// 检查是否启用自动禁用功能
	if !channelError.AutoBan {
		common.SysLog(common.LogText("channel %q (#%d) has automatic disabling turned off, skipping", channelError.ChannelName, channelError.ChannelId))
		return
	}

	success := model.UpdateChannelStatus(channelError.ChannelId, channelError.UsingKey, common.ChannelStatusAutoDisabled, reason)
	if success {
		closeWebSockets, channel := shouldCloseActiveWebSocketsAfterDisable(channelError.ChannelId)
		if closeWebSockets {
			CloseActiveWebSocketsForChannel(channelError.ChannelId, ChannelDisabledCloseReason)
		}
		params := map[string]any{"Name": channelError.ChannelName, "Id": channelError.ChannelId, "Reason": reason}
		NotifyChannelWatchers(formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), func(lang string) (string, string) {
			return i18n.Translate(lang, i18n.MsgChannelNotifyDisabledSubject, params), i18n.Translate(lang, i18n.MsgChannelNotifyDisabledContent, params)
		})
		if common.IsMasterNode && channel != nil {
			scheduledPlugins.disabled(channel, channelError.UsingKey, time.Now())
		}
	}
}

func EnableChannel(channelId int, usingKey string, channelName string) {
	success := model.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusEnabled, "")
	if success {
		params := map[string]any{"Name": channelName, "Id": channelId}
		NotifyChannelWatchers(formatNotifyType(channelId, common.ChannelStatusEnabled), func(lang string) (string, string) {
			return i18n.Translate(lang, i18n.MsgChannelNotifyEnabledSubject, params), i18n.Translate(lang, i18n.MsgChannelNotifyEnabledContent, params)
		})
	}
}

func ShouldDisableChannel(err *types.NewAPIError) bool {
	if !common.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if operation_setting.ShouldDisableByStatusCode(err.StatusCode) {
		return true
	}

	// Match keywords against both the parsed error message and the raw
	// upstream body: many providers answer 429/5xx with plain text or HTML
	// bodies whose text never reaches err.Error(), which previously made
	// keyword-based auto-disable silently miss those channels.
	lowerMessage := strings.ToLower(err.Error())
	if rawBody := err.GetRawUpstreamBody(); rawBody != "" {
		lowerMessage += "\n" + strings.ToLower(rawBody)
	}
	search, _ := AcSearch(lowerMessage, operation_setting.AutomaticDisableKeywords, true)
	return search
}

func ShouldEnableChannel(newAPIError *types.NewAPIError, status int) bool {
	if !common.AutomaticEnableChannelEnabled {
		return false
	}
	if newAPIError != nil {
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		return false
	}
	return true
}

// modelDelistKeywords 上游明确表示「该模型不存在 / 未授权 / 不支持」的关键词。
// 只匹配模型级错误，用于单模型自动下架判定；命中即认为这个模型在这个渠道上不可用。
var modelDelistKeywords = []string{
	"model_not_found",
	"does not exist",
	"unknown model",
	"no such model",
	"invalid model",
	"not authorized for model",
	"does not have access to model",
	"no permission for model",
	"not entitled to model",
	"unsupported model",
	"model not found",
}

// ShouldDelistModel 判断单模型巡检失败时是否应该把这个模型下架（禁用 ability）。
// 只有「模型级」的确定性错误才下架：
//   - 上游 404：模型不存在；
//   - 4xx 响应体里明确写了模型不存在/未授权的关键词。
//
// 401（渠道鉴权）、429（限流）、5xx（上游故障）、超时等瞬态或渠道级错误一律不下架，
// 交由渠道级自动禁用/重试机制处理，避免抖动把好模型误杀。
func ShouldDelistModel(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	sc := err.StatusCode
	if sc == 404 {
		// 上游明确 404：模型不存在，直接下架。
		return true
	}
	if sc == 0 || sc == 401 || sc == 429 || sc >= 500 {
		// 0=连接/本地错误、401=渠道鉴权、429=限流、5xx=上游故障：
		// 都是瞬态或渠道级问题，不下架，交由渠道级机制处理。
		return false
	}
	// 其余 4xx（400/403/422 等）按关键词判定是否模型级错误；
	// 403 可能携带 "not authorized for model"，属于模型级未授权。
	lowerMessage := strings.ToLower(err.Error())
	if rawBody := err.GetRawUpstreamBody(); rawBody != "" {
		lowerMessage += "\n" + strings.ToLower(rawBody)
	}
	for _, kw := range modelDelistKeywords {
		if strings.Contains(lowerMessage, kw) {
			return true
		}
	}
	return false
}
