package model

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MoeForge/moego/regexp"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// RequestPolicySnapshot is the validated read model behind the request policy
// settings API. It is rebuilt from the option map on every option write, so
// the runtime globals updated by the same write never disagree with it.
type RequestPolicySnapshot struct {
	Affinity        operation_setting.ChannelAffinitySetting
	RetryTimes      int
	RetryCodes      []operation_setting.StatusCodeRange
	DisableCodes    []operation_setting.StatusCodeRange
	DisableKeywords []string
	AutoDisable     bool
	Options         map[string]string
}

var requestPolicySnapshot atomic.Pointer[RequestPolicySnapshot]
var requestPolicyOptionMutex sync.Mutex
var requestPolicyDefaults = requestPolicyDefaultOptions()

// loadBalanceOptionFields 负载均衡配置里允许走请求策略接口的字段。
//
// 显式列举而不是按前缀全放行：配置结构体以后加字段时不会自动出现在
// 请求策略接口上，免得同一个字段被「请求策略页」和「通用选项接口」
// 两套口径分别维护，最后总有一套漏掉校验。
var loadBalanceOptionFields = map[string]struct{}{
	"by_group":                        {},
	"smart_top_n":                     {},
	"smart_explore_percent":           {},
	"smart_fail_cooldown_seconds":     {},
	"smart_min_samples":               {},
	"smart_load_penalty":              {},
	"smart_latency_half_life_minutes": {},
}

// autoModelOptionFields auto 智能模型配置里允许走请求策略接口的字段。
var autoModelOptionFields = map[string]struct{}{
	"enabled": {},
	"pool":    {},
}

func requestPolicyDefaultOptions() map[string]string {
	defaults := make(map[string]string)
	for prefix, value := range map[string]any{
		"channel_affinity_setting.": operation_setting.GetChannelAffinitySetting(),
		"monitor_setting.":          operation_setting.GetMonitorSetting(),
		"loadbalance_setting.":      operation_setting.GetLoadBalanceSetting(),
		"auto_model_setting.":       operation_setting.GetAutoModelSetting(),
	} {
		fields, err := config.ConfigToMap(value)
		if err != nil {
			panic(err)
		}
		for field, value := range fields {
			defaults[prefix+field] = value
		}
	}
	defaults["RetryTimes"] = strconv.Itoa(common.RetryTimes)
	defaults["AutomaticRetryStatusCodes"] = operation_setting.AutomaticRetryStatusCodesToString()
	defaults["AutomaticDisableStatusCodes"] = operation_setting.AutomaticDisableStatusCodesToString()
	defaults["AutomaticDisableKeywords"] = operation_setting.AutomaticDisableKeywordsToString()
	defaults["AutomaticDisableChannelEnabled"] = strconv.FormatBool(common.AutomaticDisableChannelEnabled)
	defaults["AutomaticEnableChannelEnabled"] = strconv.FormatBool(common.AutomaticEnableChannelEnabled)
	defaults["ChannelDisableThreshold"] = strconv.FormatFloat(common.ChannelDisableThreshold, 'f', -1, 64)
	defaults["ChannelLoadBalanceStrategy"] = string(CurrentLoadBalanceStrategy)
	return defaults
}

