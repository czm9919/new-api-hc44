package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type TokenDetails struct {
	TextTokens  int
	AudioTokens int
}

type QuotaInfo struct {
	InputDetails  TokenDetails
	OutputDetails TokenDetails
	ModelName     string
	UsePrice      bool
	ModelPrice    float64
	ModelRatio    float64
	GroupRatio    float64
}

func hasCustomModelRatio(modelName string, currentRatio float64) bool {
	defaultRatio, exists := ratio_setting.GetDefaultModelRatioMap()[modelName]
	if !exists {
		return true
	}
	return currentRatio != defaultRatio
}

func calculateAudioQuota(info QuotaInfo) (int, *common.QuotaClamp) {
	if info.UsePrice {
		modelPrice := decimal.NewFromFloat(info.ModelPrice)
		quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		groupRatio := decimal.NewFromFloat(info.GroupRatio)

		quota := modelPrice.Mul(quotaPerUnit).Mul(groupRatio)
		return common.QuotaFromDecimalChecked(quota)
	}

	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(info.ModelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(info.ModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(info.ModelName))

	groupRatio := decimal.NewFromFloat(info.GroupRatio)
	modelRatio := decimal.NewFromFloat(info.ModelRatio)
	ratio := groupRatio.Mul(modelRatio)

	inputTextTokens := decimal.NewFromInt(int64(info.InputDetails.TextTokens))
	outputTextTokens := decimal.NewFromInt(int64(info.OutputDetails.TextTokens))
	inputAudioTokens := decimal.NewFromInt(int64(info.InputDetails.AudioTokens))
	outputAudioTokens := decimal.NewFromInt(int64(info.OutputDetails.AudioTokens))

	quota := decimal.Zero
	quota = quota.Add(inputTextTokens)
	quota = quota.Add(outputTextTokens.Mul(completionRatio))
	quota = quota.Add(inputAudioTokens.Mul(audioRatio))
	quota = quota.Add(outputAudioTokens.Mul(audioRatio).Mul(audioCompletionRatio))

	quota = quota.Mul(ratio)

	// If ratio is not zero and quota is less than or equal to zero, set quota to 1
	if !ratio.IsZero() && quota.LessThanOrEqual(decimal.Zero) {
		quota = decimal.NewFromInt(1)
	}

	return common.QuotaFromDecimalChecked(quota)
}

// PreWssConsumeQuota charges the usage of one Realtime response and returns
// the quota it charged.
func PreWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.RealtimeUsage) (int, error) {
	if relayInfo.UsePrice {
		return 0, nil
	}
	userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
	if err != nil {
		return 0, err
	}

	token, err := model.GetTokenByKey(strings.TrimPrefix(relayInfo.TokenKey, "sk-"), false)
	if err != nil {
		return 0, err
	}

	modelName := relayInfo.OriginModelName
	textInputTokens := usage.InputTokenDetails.TextTokens
	textOutTokens := usage.OutputTokenDetails.TextTokens
	audioInputTokens := usage.InputTokenDetails.AudioTokens
	audioOutTokens := usage.OutputTokenDetails.AudioTokens
	groupRatio := ratio_setting.GetGroupRatio(relayInfo.UsingGroup)
	modelRatio, _, _ := ratio_setting.GetModelRatio(modelName)

	autoGroup, exists := common.GetContextKey(ctx, constant.ContextKeyAutoGroup)
	if exists {
		groupRatio = ratio_setting.GetGroupRatio(autoGroup.(string))
		logger.LogDebug(ctx, "final group ratio: %f", groupRatio)
		relayInfo.UsingGroup = autoGroup.(string)
	}

	actualGroupRatio := groupRatio
	userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(relayInfo.UserGroup, relayInfo.UsingGroup)
	if ok {
		actualGroupRatio = userGroupRatio
	}

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  modelName,
		UsePrice:   relayInfo.UsePrice,
		ModelRatio: modelRatio,
		GroupRatio: actualGroupRatio,
	}

	quota, clamp := calculateAudioQuota(quotaInfo)
	noteQuotaClamp(relayInfo, clamp)

	if userQuota < quota {
		return 0, fmt.Errorf("user quota is not enough, user quota: %s, need quota: %s", logger.FormatQuota(userQuota), logger.FormatQuota(quota))
	}

	if !token.UnlimitedQuota && token.RemainQuota < quota {
		return 0, fmt.Errorf("token quota is not enough, token remain quota: %s, need quota: %s", logger.FormatQuota(token.RemainQuota), logger.FormatQuota(quota))
	}

	err = PostConsumeQuota(relayInfo, quota, 0, false)
	if err != nil {
		return 0, err
	}
	logger.LogInfo(ctx, common.LogText("realtime streaming consume quota success, quota: %d", quota))
	return quota, nil
}

func PostWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelName string,
	usage *dto.RealtimeUsage, extraContent []*common.Message) {

	var tieredResult *billingexpr.TieredResult
	tieredOk, tieredQuota, tieredRes := TryTieredSettle(relayInfo, billingexpr.TokenParams{
		P:   float64(usage.InputTokens),
		C:   float64(usage.OutputTokens),
		Len: float64(usage.InputTokens),
	})
	if tieredOk {
		tieredResult = tieredRes
	}

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	textInputTokens := usage.InputTokenDetails.TextTokens
	textOutTokens := usage.OutputTokenDetails.TextTokens

	audioInputTokens := usage.InputTokenDetails.AudioTokens
	audioOutTokens := usage.OutputTokenDetails.AudioTokens

	tokenName := ctx.GetString("token_name")
	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(modelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(relayInfo.OriginModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(modelName))

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  modelName,
		UsePrice:   usePrice,
		ModelRatio: modelRatio,
		GroupRatio: groupRatio,
	}

	quota, clamp := calculateAudioQuota(quotaInfo)
	noteQuotaClamp(relayInfo, clamp)
	if tieredOk {
		quota = tieredQuota
	}

	totalTokens := usage.TotalTokens
	var logContent []*common.Message
	if !usePrice {
		logContent = append(logContent, common.NewMessage("Model ratio {{model_ratio}}, completion ratio {{completion_ratio}}, audio ratio {{audio_ratio}}, audio completion ratio {{audio_completion_ratio}}, group ratio {{group_ratio}}", map[string]any{
			"model_ratio":            fmt.Sprintf("%.2f", modelRatio),
			"completion_ratio":       fmt.Sprintf("%.2f", completionRatio.InexactFloat64()),
			"audio_ratio":            fmt.Sprintf("%.2f", audioRatio.InexactFloat64()),
			"audio_completion_ratio": fmt.Sprintf("%.2f", audioCompletionRatio.InexactFloat64()),
			"group_ratio":            fmt.Sprintf("%.2f", groupRatio),
		}))
	} else {
		logContent = append(logContent, common.NewMessage("Model price {{model_price}}, group ratio {{group_ratio}}", map[string]any{
			"model_price": fmt.Sprintf("%.2f", modelPrice),
			"group_ratio": fmt.Sprintf("%.2f", groupRatio),
		}))
	}

	// record all the consume log even if quota is 0
	if totalTokens == 0 {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent = append(logContent, common.NewMessage("Possibly an upstream timeout"))
		logger.LogError(ctx, common.LogText("total tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d",
			relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, relayInfo.FinalPreConsumedQuota))
	} else {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
	}

	if err := SettleBilling(ctx, relayInfo, quota); err != nil {
		logger.LogError(ctx, common.LogText("error settling billing: %s", err.Error()))
	}

	logModel := modelName
	logContent = append(logContent, extraContent...)
	logContent = appendFundingSource(relayInfo, logContent)
	other := GenerateWssOtherInfo(ctx, relayInfo, usage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	if tieredResult != nil {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	attachQuotaSaturation(ctx, relayInfo, other)
	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
}

func CalcOpenRouterCacheCreateTokens(usage dto.Usage, priceData types.PriceData) int {
	if priceData.CacheCreationRatio == 1 {
		return 0
	}
	quotaPrice := priceData.ModelRatio / common.QuotaPerUnit
	promptCacheCreatePrice := quotaPrice * priceData.CacheCreationRatio
	promptCacheReadPrice := quotaPrice * priceData.CacheRatio
	completionPrice := quotaPrice * priceData.CompletionRatio

	cost, _ := usage.Cost.(float64)
	totalPromptTokens := float64(usage.PromptTokens)
	completionTokens := float64(usage.CompletionTokens)
	promptCacheReadTokens := float64(usage.PromptTokensDetails.CachedTokens)

	value := (cost -
		totalPromptTokens*quotaPrice +
		promptCacheReadTokens*(quotaPrice-promptCacheReadPrice) -
		completionTokens*completionPrice) /
		(promptCacheCreatePrice - quotaPrice)
	quota, clamp := common.QuotaRoundChecked(value)
	if clamp != nil {
		return -1
	}
	return quota
}

func PostAudioConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent []*common.Message) {
	if usage == nil {
		usage = &dto.Usage{PromptTokens: relayInfo.GetEstimatePromptTokens(), TotalTokens: relayInfo.GetEstimatePromptTokens()}
	}
	// 上游 usage 兜底估算（v1.1.0 M1 · B82）：与文本路径同构。
	usage, usageFallbackApplied := applyUsageFallback(relayInfo, effectiveBillingUsage(usage))
	if usageFallbackApplied {
		extraContent = append(extraContent, common.NewMessage("Upstream returned no token counts, billed an estimated prompt token count"))
	}

	var tieredUsedVars map[string]bool
	if snap := relayInfo.TieredBillingSnapshot; snap != nil {
		tieredUsedVars = billingexpr.UsedVarsByHash(snap.ExprString, snap.ExprHash)
	}
	var tieredResult *billingexpr.TieredResult
	tieredOk, tieredQuota, tieredRes := TryTieredSettle(relayInfo, BuildTieredTokenParams(usage, false, tieredUsedVars))
	if tieredOk {
		tieredResult = tieredRes
	}
	fixedPriceBilling := tieredOk && isFixedPriceSettlement(relayInfo, tieredRes)

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	textInputTokens := usage.PromptTokensDetails.TextTokens
	textOutTokens := usage.CompletionTokenDetails.TextTokens

	audioInputTokens := usage.PromptTokensDetails.AudioTokens
	audioOutTokens := usage.CompletionTokenDetails.AudioTokens

	tokenName := ctx.GetString("token_name")
	billingModelName := relayInfo.GetBillingModelName()
	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(billingModelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(billingModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(billingModelName))

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  billingModelName,
		UsePrice:   usePrice,
		ModelRatio: modelRatio,
		GroupRatio: groupRatio,
	}

	quota, clamp := calculateAudioQuota(quotaInfo)
	noteQuotaClamp(relayInfo, clamp)
	if tieredOk {
		quota = tieredQuota
	}

	totalTokens := usage.TotalTokens
	var logContent []*common.Message
	if !usePrice {
		logContent = append(logContent, common.NewMessage("Model ratio {{model_ratio}}, completion ratio {{completion_ratio}}, audio ratio {{audio_ratio}}, audio completion ratio {{audio_completion_ratio}}, group ratio {{group_ratio}}", map[string]any{
			"model_ratio":            fmt.Sprintf("%.2f", modelRatio),
			"completion_ratio":       fmt.Sprintf("%.2f", completionRatio.InexactFloat64()),
			"audio_ratio":            fmt.Sprintf("%.2f", audioRatio.InexactFloat64()),
			"audio_completion_ratio": fmt.Sprintf("%.2f", audioCompletionRatio.InexactFloat64()),
			"group_ratio":            fmt.Sprintf("%.2f", groupRatio),
		}))
	} else {
		logContent = append(logContent, common.NewMessage("Model price {{model_price}}, group ratio {{group_ratio}}", map[string]any{
			"model_price": fmt.Sprintf("%.2f", modelPrice),
			"group_ratio": fmt.Sprintf("%.2f", groupRatio),
		}))
	}

	// record all the consume log even if quota is 0
	if totalTokens == 0 && !fixedPriceBilling {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent = append(logContent, common.NewMessage("Possibly an upstream timeout"))
		logger.LogError(ctx, common.LogText("total tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d",
			relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, billingModelName, relayInfo.FinalPreConsumedQuota))
	} else {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
	}

	// 计费异常告警（v1.1.0 M1 · B71）：与文本路径同构。
	checkBillingAnomalies(ctx, billingAnomalyInput{
		UserId:            relayInfo.UserId,
		ChannelId:         relayInfo.ChannelId,
		TokenId:           relayInfo.TokenId,
		ModelName:         billingModelName,
		Quota:             quota,
		PreConsumedQuota:  relayInfo.FinalPreConsumedQuota,
		Billable:          totalTokens != 0 || fixedPriceBilling,
		UsePrice:          usePrice,
		FixedPriceBilling: fixedPriceBilling,
		ModelRatio:        modelRatio,
		GroupRatio:        groupRatio,
	})

	if err := SettleBilling(ctx, relayInfo, quota); err != nil {
		logger.LogError(ctx, common.LogText("error settling billing: %s", err.Error()))
	}

	logModel := billingModelName
	logContent = append(logContent, extraContent...)
	logContent = appendFundingSource(relayInfo, logContent)
	other := GenerateAudioOtherInfo(ctx, relayInfo, usage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	if usageFallbackApplied {
		reportUsageFallback(ctx, other, usage.PromptTokens)
	}
	if tieredResult != nil {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	attachQuotaSaturation(ctx, relayInfo, other)
	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
	relayInfo.PerformanceOutputTokens = int64(usage.CompletionTokens)
}

// appendFundingSource 在消费明细里标注本次扣费的资金来源（hc20 免费/付费余额隔离）：
//   - 订阅计费：订阅额度；
//   - 免费分组白名单内且模型也在白名单内：免费余额优先，不足部分扣付费余额；
//   - 白名单外（付费分组或模型受限）：付费余额。
func appendFundingSource(relayInfo *relaycommon.RelayInfo, logContent []*common.Message) []*common.Message {
	if relayInfo == nil {
		return logContent
	}
	if relayInfo.BillingSource == BillingSourceSubscription {
		return append(logContent, common.NewMessage("资金来源: 订阅额度"))
	}
	if relayFreeQuotaUsable(relayInfo) {
		return append(logContent, common.NewMessage("资金来源: 免费余额优先，不足部分扣付费余额"))
	}
	return append(logContent, common.NewMessage("资金来源: 付费余额"))
}

// relayFreeQuotaUsable 判定本次计费是否允许消耗免费额度（hc31 · B89）：
// 分组白名单（FreeQuotaAllowedGroups）与模型白名单（FreeQuotaAllowedModels）
// 需同时放行；模型以计费身份（BillingModelName，缺省回落 OriginModelName）为准，
// 保证虚拟定价别名下判定与实际扣费一致。
func relayFreeQuotaUsable(relayInfo *relaycommon.RelayInfo) bool {
	if relayInfo == nil {
		return false
	}
	return model.FreeQuotaUsable(relayInfo.UsingGroup, relayInfo.GetBillingModelName())
}

func PreConsumeTokenQuota(relayInfo *relaycommon.RelayInfo, quota int) error {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if relayInfo.IsPlayground {
		return nil
	}
	// 原子预扣：检查与扣减在同一操作中完成，并发请求不可能同时通过检查后超扣。
	reserved, err := model.TryReserveTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota, relayInfo.TokenUnlimited)
	if err != nil {
		return err
	}
	if !reserved {
		remainQuota := 0
		if token, tokenErr := model.GetTokenByKey(relayInfo.TokenKey, false); tokenErr == nil && token != nil {
			remainQuota = token.RemainQuota
		}
		return fmt.Errorf("token quota is not enough, token remain quota: %s, need quota: %s", logger.FormatQuota(remainQuota), logger.FormatQuota(quota))
	}
	return nil
}

type postConsumeQuotaResult struct {
	FundingApplied bool
	TokenApplied   bool
}

func PostConsumeQuota(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool) error {
	_, err := postConsumeQuotaWithResult(relayInfo, quota, preConsumedQuota, sendEmail)
	return err
}

func postConsumeQuotaWithResult(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool) (result postConsumeQuotaResult, err error) {

	// 1) Consume from wallet quota OR subscription item
	if relayInfo != nil && relayInfo.BillingSource == BillingSourceSubscription {
		if relayInfo.SubscriptionId == 0 {
			return result, errors.New("subscription id is missing")
		}
		delta := int64(quota)
		if delta != 0 {
			if err := model.PostConsumeUserSubscriptionDelta(relayInfo.SubscriptionId, delta); err != nil {
				return result, err
			}
			relayInfo.SubscriptionPostDelta += delta
		}
	} else {
		// Wallet
		if quota > 0 {
			if relayFreeQuotaUsable(relayInfo) {
				err = model.DecreaseUserQuota(relayInfo.UserId, quota, false)
			} else {
				err = model.DecreaseUserQuotaPaidOnly(relayInfo.UserId, quota, false)
			}
		} else {
			err = model.IncreaseUserQuota(relayInfo.UserId, -quota, false)
		}
		if err != nil {
			return result, err
		}
	}
	result.FundingApplied = true

	if !relayInfo.IsPlayground {
		if quota > 0 {
			err = model.DecreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota)
		} else {
			err = model.IncreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, -quota)
		}
		if err != nil {
			return result, err
		}
		result.TokenApplied = true
	}

	if sendEmail {
		if (quota + preConsumedQuota) != 0 {
			checkAndSendQuotaNotify(relayInfo, quota, preConsumedQuota)
		}
	}

	return result, nil
}

