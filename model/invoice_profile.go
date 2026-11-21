package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// InvoiceProfile 买家发票抬头（B99）：企业名称/税号/地址电话/开户行账号。
// 一个用户可保存多份抬头（个人、不同公司），标记一份默认，下单开票时直接选用。
type InvoiceProfile struct {
	Id          int    `json:"id"`
	UserId      int    `json:"user_id" gorm:"index"`
	Name        string `json:"name" gorm:"type:varchar(255)"`                  // 抬头名称（个人姓名或企业全称）
	Type        string `json:"type" gorm:"type:varchar(16);default:'company'"` // personal / company
	TaxId       string `json:"tax_id" gorm:"type:varchar(64);default:''"`      // 纳税人识别号
	Address     string `json:"address" gorm:"type:varchar(512);default:''"`    // 注册地址
	Phone       string `json:"phone" gorm:"type:varchar(32);default:''"`
	BankName    string `json:"bank_name" gorm:"type:varchar(128);default:''"`
	BankAccount string `json:"bank_account" gorm:"type:varchar(64);default:''"`
	IsDefault   bool   `json:"is_default" gorm:"default:false"`
	CreateTime  int64  `json:"create_time" gorm:"bigint"`
}

func (p *InvoiceProfile) validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("发票抬头名称不能为空")
	}
	if p.Type != "personal" && p.Type != "company" {
		return errors.New("发票抬头类型只支持 personal 或 company")
	}
	if p.Type == "company" && strings.TrimSpace(p.TaxId) == "" {
		return errors.New("企业抬头必须填写纳税人识别号")
	}
	return nil
}

// GetUserInvoiceProfiles 返回用户全部抬头，默认抬头排最前，其余按创建时间倒序。
func GetUserInvoiceProfiles(userId int) ([]*InvoiceProfile, error) {
	var profiles []*InvoiceProfile
	err := DB.Where("user_id = ?", userId).
		Order("is_default DESC, create_time DESC").
		Find(&profiles).Error
	return profiles, err
}

// InsertInvoiceProfile 创建抬头。设为默认时先取消该用户原有默认。
func InsertInvoiceProfile(profile *InvoiceProfile) error {
	if err := profile.validate(); err != nil {
		return err
	}
	if profile.UserId <= 0 {
		return errors.New("invalid user")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if profile.IsDefault {
			if err := tx.Model(&InvoiceProfile{}).Where("user_id = ?", profile.UserId).
				Update("is_default", false).Error; err != nil {
				return err
			}
		}
		profile.CreateTime = common.GetTimestamp()
		return tx.Create(profile).Error
	})
}

// UpdateInvoiceProfile 更新本人抬头；所有权校验由调用方通过 userId 条件保证。
func UpdateInvoiceProfile(userId int, profile *InvoiceProfile) error {
	if err := profile.validate(); err != nil {
		return err
	}
	var existing InvoiceProfile
	if err := DB.Where("id = ? AND user_id = ?", profile.Id, userId).First(&existing).Error; err != nil {
		return errors.New("发票抬头不存在")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if profile.IsDefault && !existing.IsDefault {
			if err := tx.Model(&InvoiceProfile{}).Where("user_id = ? AND id <> ?", userId, profile.Id).
				Update("is_default", false).Error; err != nil {
				return err
			}
		}
		existing.Name = profile.Name
		existing.Type = profile.Type
		existing.TaxId = profile.TaxId
		existing.Address = profile.Address
		existing.Phone = profile.Phone
		existing.BankName = profile.BankName
		existing.BankAccount = profile.BankAccount
		existing.IsDefault = profile.IsDefault
		return tx.Save(&existing).Error
	})
}

// DeleteInvoiceProfile 删除本人抬头；若删的是默认抬头则把最近一份补为默认。
func DeleteInvoiceProfile(userId int, id int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND user_id = ?", id, userId).Delete(&InvoiceProfile{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("发票抬头不存在")
		}
		var remaining InvoiceProfile
		if err := tx.Where("user_id = ?", userId).Order("create_time DESC").First(&remaining).Error; err != nil {
			return nil // 一份都不剩了，无需补默认
		}
		var defaultCount int64
		if err := tx.Model(&InvoiceProfile{}).Where("user_id = ?", userId).
			Where("is_default = ?", true).Count(&defaultCount).Error; err != nil {
			return err
		}
		if defaultCount > 0 {
			return nil // 还有默认抬头在，无需补位
		}
		return tx.Model(&remaining).Update("is_default", true).Error
	})
}
