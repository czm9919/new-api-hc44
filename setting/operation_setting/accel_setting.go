package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// AccelSetting 加速专线的运营配置（hc50 · P2）。
//
// 为什么要这几项：
//   - IngressHost：入站加速（DCDN）是基础设施侧的事，代码侧唯一能做的是
//     「告诉用户该用哪个域名接」。填了它，加速页会展示接入地址与示例，
//     用户不必再猜「我买的加速到底走哪个域名」。
//   - SlotTTLSeconds：Redis 并发槽位的兜底存活时间。进程被 kill 时不会走
//     release，只能靠 TTL 回收；正常请求远短于它，调小反而更安全。
//   - LineDisplayEnabled：线路健康（成功率为进程内 EWMA）在重启后会冷启动，
//     不想让用户看到「0%」时可以整体关掉展示，只留管理端可见。
type AccelSetting struct {
	IngressHost        string `json:"ingress_host"`         // 入站加速域名，如 accel.example.com；空表示不展示
	SlotTTLSeconds     int    `json:"slot_ttl_seconds"`     // Redis 槽位兜底 TTL，默认 600
	LineDisplayEnabled bool   `json:"line_display_enabled"` // 用户端是否展示线路状态
	LineDegradedRate   int    `json:"line_degraded_rate"`   // 成功率低于该百分比视为异常线路，默认 50
}

var accelSetting = AccelSetting{
	IngressHost:        "",
	SlotTTLSeconds:     600,
	LineDisplayEnabled: true,
	LineDegradedRate:   50,
}

func init() {
	config.GlobalConfig.Register("accel_setting", &accelSetting)
}

// GetAccelSetting 返回加速配置；越界值回落到默认，避免配置写坏后槽位永久不回收。
func GetAccelSetting() *AccelSetting {
	if accelSetting.SlotTTLSeconds < 30 || accelSetting.SlotTTLSeconds > 86400 {
		accelSetting.SlotTTLSeconds = 600
	}
	if accelSetting.LineDegradedRate <= 0 || accelSetting.LineDegradedRate > 100 {
		accelSetting.LineDegradedRate = 50
	}
	return &accelSetting
}
