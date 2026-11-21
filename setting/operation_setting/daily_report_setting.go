package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// DailyReportSetting 每日战报：每天固定时刻把前一天的运营数据推给管理员。
//
// 战报不经过「告警总开关」（BillingAlertEnabled）——它是日报，不是告警，
// 关掉告警的人通常仍然想每天看一眼昨天的量。
type DailyReportSetting struct {
	Enabled  bool `json:"enabled"`
	Hour     int  `json:"hour"`      // 北京时间 0-23
	Minute   int  `json:"minute"`    // 0-59
	TopUsers int  `json:"top_users"` // 战报里列几个消耗大户，0 表示不列
}

var dailyReportSetting = DailyReportSetting{
	Enabled:  true,
	Hour:     9,
	Minute:   0,
	TopUsers: 3,
}

func init() {
	config.GlobalConfig.Register("daily_report_setting", &dailyReportSetting)
}

func GetDailyReportSetting() *DailyReportSetting {
	// 时间越界时回落到默认值，避免配置写坏后战报永远不触发。
	if dailyReportSetting.Hour < 0 || dailyReportSetting.Hour > 23 {
		dailyReportSetting.Hour = 9
	}
	if dailyReportSetting.Minute < 0 || dailyReportSetting.Minute > 59 {
		dailyReportSetting.Minute = 0
	}
	if dailyReportSetting.TopUsers < 0 || dailyReportSetting.TopUsers > 10 {
		dailyReportSetting.TopUsers = 3
	}
	return &dailyReportSetting
}