func checkAndSendQuotaNotify(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int) {
	gopool.Go(func() {
		userSetting := relayInfo.UserSetting
		// UserQuota is the balance before this request; quota plus
		// preConsumedQuota is what the request finally cost.
		balanceAfter := relayInfo.UserQuota - (quota + preConsumedQuota)
		if !quotaReminderDue(userSetting, int64(relayInfo.UserQuota), int64(balanceAfter)) {
			return
		}
		lang := userSetting.Language
		prompt := i18n.Translate(lang, i18n.MsgNotifyQuotaLowTitle)
		content, values := quotaNotifyContent(lang, userSetting.NotifyType, prompt, logger.FormatQuota(balanceAfter))
		err := NotifyUser(relayInfo.UserId, relayInfo.UserEmail, relayInfo.UserSetting, dto.NewNotify(dto.NotifyTypeQuotaExceed, prompt, content, values))
		if err != nil {
			common.SysError(common.LogText("failed to send quota notify to user %d: %s", relayInfo.UserId, err.Error()))
		}
	})
}

func checkAndSendSubscriptionQuotaNotify(relayInfo *relaycommon.RelayInfo) {
	gopool.Go(func() {
		if relayInfo == nil {
			return
		}
		if relayInfo.SubscriptionId == 0 || relayInfo.SubscriptionAmountTotal <= 0 {
			return
		}

		userSetting := relayInfo.UserSetting
		usedBefore := relayInfo.SubscriptionAmountUsedAfterPreConsume - relayInfo.SubscriptionPreConsumed
		usedAfter := relayInfo.SubscriptionAmountUsedAfterPreConsume + relayInfo.SubscriptionPostDelta
		remaining := relayInfo.SubscriptionAmountTotal - usedAfter
		if !quotaReminderDue(userSetting, relayInfo.SubscriptionAmountTotal-usedBefore, remaining) {
			return
		}

		lang := userSetting.Language
		prompt := i18n.Translate(lang, i18n.MsgNotifySubscriptionQuotaLowTitle)
		content, values := quotaNotifyContent(lang, userSetting.NotifyType, prompt, logger.FormatQuota(int(remaining)))
		if err := NotifyUser(relayInfo.UserId, relayInfo.UserEmail, relayInfo.UserSetting, dto.NewNotify(dto.NotifyTypeQuotaExceed, prompt, content, values)); err != nil {
			common.SysError(common.LogText("failed to send subscription quota notify to user %d: %s", relayInfo.UserId, err.Error()))
		}
	})
}

