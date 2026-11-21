package common

import "strings"

// =============================================================================
// 余额显示配置（hc23）：付费余额 / 免费额度在后台界面如何展示。
//
// 背景：user.quota 是「工作余额总额」，其中 free_quota 是签到等赠送的免费部分，
// 付费余额 = quota - free_quota。用户侧钱包页早已拆开展示（hc20），但管理员在
// 用户管理列表里只看到一个合并后的「可用余额」，无法判断某个用户到底还有多少
// 真金白银、多少是赠送额度。这里把「怎么展示」交给管理员配置。
//
// 配置放在单个 JSON option 里（而不是拆成 5 个 option），原因：
//  - 这 5 项互相耦合（隐藏免费余额只在拆分开启时有意义），一起读写不会出现
//    「改了一半」的中间态；
//  - 前端只需一次解析、一次保存，和 hc22 的 SidebarNavCustom 保持同一种模式。
// =============================================================================

// BalanceDisplaySettingOptionKey 余额显示配置的 option key。
const BalanceDisplaySettingOptionKey = "BalanceDisplaySetting"

// BalanceDisplaySettingDefault 默认配置：拆分展示开启、名称留空走翻译、不隐藏免费额度。
const BalanceDisplaySettingDefault = `{"split_enabled":true,"paid_label":"","free_label":"","hide_free":false,"show_in_user_list":true}`

// BalanceDisplaySettingJSON 返回当前余额显示配置的 JSON 字符串。
// 未配置过时返回默认值，保证前端拿到的一定是可用配置（空串会让前端退回默认，
// 但显式给出默认值可以让后台设置页直接回显）。
func BalanceDisplaySettingJSON() string {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	if v := strings.TrimSpace(OptionMap[BalanceDisplaySettingOptionKey]); v != "" {
		return v
	}
	return BalanceDisplaySettingDefault
}

// ValidateBalanceDisplaySetting 校验余额显示配置。只做「必须是 JSON 对象」这一层，
// 具体字段缺失/类型错误由前端解析时逐个回落默认值（配置损坏不能把列表页搞崩）。
func ValidateBalanceDisplaySetting(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	var parsed map[string]any
	if err := Unmarshal([]byte(trimmed), &parsed); err != nil {
		return NewMessage("余额显示配置必须是合法的 JSON 对象")
	}
	return nil
}