func IsRequestPolicyOption(key string) bool {
	if strings.HasPrefix(key, "channel_affinity_setting.") {
		return true
	}
	// 渠道调度相关的两组分层配置（loadbalance_setting.* / auto_model_setting.*）
	// 由「请求策略 · 渠道调度」页整块提交，必须在这里放行，否则保存时报
	// not a request policy option。
	if field, ok := strings.CutPrefix(key, "loadbalance_setting."); ok {
		_, known := loadBalanceOptionFields[field]
		return known
	}
	if field, ok := strings.CutPrefix(key, "auto_model_setting."); ok {
		_, known := autoModelOptionFields[field]
		return known
	}
	switch key {
	case "ChannelLoadBalanceStrategy":
		return true
	case "VGuardEnabled", "VGuardStrongWords", "VGuardWeakWords", "VGuardWeakThreshold", "VGuardWindowMinutes", "VGuardBlockHours", "VGuardInjectEnabled", "VGuardInjectPrompt", "VGuardApplyGroups", "VGuardUserWhitelist", "VGuardBanTiers", "VGuardOutputCheckEnabled", "VGuardWarnEmailEnabled", "VGuardBanEmailEnabled", "VGuardWarnEmailSubject", "VGuardWarnEmailBody", "VGuardBanEmailSubject", "VGuardBanEmailBody", "VGuardEmailThrottlePerDay", "VGuardLLMEnabled", "VGuardLLMMode", "VGuardLLMBaseURL", "VGuardLLMKey", "VGuardLLMModel", "VGuardLLMPromptTemplate", "VGuardLLMTrigger", "VGuardLLMAction", "VGuardLLMSustainHours", "VGuardLLMTimeoutSec", "VGuardLLMMaxTextChars", "VGuardLLMUnsafePattern", "VGuardLLMSafePattern", "VGuardLLMFailOpen", "RealNameEnabled", "RealNameMethod", "RealNameApplyGroups", "RealNameUserWhitelist", "RealNameBlockRelay", "RealNameBlockTopup", "RealNameBlockMessage", "RealNameNoticeText", "RealNameTieredId2Trigger", "RealNameTieredQuotaThreshold", "RealNameOneIdOneAccount", "RealNameMinAge", "RealNameId2Provider", "RealNameId2AppCode", "RealNameId2SecretKey", "RealNameId2Endpoint", "RealNameId2BodyTemplate", "RealNameId2SuccessRule", "RealNameSmsProvider", "RealNameSmsAccessKey", "RealNameSmsSecretKey", "RealNameSmsSignName", "RealNameSmsTemplateCode", "RealNameSmsAppId", "RealNameSmsEndpoint", "RealNameSmsBodyTemplate", "RealNameSmsAuthHeader", "RealNameSmsCodeTTL", "RealNamePhonePattern", "RealNameHashPepper", "RealNameProviderTimeoutSec", "VGuardLLMAsyncEnabled", "VGuardLLMFastSeconds", "VGuardLLMAsyncSeconds", "VGuardLLMProtocol", "VGuardLLMSoInstructions", "VGuardLLMSoUnsafeCriteria", "VGuardLLMSoSafeCriteria", "VGuardLLMSoNoulInstruction", "VGuardLLMMaxConcurrent", "VGuardLLMErrorCooldownSec", "VGuardLLMModels", "VGuardLLMSafeExitStreak", "VGuardLLMSafeExitFloorMin", "VGuardLLMWeakReenter":
		return true
	case "AutomaticEnableChannelEnabled", "ChannelDisableThreshold", "monitor_setting.auto_test_channel_enabled", "monitor_setting.auto_test_channel_minutes", "monitor_setting.channel_test_concurrency", "monitor_setting.channel_test_mode", "monitor_setting.model_auto_check_enabled", "RetryTimes", "AutomaticRetryStatusCodes", "AutomaticDisableChannelEnabled", "AutomaticDisableStatusCodes", "AutomaticDisableKeywords":
		return true
	}
	return false
}

func CurrentRequestPolicy() *RequestPolicySnapshot {
	if snapshot := requestPolicySnapshot.Load(); snapshot != nil {
		return snapshot
	}
	// Startup and test fixtures may not have loaded options yet.
	snapshot, err := BuildRequestPolicy(nil)
	if err != nil {
		return &RequestPolicySnapshot{Options: map[string]string{}}
	}
	return snapshot
}

