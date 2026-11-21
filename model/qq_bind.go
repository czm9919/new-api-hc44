package model

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// QQ 绑定（v1.1.0 M2 · 抽奖/免费模型门禁）
//
// 设计：面板不直接接触 QQ 账号密码，用户在面板生成一次性绑定码，
// 把绑定码发给配套 AstrBot 机器人私聊（机器人侧调用
// POST /api/qq/agent/verify 携带 QQ 号 + 绑定码），服务端核对绑定码
// 后把 QQ 号写进 users.qq。这样：
//  1. QQ 号由机器人侧「谁在发消息」得出，面板无法伪造
//  2. 用户不需要把 QQ 密码或 Cookie 交给站点
//  3. 绑定码一次性、限时、可吊销，泄露风险可控

const (
	// QQBindCodeTTL 绑定码有效期：10 分钟足够用户复制粘贴到机器人。
	QQBindCodeTTL = 10 * time.Minute
	// QQBindCodeLength 绑定码长度（去掉易混字符的 base32 子集）。
	QQBindCodeLength = 8
	// QQBindCodeAttempts 生成绑定码时的候选尝试次数（唯一索引冲突重试）。
	QQBindCodeAttempts = 8
)

// qqBindCodeAlphabet 去掉 0/O/1/I 等易混字符，避免用户抄错。
const qqBindCodeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// QQBindCode 一次性 QQ 绑定码。
type QQBindCode struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId    int    `json:"user_id" gorm:"not null;index"`
	Code      string `json:"code" gorm:"type:varchar(16);not null;uniqueIndex"`
	ExpiresAt int64  `json:"expires_at" gorm:"bigint;not null"`
	UsedAt    int64  `json:"used_at" gorm:"bigint;default:0"`
	CreatedAt int64  `json:"created_at" gorm:"bigint;not null"`
}

func (QQBindCode) TableName() string {
	return "qq_bind_codes"
}

// ErrQQBindCodeInvalid 绑定码不存在 / 已使用 / 已过期统一对外表述，
// 避免泄露「这个码存在但过期了」这类信息。
var ErrQQBindCodeInvalid = errors.New("bind code is invalid or expired")

// ErrQQAlreadyBound 目标 QQ 已绑定到另一个账号。
var ErrQQAlreadyBound = errors.New("this QQ is already bound to another account")

// ErrQQBindAccountAlreadyBound 当前账号已绑定其它 QQ。
var ErrQQBindAccountAlreadyBound = errors.New("this account is already bound to another QQ")

// NormalizeQQ 规范化 QQ 号：去空白，只接受 5-12 位纯数字。
func NormalizeQQ(raw string) (string, error) {
	qq := strings.TrimSpace(raw)
	if qq == "" {
		return "", errors.New("QQ number is required")
	}
	for _, r := range qq {
		if r < '0' || r > '9' {
			return "", errors.New("QQ number must be digits only")
		}
	}
	if len(qq) < 5 || len(qq) > 12 {
		return "", errors.New("QQ number length must be between 5 and 12")
	}
	return qq, nil
}