// quotaReminderDue reports whether a charge that moved a balance (wallet or
// subscription) from before to after earns a low-balance reminder. The site
// and the user must both have reminders on, and the charge must cross the
// user's threshold, or the site default when the user set none. A balance
// already below the threshold earns nothing more until a top-up lifts it back
// above, so a user gets one reminder per drop.
func quotaReminderDue(userSetting dto.UserSetting, before int64, after int64) bool {
	if !common.QuotaRemindEnabled {
		return false
	}
	if userSetting.QuotaWarningEnabled != nil && !*userSetting.QuotaWarningEnabled {
		return false
	}
	threshold := float64(common.QuotaRemindThreshold)
	if userSetting.QuotaWarningThreshold != 0 {
		threshold = userSetting.QuotaWarningThreshold
	}
	return float64(before) >= threshold && float64(after) < threshold
}

// quotaNotifyContent renders a low-quota notice in the user's saved language.
// Bark and Gotify get plain text; email and webhook get HTML with the top-up
// link. values stay in the notify payload for webhook receivers.
func quotaNotifyContent(lang string, notifyType string, prompt string, remaining string) (string, []any) {
	switch notifyType {
	case dto.NotifyTypeBark:
		// Bark推送使用简短文本，不支持HTML
		return i18n.Translate(lang, i18n.MsgNotifyQuotaLowBark, map[string]any{"Prompt": prompt, "Quota": remaining}), []any{prompt, remaining}
	case dto.NotifyTypeGotify:
		return i18n.Translate(lang, i18n.MsgNotifyQuotaLowGotify, map[string]any{"Prompt": prompt, "Quota": remaining}), []any{prompt, remaining}
	default:
		topUpLink := PaymentReturnURL("/wallet")
		return i18n.Translate(lang, i18n.MsgNotifyQuotaLowEmail, map[string]any{"Prompt": prompt, "Quota": remaining, "Link": topUpLink}), []any{prompt, remaining, topUpLink, topUpLink}
	}
}
