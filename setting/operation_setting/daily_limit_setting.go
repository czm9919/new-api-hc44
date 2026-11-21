package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// DailyLimitSetting 每日额度护栏。
//
// 它管的不是「用户有没有钱」——余额是用户自己的，花光是他的事；
// 它管的是「一个账号能不能在一天之内把整池上游打穿」。号池是共享的，
// 单个账号无上限刷会把 NIM 的免费额度吃干净，连带影响所有人，还容易
// 触发上游风控把整批 key 做掉。
//
// 因此这里只做「当日已消耗 >= 上限就拒绝」的粗粒度判断，不预估本次
// 请求要花多少（流式请求无法预知），也不区分模型，简单、可解释、不误伤。
type DailyLimitSetting struct {
	Enabled bool `json:"enabled"`
	// UserQuota 每用户每日最多消耗的额度（quota 单位）。
	// QuotaPerUnit=500000 时，500000 = $1，即 1 元人民币约 70000。
	// 0 表示不限制。
	UserQuota int `json:"user_quota"`
	// GroupQuota 按分组覆盖 UserQuota，键为分组名（如 "vip"）。
	// 分组没有配置时回退到 UserQuota。
	GroupQuota map[string]int `json:"group_quota"`
}

var dailyLimitSetting = DailyLimitSetting{
	Enabled:    false,
	UserQuota:  0,
	GroupQuota: map[string]int{},
}

func init() {
	config.GlobalConfig.Register("daily_limit_setting", &dailyLimitSetting)
}

// GetDailyLimitSetting 获取每日额度配置。
func GetDailyLimitSetting() *DailyLimitSetting {
	return &dailyLimitSetting
}

// DailyQuotaForGroup 返回某分组当日可用额度；0 表示不限。
func (s *DailyLimitSetting) DailyQuotaForGroup(group string) int {
	if s == nil || !s.Enabled {
		return 0
	}
	if group != "" {
		if quota, ok := s.GroupQuota[group]; ok && quota > 0 {
			return quota
		}
	}
	if s.UserQuota < 0 {
		return 0
	}
	return s.UserQuota
}