func randomQQBindCode() (string, error) {
	buf := make([]byte, QQBindCodeLength)
	max := big.NewInt(int64(len(qqBindCodeAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = qqBindCodeAlphabet[n.Int64()]
	}
	return string(buf), nil
}

// CreateQQBindCode 为用户生成一次性绑定码；旧的未使用码一并作废。
func CreateQQBindCode(userId int) (*QQBindCode, error) {
	if userId <= 0 {
		return nil, errors.New("invalid user id")
	}
	now := time.Now()
	// 同一账号只保留最新一个有效码，降低「旧码被冒用」的窗口。
	if err := DB.Where("user_id = ? AND used_at = 0", userId).Delete(&QQBindCode{}).Error; err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < QQBindCodeAttempts; attempt++ {
		code, err := randomQQBindCode()
		if err != nil {
			return nil, err
		}
		record := &QQBindCode{
			UserId:    userId,
			Code:      code,
			ExpiresAt: now.Add(QQBindCodeTTL).Unix(),
			CreatedAt: now.Unix(),
		}
		if err := DB.Create(record).Error; err != nil {
			lastErr = err
			continue
		}
		return record, nil
	}
	return nil, fmt.Errorf("failed to allocate bind code: %w", lastErr)
}

// EnsureUserQQBackfilled 把历史行的 users.qq 从 NULL 归一成空串。
//
// AutoMigrate 在 SQLite 上加列时只会给「之后插入」的行补默认值，老行的 qq
// 仍然是 NULL；而 User.QQ 是 string，任何 `Select("qq")` 扫到 NULL 都会报
// "converting NULL to string is unsupported"，表现为老用户打不开抽奖/绑定页。
// 所以每次启动都做一次幂等回填。
func EnsureUserQQBackfilled() error {
	if err := DB.Exec("UPDATE users SET qq = '' WHERE qq IS NULL").Error; err != nil {
		return fmt.Errorf("backfill users.qq: %w", err)
	}
	return nil
}

// GetUserQQ 返回用户已绑定的 QQ（未绑定为空串）。
func GetUserQQ(userId int) (string, error) {
	var qq string
	err := DB.Model(&User{}).Where("id = ?", userId).
		Select("COALESCE(qq, '') AS qq").Take(&qq).Error
	return qq, err
}

// IsQQBound 用户是否已绑定 QQ。
func IsQQBound(userId int) (bool, error) {
	qq, err := GetUserQQ(userId)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(qq) != "", nil
}

// CountQQBoundUsers 已绑定 QQ 的用户数（管理端概览用）。
func CountQQBoundUsers() (int64, error) {
	var count int64
	err := DB.Model(&User{}).
		Where("COALESCE(qq, '') <> ''").
		Count(&count).Error
	return count, err
}

// CountActiveQQBindCodes 仍未使用的绑定码数量（管理端概览用）。
func CountActiveQQBindCodes(now time.Time) (int64, error) {
	var count int64
	err := DB.Model(&QQBindCode{}).
		Where("used_at = 0 AND expires_at > ?", now.Unix()).
		Count(&count).Error
	return count, err
}

// QQBindResult 绑定成功后的回执（供机器人回复用户）。
type QQBindResult struct {
	UserId   int    `json:"user_id"`
	Username string `json:"username"`
	QQ       string `json:"qq"`
	BoundAt  int64  `json:"bound_at"`
}

// BindQQByCode 用绑定码完成 QQ 绑定。
//
// 并发安全：先无条件把该码标记为已使用（UPDATE ... WHERE used_at = 0），
// 只有抢到这一次更新的请求才继续写 users.qq，避免同一个码被两个 QQ 同时用掉。
func BindQQByCode(code, rawQQ string) (*QQBindResult, error) {
	normalizedCode := strings.ToUpper(strings.TrimSpace(code))
	if normalizedCode == "" {
		return nil, ErrQQBindCodeInvalid
	}
	qq, err := NormalizeQQ(rawQQ)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()

	var bindCode QQBindCode
	if err := DB.Where("code = ?", normalizedCode).Take(&bindCode).Error; err != nil {
		return nil, ErrQQBindCodeInvalid
	}
	if bindCode.UsedAt != 0 || bindCode.ExpiresAt <= now {
		return nil, ErrQQBindCodeInvalid
	}
	res := DB.Model(&QQBindCode{}).
		Where("id = ? AND used_at = 0", bindCode.Id).
		Update("used_at", now)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected != 1 {
		return nil, ErrQQBindCodeInvalid
	}

	var user User
	if err := DB.Where("id = ?", bindCode.UserId).Take(&user).Error; err != nil {
		return nil, err
	}
	if user.Status != common.UserStatusEnabled {
		return nil, errors.New("account is disabled")
	}
	currentQQ := strings.TrimSpace(user.QQ)
	if currentQQ != "" && currentQQ != qq {
		return nil, ErrQQBindAccountAlreadyBound
	}

	var conflict int64
	if err := DB.Model(&User{}).
		Where("qq = ? AND id <> ?", qq, user.Id).
		Count(&conflict).Error; err != nil {
		return nil, err
	}
	if conflict > 0 {
		return nil, ErrQQAlreadyBound
	}

	if currentQQ == qq {
		// 重复绑定同一个 QQ：幂等返回，不重复刷新绑定时间。
		return &QQBindResult{UserId: user.Id, Username: user.Username, QQ: qq, BoundAt: user.QQBoundAt}, nil
	}

	if err := DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"qq":          qq,
		"qq_bound_at": now,
	}).Error; err != nil {
		return nil, err
	}

	return &QQBindResult{UserId: user.Id, Username: user.Username, QQ: qq, BoundAt: now}, nil
}

// GetUserByQQ 用 QQ 号反查绑定账号。
//
// 机器人侧执行「余额 / 签到 / 用量」这类自助命令时，手里只有发消息的 QQ 号，
// 需要它定位账号。查不到返回 gorm.ErrRecordNotFound，调用方据此回「还未绑定」。
func GetUserByQQ(rawQQ string) (*User, error) {
	qq, err := NormalizeQQ(rawQQ)
	if err != nil {
		return nil, err
	}
	var user User
	if err := DB.Where("qq = ?", qq).Take(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// UnbindQQ 解除当前账号的 QQ 绑定。绑定码不受影响（旧码已作废）。
func UnbindQQ(userId int) error {
	return DB.Model(&User{}).Where("id = ?", userId).Updates(map[string]any{
		"qq":          "",
		"qq_bound_at": 0,
	}).Error
}