// validateChannelDispatchOptions 校验渠道调度相关选项（全局策略、负载均衡
// 参数、按分组覆盖表、auto 智能模型池）。
//
// 这些值最终由 setting/config.updateConfigFromMap 反射写进结构体，而它对
// 解析失败的字段是静默 continue 的；operation_setting.GetLoadBalanceSetting
// 也只是把越界值悄悄换回默认值。两处都不会报错，所以非法输入必须在这里挡住，
// 否则用户看到的是「保存成功，但刷新后值又变回去了」。
func validateChannelDispatchOptions(raw map[string]string) error {
	if strategy, ok := raw["ChannelLoadBalanceStrategy"]; ok && strategy != "" && !IsValidLoadBalanceStrategy(strategy) {
		return fmt.Errorf("unknown channel load balance strategy: %s", strategy)
	}
	// 边界与前端 zod 校验、GetLoadBalanceSetting 的钳制范围保持一致。
	for _, field := range []struct {
		name    string
		min     float64
		max     float64
		integer bool
	}{
		{"smart_top_n", 1, 64, true},
		{"smart_explore_percent", 0, 50, true},
		{"smart_fail_cooldown_seconds", 0, 600, true},
		{"smart_min_samples", 1, 1000, true},
		{"smart_load_penalty", 0, 100, false},
		{"smart_latency_half_life_minutes", 0.5, 1440, false},
	} {
		key := "loadbalance_setting." + field.name
		value, ok := raw[key]
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("invalid numeric value: %s", key)
		}
		if field.integer && number != math.Trunc(number) {
			return fmt.Errorf("%s must be an integer", key)
		}
		if number < field.min || number > field.max {
			return fmt.Errorf("%s must be between %g and %g", key, field.min, field.max)
		}
	}
	// by_group 是「分组名 -> 策略名」的覆盖表。策略名非法时
	// StrategyForGroup 会静默回退到全局策略，表现为「分组覆盖不生效」，
	// 所以这里直接拒绝。
	if value, ok := raw["loadbalance_setting.by_group"]; ok && strings.TrimSpace(value) != "" {
		byGroup := map[string]string{}
		if err := common.Unmarshal([]byte(value), &byGroup); err != nil {
			return fmt.Errorf("loadbalance_setting.by_group must be a JSON object of group -> strategy: %w", err)
		}
		for group, strategy := range byGroup {
			if !IsValidLoadBalanceStrategy(strategy) {
				return fmt.Errorf("loadbalance_setting.by_group: unknown strategy %q for group %q", strategy, group)
			}
		}
	}
	if value, ok := raw["auto_model_setting.enabled"]; ok && strings.TrimSpace(value) != "" {
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("invalid boolean: auto_model_setting.enabled")
		}
	}
	if value, ok := raw["auto_model_setting.pool"]; ok && strings.TrimSpace(value) != "" {
		pool := []string{}
		if err := common.Unmarshal([]byte(value), &pool); err != nil {
			return fmt.Errorf("auto_model_setting.pool must be a JSON array of model names: %w", err)
		}
		for _, model := range pool {
			if strings.TrimSpace(model) == "" {
				return fmt.Errorf("auto_model_setting.pool: model name must not be empty")
			}
		}
	}
	return nil
}

