package common

import (
	"testing"
	"time"
)

// hc50：Redis 未初始化时必须「明确失败」而不是静默返回成功，
// 否则调用方会以为已经占到位，并发上限形同虚设。
func TestRedisSlotUnavailableWithoutClient(t *testing.T) {
	prevEnabled, prevRDB := RedisEnabled, RDB
	RedisEnabled, RDB = false, nil
	defer func() { RedisEnabled, RDB = prevEnabled, prevRDB }()

	if RedisSlotAvailable() {
		t.Fatal("RedisSlotAvailable() must be false without a client")
	}
	if ok, err := RedisReserveSlot("accel:slot:test", 3, time.Minute); err == nil || ok {
		t.Fatalf("ReserveSlot should fail loudly, got ok=%v err=%v", ok, err)
	}
	if err := RedisReleaseSlot("accel:slot:test"); err == nil {
		t.Fatal("ReleaseSlot should fail loudly without a client")
	}
}

// limit <= 0 表示不限并发：不碰 Redis，直接放行。
func TestRedisReserveSlotUnlimitedSkipsRedis(t *testing.T) {
	if ok, err := RedisReserveSlot("accel:slot:unlimited", 0, time.Minute); !ok || err != nil {
		t.Fatalf("limit 0 must always succeed, got ok=%v err=%v", ok, err)
	}
}

func TestSlotKey(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		want  string
	}{
		{name: "user id", parts: []string{"42"}, want: "accel:slot:42"},
		{name: "multi dimension", parts: []string{"42", "gpt"}, want: "accel:slot:42:gpt"},
		// 冒号是 key 的分隔符，业务值里带冒号必须转义，否则会拼出层级错乱的键。
		{name: "colon in part", parts: []string{"42:gpt"}, want: "accel:slot:42_gpt"},
		{name: "space in part", parts: []string{"42", "a b"}, want: "accel:slot:42:a_b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SlotKey("accel:slot", tc.parts...); got != tc.want {
				t.Fatalf("SlotKey() = %q, want %q", got, tc.want)
			}
		})
	}
}
