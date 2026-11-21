package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// ModelNotice 模型公告位（B64）：模型下架/停服/维护通知，带生效窗口。
// ModelName 为空表示全站公告；Level 决定前端颜色（info/warning/deprecation）。
type ModelNotice struct {
	Id         int    `json:"id"`
	ModelName  string `json:"model_name" gorm:"type:varchar(128);index;default:''"`
	Level      string `json:"level" gorm:"type:varchar(16);default:'info'"`
	Title      string `json:"title" gorm:"type:varchar(255)"`
	Content    string `json:"content" gorm:"type:text"`
	StartTime  int64  `json:"start_time" gorm:"bigint;default:0"`
	EndTime    int64  `json:"end_time" gorm:"bigint;default:0"`
	Enabled    bool   `json:"enabled" gorm:"default:true"`
	CreateTime int64  `json:"create_time" gorm:"bigint"`
}

const (
	ModelNoticeLevelInfo        = "info"
	ModelNoticeLevelWarning     = "warning"
	ModelNoticeLevelDeprecation = "deprecation"
)

func (n *ModelNotice) validate() error {
	if strings.TrimSpace(n.Title) == "" {
		return errors.New("公告标题不能为空")
	}
	switch n.Level {
	case ModelNoticeLevelInfo, ModelNoticeLevelWarning, ModelNoticeLevelDeprecation:
	case "":
		n.Level = ModelNoticeLevelInfo
	default:
		return errors.New("公告级别只支持 info / warning / deprecation")
	}
	if n.StartTime > 0 && n.EndTime > 0 && n.EndTime <= n.StartTime {
		return errors.New("公告结束时间必须晚于开始时间")
	}
	return nil
}

// noticeActiveClause 生效条件：启用 + 已开始（或未设开始时间）+ 未结束（或未设结束时间）。
func noticeActiveClause(now int64) (string, []any) {
	return "enabled = ? AND (start_time = 0 OR start_time <= ?) AND (end_time = 0 OR end_time >= ?)",
		[]any{true, now, now}
}

// GetActiveModelNotices 返回当前生效的公告。modelName 为空只取全站公告；
// 非空时返回全站公告 + 该模型（含通配前缀匹配，如 openai/*）的公告。
func GetActiveModelNotices(modelName string, limit int) ([]*ModelNotice, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	clause, args := noticeActiveClause(common.GetTimestamp())
	query := DB.Where(clause, args...)
	if strings.TrimSpace(modelName) != "" {
		candidates := []string{"", modelName, FormatWildcardNoticeModel(modelName)}
		query = query.Where("model_name IN ?", dedupeStrings(candidates))
	} else {
		query = query.Where("model_name = ''")
	}
	var notices []*ModelNotice
	err := query.Order("create_time DESC").Limit(limit).Find(&notices).Error
	return notices, err
}

// FormatWildcardNoticeModel 把 openai/gpt-4o 归一成 openai/*，用于按厂商批量发公告。
func FormatWildcardNoticeModel(modelName string) string {
	idx := strings.Index(modelName, "/")
	if idx <= 0 {
		return modelName
	}
	return modelName[:idx] + "/*"
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// GetAllModelNotices 后台列表，含未生效与已过期。
func GetAllModelNotices() ([]*ModelNotice, error) {
	var notices []*ModelNotice
	err := DB.Order("create_time DESC").Find(&notices).Error
	return notices, err
}

func InsertModelNotice(notice *ModelNotice) error {
	if err := notice.validate(); err != nil {
		return err
	}
	notice.CreateTime = common.GetTimestamp()
	// Enabled 带 default:true 标签，GORM 会省略零值字段，导致「新建即禁用」永远写成 true。
	// 这里先记住调用方意图，创建后按意图回写一次，保证 enabled=false 能真正落库。
	desiredEnabled := notice.Enabled
	if err := DB.Create(notice).Error; err != nil {
		return err
	}
	if desiredEnabled {
		return nil
	}
	if err := DB.Model(&ModelNotice{}).Where("id = ?", notice.Id).
		Update("enabled", false).Error; err != nil {
		return err
	}
	notice.Enabled = false
	return nil
}

func UpdateModelNotice(notice *ModelNotice) error {
	if notice.Id <= 0 {
		return errors.New("公告不存在")
	}
	if err := notice.validate(); err != nil {
		return err
	}
	var existing ModelNotice
	if err := DB.First(&existing, notice.Id).Error; err != nil {
		return errors.New("公告不存在")
	}
	existing.ModelName = notice.ModelName
	existing.Level = notice.Level
	existing.Title = notice.Title
	existing.Content = notice.Content
	existing.StartTime = notice.StartTime
	existing.EndTime = notice.EndTime
	existing.Enabled = notice.Enabled
	return DB.Save(&existing).Error
}

func DeleteModelNotice(id int) error {
	result := DB.Delete(&ModelNotice{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("公告不存在")
	}
	return nil
}
