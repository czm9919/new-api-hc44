package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// 大转盘抽奖记录（v1.1.0 M2）
//
// 每次抽奖落一条记录，同时用 (user_id, day_key, spin_index) 唯一索引
// 兜住「每日次数上限」：第 N 次抽奖的 spin_index 必须是 N-1，并发抽奖
// 只有一个请求能抢到同一个下标，另一个会撞唯一索引后重试并看到已达上限。
// 这比「先 count 再 insert」更可靠，也不依赖进程内锁。
type LotterySpin struct {
	Id           int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId       int    `json:"user_id" gorm:"not null;uniqueIndex:idx_lottery_user_day_index"`
	DayKey       string `json:"day_key" gorm:"type:varchar(10);not null;uniqueIndex:idx_lottery_user_day_index"` // 格式 YYYY-MM-DD（本地时区）
	SpinIndex    int    `json:"spin_index" gorm:"not null;uniqueIndex:idx_lottery_user_day_index"`
	PrizeId      string `json:"prize_id" gorm:"type:varchar(32);not null"`
	PrizeName    string `json:"prize_name" gorm:"type:varchar(64);not null"`
	PrizeType    string `json:"prize_type" gorm:"type:varchar(16);not null"` // quota / none
	QuotaAwarded int    `json:"quota_awarded" gorm:"not null;default:0"`
	// Paid 本次是否为付费抽奖（扣了余额）；免费次数与付费次数分开计数，
	// 但共用同一个 spin_index 序列，避免唯一索引口径分裂。
	Paid bool `json:"paid" gorm:"not null;default:false;index:idx_lottery_paid"`
	// QuotaCost 本次实际扣掉的余额（免费抽奖恒为 0），退款与对账都靠它。
	QuotaCost int   `json:"quota_cost" gorm:"not null;default:0"`
	CreatedAt int64 `json:"created_at" gorm:"bigint;not null;index"`
}

func (LotterySpin) TableName() string {
	return "lottery_spins"
}

// LotteryDayKey 返回本地时区的日期键（与签到口径一致）。
func LotteryDayKey(t time.Time) string {
	return t.Format("2006-01-02")
}

// CountLotterySpinsOnDay 用户某天已抽奖次数（免费 + 付费，用于 spin_index 序列）。
func CountLotterySpinsOnDay(userId int, dayKey string) (int64, error) {
	var count int64
	err := DB.Model(&LotterySpin{}).
		Where("user_id = ? AND day_key = ?", userId, dayKey).
		Count(&count).Error
	return count, err
}

// CountLotteryPaidSpinsOnDay 用户某天已付费抽奖次数（付费每日上限用这个口径）。
func CountLotteryPaidSpinsOnDay(userId int, dayKey string) (int64, error) {
	var count int64
	err := DB.Model(&LotterySpin{}).
		Where("user_id = ? AND day_key = ? AND paid = ?", userId, dayKey, true).
		Count(&count).Error
	return count, err
}

// CreateLotterySpin 落一条抽奖记录；spin_index 冲突时返回 ErrLotterySpinConflict，
// 由上层重新取次数决定是「重试」还是「已达上限」。
var ErrLotterySpinConflict = errors.New("lottery spin index conflict")

func CreateLotterySpin(spin *LotterySpin) error {
	err := DB.Create(spin).Error
	if err == nil {
		return nil
	}
	// 唯一索引冲突（SQLite/MySQL/PG 的驱动错误文本不同，统一按文本判定，
	// 判不出来时按普通错误上抛，不会误放行）。
	if isUniqueConstraintError(err) {
		return ErrLotterySpinConflict
	}
	return err
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := err.Error()
	for _, needle := range []string{
		"UNIQUE constraint failed",
		"Duplicate entry",
		"duplicate key value violates unique constraint",
		"constraint failed: UNIQUE",
	} {
		if containsFold(msg, needle) {
			return true
		}
	}
	return false
}

func containsFold(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(needle) > len(haystack) {
		return false
	}
	lowerHay := []byte(haystack)
	lowerNeedle := []byte(needle)
	for i := range lowerHay {
		if lowerHay[i] >= 'A' && lowerHay[i] <= 'Z' {
			lowerHay[i] += 'a' - 'A'
		}
	}
	for i := range lowerNeedle {
		if lowerNeedle[i] >= 'A' && lowerNeedle[i] <= 'Z' {
			lowerNeedle[i] += 'a' - 'A'
		}
	}
	for i := 0; i+len(lowerNeedle) <= len(lowerHay); i++ {
		match := true
		for j := range lowerNeedle {
			if lowerHay[i+j] != lowerNeedle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// ListUserLotterySpins 用户最近 N 条抽奖记录（倒序）。
func ListUserLotterySpins(userId int, limit int) ([]LotterySpin, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var spins []LotterySpin
	err := DB.Where("user_id = ?", userId).
		Order("id DESC").
		Limit(limit).
		Find(&spins).Error
	return spins, err
}
