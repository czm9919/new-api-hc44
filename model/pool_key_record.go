package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// PoolKeyRecord 号池密钥贡献台账。
//
// 号池把多把上游密钥合并进同一个「号池渠道」，渠道里只保留 key 字符串本身，
// 无法回答两个关键问题：
//  1. 这把 key 是不是已经有人贡献过？（同一把 key 重复贡献等于虚增号池容量）
//  2. 这个上游账号是不是已经贡献过？（同一账号下新建的第二把 key 并不会增加
//     上游的真实额度，只是把同一个账号重复计入号池）
//
// 因此这里单独记一份「谁在什么时候用哪个上游账号贡献了哪把 key」的台账：
//   - KeyHash 只存 SHA-256 指纹，不落明文，避免数据库泄露直接带走上游密钥；
//   - (Provider, KeyHash) 唯一，保证同一把 key 全局只能存在一条有效记录；
//   - (Provider, AccountKey) 用于拦截同一上游账号的重复贡献。
type PoolKeyRecord struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Provider string `json:"provider" gorm:"type:varchar(32);not null;uniqueIndex:idx_pool_key_provider_hash"`
	// KeyHash 上游密钥的 SHA-256 指纹（不存明文）
	KeyHash string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_pool_key_provider_hash"`
	// KeyMask 脱敏后的密钥，仅用于后台展示与错误提示
	KeyMask string `json:"key_mask" gorm:"type:varchar(64);not null"`
	// AccountKey 归一化后的上游账号标识（小写、去空白），用于判重
	AccountKey string `json:"account_key" gorm:"type:varchar(191);not null;default:'';index:idx_pool_key_provider_account"`
	// AccountLabel 用户填写的原始账号标识（保留大小写，仅展示）
	AccountLabel string `json:"account_label" gorm:"type:varchar(191);not null;default:''"`
	// ContributorId 贡献者用户 ID
	ContributorId int `json:"contributor_id" gorm:"not null;index"`
	// ChannelId 该 key 最终并入的号池渠道 ID
	ChannelId int `json:"channel_id" gorm:"not null;default:0"`
	// CreatedTime 首次贡献时间
	CreatedTime int64 `json:"created_time" gorm:"bigint"`
	// Status 1=在池中 0=已从号池移除
	Status int `json:"status" gorm:"not null;default:1"`
}

func (PoolKeyRecord) TableName() string {
	return "pool_key_records"
}

// PoolKeyHash 计算上游密钥指纹。密钥本身按原样参与计算（不做 trim 以外的大小写变换，
// 上游密钥区分大小写），调用方负责先去空白。
func PoolKeyHash(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])
}

// NormalizePoolAccountKey 归一化上游账号标识：去首尾空白、内部连续空白压成一个、转小写。
// 这样「Zhang@Gmail.com」「zhang@gmail.com 」「 zhang@gmail. com 」不会被当成三个账号。
func NormalizePoolAccountKey(account string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(account)))
	return strings.Join(fields, "")
}

// GetPoolKeyRecordByHash 按指纹查台账。未找到返回 (nil, nil)。
func GetPoolKeyRecordByHash(provider string, keyHash string) (*PoolKeyRecord, error) {
	var rec PoolKeyRecord
	err := DB.Where("provider = ? AND key_hash = ?", provider, keyHash).First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

// GetActivePoolKeyRecordByAccount 按上游账号查台账中仍有效的记录。
//
// excludeHash 用于把自己排除在判重之外（重新提交同一把 key 时关心的是「同账号的其它 key」）。
// 未找到返回 (nil, nil)。
func GetActivePoolKeyRecordByAccount(provider string, accountKey string, excludeHash string) (*PoolKeyRecord, error) {
	if strings.TrimSpace(accountKey) == "" {
		return nil, nil
	}
	query := DB.Where("provider = ? AND account_key = ? AND status = ?", provider, accountKey, 1)
	if excludeHash != "" {
		query = query.Where("key_hash <> ?", excludeHash)
	}
	var rec PoolKeyRecord
	err := query.Order("id asc").First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

// ListPoolKeyRecords 返回某供应商的贡献台账（后台运营视图用），按贡献时间倒序。
func ListPoolKeyRecords(provider string, limit int) ([]PoolKeyRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	query := DB.Model(&PoolKeyRecord{})
	if strings.TrimSpace(provider) != "" {
		query = query.Where("provider = ?", provider)
	}
	var records []PoolKeyRecord
	err := query.Order("id desc").Limit(limit).Find(&records).Error
	return records, err
}

// ListPoolKeyRecordsByContributor 返回某个贡献者自己的台账记录（用户侧展示用）。
func ListPoolKeyRecordsByContributor(userId int, provider string, limit int) ([]PoolKeyRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := DB.Model(&PoolKeyRecord{}).Where("contributor_id = ?", userId)
	if strings.TrimSpace(provider) != "" {
		query = query.Where("provider = ?", provider)
	}
	var records []PoolKeyRecord
	err := query.Order("id desc").Limit(limit).Find(&records).Error
	return records, err
}

// UpsertPoolKeyRecord 写入/更新一条台账记录。
//
// 同一把 key 再次贡献（例如被移除后又加回来）时复用原行，只刷新账号、贡献者、
// 渠道与状态，避免触发唯一索引冲突，也保留首次贡献时间。
func UpsertPoolKeyRecord(rec *PoolKeyRecord) error {
	if rec == nil {
		return nil
	}
	existing, err := GetPoolKeyRecordByHash(rec.Provider, rec.KeyHash)
	if err != nil {
		return err
	}
	if existing == nil {
		if rec.CreatedTime == 0 {
			rec.CreatedTime = common.GetTimestamp()
		}
		if rec.Status == 0 {
			rec.Status = 1
		}
		return DB.Create(rec).Error
	}
	updates := map[string]any{
		"key_mask":       rec.KeyMask,
		"status":         1,
		"channel_id":     rec.ChannelId,
		"contributor_id": rec.ContributorId,
	}
	// 账号标识只在新值非空时覆盖，避免后台批量补录时把已有账号清空。
	if strings.TrimSpace(rec.AccountKey) != "" {
		updates["account_key"] = rec.AccountKey
		updates["account_label"] = rec.AccountLabel
	}
	return DB.Model(&PoolKeyRecord{}).Where("id = ?", existing.Id).Updates(updates).Error
}

// ClearPoolKeyRecordAccount 把某条台账的账号标识清空（后台发现填错时用）。
func ClearPoolKeyRecordAccount(id int) error {
	return DB.Model(&PoolKeyRecord{}).Where("id = ?", id).Updates(map[string]any{
		"account_key":   "",
		"account_label": "",
	}).Error
}
