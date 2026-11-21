package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 系统级运维通知必须各用各的限流桶。
// hc46 真机验证时发现：战报与熔断告警共用 admin_alert，
// 默认「10 分钟 2 条」被战报 + 熔断占满后，随后发出的「号池恢复」通知被静默丢弃。
func TestSystemNotifyTypesUseSeparateLimits(t *testing.T) {
	originalRedis := common.RedisEnabled
	originalCount := constant.NotifyLimitCount
	originalWindow := constant.NotificationLimitDurationMinute
	defer func() {
		common.RedisEnabled = originalRedis
		constant.NotifyLimitCount = originalCount
		constant.NotificationLimitDurationMinute = originalWindow
		notifyLimitStore.Range(func(key, value any) bool {
			notifyLimitStore.Delete(key)
			return true
		})
	}()
	common.RedisEnabled = false
	constant.NotifyLimitCount = 2
	// 窗口为 0 时计数每次都会被重置，限流不会生效。
	constant.NotificationLimitDurationMinute = 10

	// 三个类型必须是不同字符串，否则限流键会撞在一起。
	assert.NotEqual(t, dto.NotifyTypeAdminAlert, dto.NotifyTypeDailyReport)
	assert.NotEqual(t, dto.NotifyTypeAdminAlert, dto.NotifyTypePoolHealth)
	assert.NotEqual(t, dto.NotifyTypeDailyReport, dto.NotifyTypePoolHealth)

	const userID = 990001
	for i := 0; i < constant.NotifyLimitCount; i++ {
		ok, err := CheckNotificationLimit(userID, dto.NotifyTypeDailyReport)
		require.NoError(t, err)
		require.True(t, ok)
	}

	// 战报配额已用满，再发一条应被拦下。
	ok, err := CheckNotificationLimit(userID, dto.NotifyTypeDailyReport)
	require.NoError(t, err)
	assert.False(t, ok, "daily_report 超出配额应被限流")

	// 事故告警不受战报配额影响。
	ok, err = CheckNotificationLimit(userID, dto.NotifyTypeAdminAlert)
	require.NoError(t, err)
	assert.True(t, ok, "admin_alert 不应被 daily_report 的配额挤掉")

	// 号池告警同样独立。
	ok, err = CheckNotificationLimit(userID, dto.NotifyTypePoolHealth)
	require.NoError(t, err)
	assert.True(t, ok, "pool_health 不应被 daily_report 的配额挤掉")

	// 号池的熔断与恢复共用同一个桶：仍受统一配额约束，避免上游抖动刷屏。
	for i := 1; i < constant.NotifyLimitCount; i++ {
		ok, err = CheckNotificationLimit(userID, dto.NotifyTypePoolHealth)
		require.NoError(t, err)
		require.True(t, ok)
	}
	ok, err = CheckNotificationLimit(userID, dto.NotifyTypePoolHealth)
	require.NoError(t, err)
	assert.False(t, ok, "pool_health 超出配额应被限流")
}
