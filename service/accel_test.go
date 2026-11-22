package service

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func TestAccelSubActive(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name string
		sub  *model.UserAccelSub
		want bool
	}{
		{name: "nil subscription", sub: nil, want: false},
		{name: "active and not expired", sub: &model.UserAccelSub{Status: model.AccelSubStatusActive, ExpireAt: now + 60}, want: true},
		{name: "active but expired", sub: &model.UserAccelSub{Status: model.AccelSubStatusActive, ExpireAt: now - 1}, want: false},
		{name: "expired status", sub: &model.UserAccelSub{Status: model.AccelSubStatusExpired, ExpireAt: now + 60}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sub.Active(now); got != tc.want {
				t.Fatalf("Active() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAcquireAccelSlotRespectsLimit(t *testing.T) {
	const userId = 900001
	first, ok := AcquireAccelSlot(userId, 2)
	if !ok {
		t.Fatal("first slot should be granted")
	}
	second, ok := AcquireAccelSlot(userId, 2)
	if !ok {
		t.Fatal("second slot should be granted")
	}
	if _, ok := AcquireAccelSlot(userId, 2); ok {
		t.Fatal("third slot must be rejected when limit is 2")
	}
	// 释放一个后应能再取一个，验证计数没有泄漏。
	first()
	third, ok := AcquireAccelSlot(userId, 2)
	if !ok {
		t.Fatal("slot should be granted again after release")
	}
	// 重复释放不应把计数打成负数（once 保护）。
	first()
	second()
	third()
	fourth, ok := AcquireAccelSlot(userId, 2)
	if !ok {
		t.Fatal("slot should be available after all releases")
	}
	fourth()
}

func TestAcquireAccelSlotUnlimited(t *testing.T) {
	const userId = 900002
	releases := make([]func(), 0, 5)
	for i := 0; i < 5; i++ {
		release, ok := AcquireAccelSlot(userId, 0)
		if !ok {
			t.Fatalf("limit 0 means unlimited, slot %d rejected", i)
		}
		releases = append(releases, release)
	}
	for _, release := range releases {
		release()
	}
}

func TestValidateAccelPlanGroup(t *testing.T) {
	const groupName = "accel-test-group"

	originalRatios := ratio_setting.GroupRatio2JSONString()
	originalUsable := setting.UserUsableGroups2JSONString()
	t.Cleanup(func() {
		if err := ratio_setting.UpdateGroupRatioByJSONString(originalRatios); err != nil {
			t.Fatalf("restore group ratios: %v", err)
		}
		if err := setting.UpdateUserUsableGroupsByJSONString(originalUsable); err != nil {
			t.Fatalf("restore usable groups: %v", err)
		}
	})

	// 1. 分组没有倍率时必须拒绝：否则请求会被判成「已废弃分组」。
	if err := ValidateAccelPlanGroup(groupName); !errors.Is(err, ErrAccelGroupMissing) {
		t.Fatalf("expected ErrAccelGroupMissing, got %v", err)
	}

	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"accel-test-group":1.3}`); err != nil {
		t.Fatalf("inject group ratio: %v", err)
	}

	// 2. 有倍率且不在「用户可用分组」里 = 合法。
	if err := ValidateAccelPlanGroup(groupName); err != nil {
		t.Fatalf("expected valid plan group, got %v", err)
	}

	// 3. 一旦进了「用户可用分组」，所有人免费可用，套餐就卖不动了。
	if err := setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default group","accel-test-group":"Free for all"}`); err != nil {
		t.Fatalf("inject usable groups: %v", err)
	}
	if err := ValidateAccelPlanGroup(groupName); !errors.Is(err, ErrAccelGroupPublic) {
		t.Fatalf("expected ErrAccelGroupPublic, got %v", err)
	}
}

func TestChargePaidBalanceRejectsOutOfRange(t *testing.T) {
	if _, err := chargePaidBalance(1, AccelMaxPrice+1); err == nil {
		t.Fatal("price above AccelMaxPrice must be rejected")
	}
}
