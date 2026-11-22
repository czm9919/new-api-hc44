package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// 加速专线（hc48）
//
// 设计要点：
//   - AccelPlan 是「卖什么」：一个套餐解锁一个分组（GroupName），并给它
//     月费、并发上限。分组本身仍然是 new-api 原生的分组机制，倍率在
//     ratio_setting 里配，所以计费链路不需要另写一套。
//   - UserAccelSub 是「谁买了」：一个用户同时只有一个加速订阅（user_id
//     唯一索引），到期时间用 unix 秒，续费按 30 天滚动。
//   - 不设「日额度」字段：加速卖的是通道，用量已经由分组倍率按量扣费，
//     再加日额度只会制造两套口径。P2 若要做，另建用量表。
type AccelPlan struct {
	Id   int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Name string `json:"name" gorm:"type:varchar(64);not null"`
	// GroupName 套餐解锁的分组名，必须与 ratio_setting 的分组倍率同名，
	// 否则 TokenAuth 的 ContainsGroupRatio 校验会把它判成「已废弃分组」。
	GroupName string `json:"group_name" gorm:"type:varchar(32);not null;index:idx_accel_plan_group"`
	// PricePerMonth 月费（额度单位，与 quota 一致），只从付费余额扣。
	PricePerMonth int64 `json:"price_per_month" gorm:"bigint;not null;default:0"`
	// Concurrency 单用户并发上限（同时进行的请求数），0 表示不限制。
	Concurrency int    `json:"concurrency" gorm:"not null;default:0"`
	Description string `json:"description" gorm:"type:varchar(255);default:''"`
	Enabled     bool   `json:"enabled" gorm:"not null;default:true;index:idx_accel_plan_enabled"`
	SortOrder   int    `json:"sort_order" gorm:"not null;default:0"`
	CreatedAt   int64  `json:"created_at" gorm:"bigint;not null"`
	UpdatedAt   int64  `json:"updated_at" gorm:"bigint;not null"`
}

func (AccelPlan) TableName() string {
	return "accel_plans"
}

// 订阅状态。expired 由惰性续费写入，避免起一个定时任务扫全表。
const (
	AccelSubStatusActive  = "active"
	AccelSubStatusExpired = "expired"
)

// AccelPeriodSeconds 一次续费覆盖的时长：固定 30 天，不按自然月，
// 避免 2 月/31 天导致的到期时间漂移。
const AccelPeriodSeconds int64 = 30 * 24 * 60 * 60

type UserAccelSub struct {
	Id     int `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId int `json:"user_id" gorm:"not null;uniqueIndex:idx_accel_sub_user"`
	PlanId int `json:"plan_id" gorm:"not null;index:idx_accel_sub_plan"`
	// GroupName 冗余存一份：套餐被删除或改绑分组后，老订阅仍按购买时的
	// 分组生效，不会因为管理员改配置而突然失效。
	GroupName string `json:"group_name" gorm:"type:varchar(32);not null"`
	// PricePerMonth 购买时的月费快照：套餐被删或调价后，老订阅仍按原价续费，
	// 不会出现「管理员改一次价，存量用户账单跟着变」。
	PricePerMonth int64 `json:"price_per_month" gorm:"bigint;not null;default:0"`
	StartAt       int64 `json:"start_at" gorm:"bigint;not null;default:0"`
	ExpireAt  int64  `json:"expire_at" gorm:"bigint;not null;default:0;index:idx_accel_sub_expire"`
	// AutoRenew 关闭后到期自然失效；取消订阅只关这个开关，不做按天退款。
	AutoRenew bool   `json:"auto_renew" gorm:"not null;default:true"`
	Status    string `json:"status" gorm:"type:varchar(16);not null;default:'active';index:idx_accel_sub_status"`
	// LastChargedAt 最近一次成功扣月费的时间；TotalCharged 累计扣费，用于对账。
	LastChargedAt int64 `json:"last_charged_at" gorm:"bigint;not null;default:0"`
	TotalCharged  int64 `json:"total_charged" gorm:"bigint;not null;default:0"`
	// LastChargeError 最近一次续费失败原因（如余额不足），给用户看的那一句话。
	LastChargeError string `json:"last_charge_error" gorm:"type:varchar(255);default:''"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint;not null"`
	UpdatedAt       int64  `json:"updated_at" gorm:"bigint;not null"`
}