func BuildRequestPolicy(options map[string]string) (*RequestPolicySnapshot, error) {
	defaults := requestPolicyDefaults

	raw := make(map[string]string)
	for key, value := range defaults {
		if IsRequestPolicyOption(key) {
			raw[key] = value
		}
	}
	maps.Copy(raw, options)
	snapshot := &RequestPolicySnapshot{Options: raw}
	if err := validateChannelDispatchOptions(raw); err != nil {
		return nil, err
	}
	affinityFields := map[string]string{}
	for key, value := range raw {
		if field, ok := strings.CutPrefix(key, "channel_affinity_setting."); ok {
			switch field {
			case "enabled", "session_mode", "switch_on_success", "keep_on_channel_disabled", "max_entries", "default_ttl_seconds", "rules":
			default:
				return nil, fmt.Errorf("unknown affinity option: %s", key)
			}
			affinityFields[field] = value
		}
	}
	// Decode through JSON so malformed scalar/array values cannot be silently ignored.
	affinityJSON := map[string]json.RawMessage{}
	for key, value := range affinityFields {
		if key == "session_mode" {
			encoded, _ := common.Marshal(value)
			affinityJSON[key] = encoded
		} else {
			affinityJSON[key] = json.RawMessage(value)
		}
	}
	encoded, err := common.Marshal(affinityJSON)
	if err != nil {
		return nil, err
	}
	if err := common.Unmarshal(encoded, &snapshot.Affinity); err != nil {
		return nil, err
	}
	snapshot.RetryTimes, err = strconv.Atoi(raw["RetryTimes"])
	if err != nil || snapshot.RetryTimes < 0 || snapshot.RetryTimes == math.MaxInt {
		return nil, fmt.Errorf("retry times must be a non-negative integer with room for the initial attempt")
	}
	snapshot.AutoDisable, err = strconv.ParseBool(raw["AutomaticDisableChannelEnabled"])
	if err != nil {
		return nil, err
	}
	snapshot.RetryCodes, err = operation_setting.ParseHTTPStatusCodeRanges(raw["AutomaticRetryStatusCodes"])
	if err != nil {
		return nil, err
	}
	snapshot.DisableCodes, err = operation_setting.ParseHTTPStatusCodeRanges(raw["AutomaticDisableStatusCodes"])
	if err != nil {
		return nil, err
	}
	snapshot.DisableKeywords = strings.Split(raw["AutomaticDisableKeywords"], "\n")
	for _, key := range []string{"AutomaticEnableChannelEnabled", "monitor_setting.auto_test_channel_enabled"} {
		if _, err := strconv.ParseBool(raw[key]); err != nil {
			return nil, fmt.Errorf("invalid boolean: %s", key)
		}
	}
	if err := operation_setting.ValidateChannelTestConcurrency(raw["monitor_setting.channel_test_concurrency"]); err != nil {
		return nil, err
	}
	for _, key := range []string{"ChannelDisableThreshold", "monitor_setting.auto_test_channel_minutes"} {
		if raw[key] == "" && key == "ChannelDisableThreshold" {
			continue
		}
		value, err := strconv.ParseFloat(raw[key], 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("invalid numeric value: %s", key)
		}
	}
	switch raw["monitor_setting.channel_test_mode"] {
	case "scheduled_all", "auto_ban_only", "passive_recovery":
	default:
		return nil, fmt.Errorf("invalid channel test mode")
	}

	if snapshot.Affinity.MaxEntries < 0 || snapshot.Affinity.DefaultTTLSeconds < 0 {
		return nil, fmt.Errorf("invalid affinity cache limits")
	}
	switch snapshot.Affinity.SessionMode {
	case "", "off", "prefer", "strict":
	default:
		return nil, fmt.Errorf("invalid global session mode")
	}
	for _, rule := range snapshot.Affinity.Rules {
		switch rule.SessionMode {
		case "", "inherit", "off", "prefer", "strict":
		default:
			return nil, fmt.Errorf("invalid session mode for rule %q", rule.Name)
		}
		patterns := append(append([]string{}, rule.ModelRegex...), rule.PathRegex...)
		if rule.ValueRegex != "" {
			patterns = append(patterns, rule.ValueRegex)
		}
		for _, pattern := range patterns {
			if _, err := regexp.Compile(pattern); err != nil {
				return nil, fmt.Errorf("rule %q: %w", rule.Name, err)
			}
		}
		if rule.TTLSeconds < 0 {
			return nil, fmt.Errorf("negative affinity TTL")
		}
	}
	return snapshot, nil
}

func refreshRequestPolicySnapshot() error {
	common.OptionMapRWMutex.RLock()
	options := make(map[string]string)
	for key, value := range common.OptionMap {
		if IsRequestPolicyOption(key) {
			options[key] = value
		}
	}
	common.OptionMapRWMutex.RUnlock()
	snapshot, err := BuildRequestPolicy(options)
	if err == nil {
		requestPolicySnapshot.Store(snapshot)
	}
	return err
}

// UpdateRequestPolicyOptions validates the entire resulting policy before any
// write. Unknown JSON rule fields stay in the original option string.
func UpdateRequestPolicyOptions(values map[string]string) error {
	requestPolicyOptionMutex.Lock()
	defer requestPolicyOptionMutex.Unlock()
	options := maps.Clone(CurrentRequestPolicy().Options)
	for key, value := range values {
		if !IsRequestPolicyOption(key) {
			return fmt.Errorf("not a request policy option: %s", key)
		}
		options[key] = value
	}
	snapshot, err := BuildRequestPolicy(options)
	if err != nil {
		return err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		for key, value := range values {
			option := Option{Key: key}
			if err := tx.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
				return err
			}
			if err := tx.Model(&option).Update("value", value).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	BumpSyncTopic(SyncTopicOptions)
	for key, value := range values {
		if err := updateOptionMap(key, value); err != nil {
			return err
		}
	}
	requestPolicySnapshot.Store(snapshot)
	return nil
}