func (UserAccelSub) TableName() string {
	return "user_accel_subs"
}

var ErrAccelPlanNotFound = errors.New("accel plan not found")

// Active 订阅在给定时刻是否可用。只认 active + 未到期，
// 不看 AutoRenew（关掉续费的用户在到期前都还能用）。
func (sub *UserAccelSub) Active(now int64) bool {
	if sub == nil || sub.Status != AccelSubStatusActive {
		return false
	}
	return sub.ExpireAt > now
}

func ListAccelPlans(enabledOnly bool) ([]AccelPlan, error) {
	plans := make([]AccelPlan, 0)
	query := DB.Model(&AccelPlan{})
	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}
	err := query.Order("sort_order ASC, id ASC").Find(&plans).Error
	return plans, err
}

func GetAccelPlanById(id int) (*AccelPlan, error) {
	var plan AccelPlan
	err := DB.Where("id = ?", id).First(&plan).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAccelPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

func CreateAccelPlan(plan *AccelPlan) error {
	now := time.Now().Unix()
	plan.CreatedAt = now
	plan.UpdatedAt = now
	return DB.Create(plan).Error
}

func UpdateAccelPlan(plan *AccelPlan) error {
	plan.UpdatedAt = time.Now().Unix()
	return DB.Model(plan).
		Select("name", "group_name", "price_per_month", "concurrency", "description", "enabled", "sort_order", "updated_at").
		Updates(plan).Error
}

func DeleteAccelPlan(id int) error {
	result := DB.Delete(&AccelPlan{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAccelPlanNotFound
	}
	return nil
}

// GetAccelPlanByGroup 找到某个分组对应的启用套餐（并发/额度校验用）。
func GetAccelPlanByGroup(groupName string) (*AccelPlan, error) {
	var plan AccelPlan
	err := DB.Where("group_name = ? AND enabled = ?", groupName, true).Order("id ASC").First(&plan).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAccelPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

func GetUserAccelSub(userId int) (*UserAccelSub, error) {
	var sub UserAccelSub
	err := DB.Where("user_id = ?", userId).First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func CreateUserAccelSub(sub *UserAccelSub) error {
	now := time.Now().Unix()
	sub.CreatedAt = now
	sub.UpdatedAt = now
	return DB.Create(sub).Error
}

// UpdateUserAccelSubFields 只更新显式给出的列，避免整体 Save 把并发读到的
// 旧值写回去（订阅状态是高频读、低频写）。
func UpdateUserAccelSubFields(userId int, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	fields["updated_at"] = time.Now().Unix()
	return DB.Model(&UserAccelSub{}).Where("user_id = ?", userId).Updates(fields).Error
}

func CountAccelSubs(status string) (int64, error) {
	var count int64
	query := DB.Model(&UserAccelSub{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	err := query.Count(&count).Error
	return count, err
}

// AccelSubRow 管理端列表用：订阅 + 用户名，一次 join 查完，
// 避免按 user_id 逐个回查用户名（订阅列表动辄几十行）。
type AccelSubRow struct {
	UserAccelSub
	Username string `json:"username" gorm:"column:username"`
}

func ListAccelSubs(offset, limit int) ([]AccelSubRow, int64, error) {
	rows := make([]AccelSubRow, 0)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	err := DB.Table("user_accel_subs AS s").
		Select("s.*, u.username AS username").
		Joins("LEFT JOIN users AS u ON u.id = s.user_id").
		Order("s.expire_at DESC, s.id DESC").
		Offset(offset).Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err := DB.Model(&UserAccelSub{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
