package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"gorm.io/gorm"
)

// 审计日志哈希链（v1.1.0 M1 · B78）。
//
// 每条记录写入前把「前一条的 Hash + 本条正文」一起做 SHA-256，结果存进
// Hash，并把前一条的 Hash 存进 PrevHash。这样：改任一条正文 → 该条 Hash
// 对不上；删掉中间某条 → 后一条的 PrevHash 对不上；插入伪造记录 → 无法
// 同时满足前后两条的链接。
//
// 链头的缓存放在进程内，避免每条审计都要 SELECT 一次；插入失败时回滚
// 缓存，保证后续记录不会链接到一条不存在于库中的记录。
//
// 已知边界：多节点共用同一个日志库时，各节点并发插入会按 id 顺序交错，
// 校验时可能报告「断点」。单节点（默认部署形态）下链严格连续；多节点
// 部署请把审计写入指向同一节点，或接受断点告警作为并发提示。
var (
	auditChainMu       sync.Mutex
	auditChainLastHash string
	auditChainLoaded   bool
	auditChainDB       *gorm.DB
)

// auditChainHead reads the hash of the newest stored row. Rows written before
// the chain existed carry an empty hash, so the head may legitimately be "".
func auditChainHead(db *gorm.DB) string {
	var hashes []string
	if err := db.Model(&AuditLog{}).
		Where("hash <> ''").
		Order("id desc").
		Limit(1).
		Pluck("hash", &hashes).Error; err != nil {
		return ""
	}
	if len(hashes) == 0 {
		return ""
	}
	return hashes[0]
}

// auditCanonicalPayload is the exact byte sequence covered by the hash. Every
// variable-length field is length-prefixed so no two different records can
// produce the same payload by moving bytes across field boundaries.
func auditCanonicalPayload(prevHash string, entry *AuditLog) string {
	return fmt.Sprintf("prev=%s\nevent=%d:%s\ncategory=%d:%s\naction=%d:%s\nip=%d:%s\nuser=%d:%d\nsuccess=%t\ncontent=%d:%s\ncreated=%d",
		prevHash,
		len(entry.EventId), entry.EventId,
		len(entry.Category), entry.Category,
		len(entry.Action), entry.Action,
		len(entry.Ip), entry.Ip,
		entry.UserId, entry.UserId,
		entry.Success,
		len(entry.Content), entry.Content,
		entry.CreatedAt,
	)
}

// sealAuditEntry fills PrevHash/Hash for a new entry. Callers must hold
// auditChainMu so concurrent writers cannot fork the chain.
func sealAuditEntry(db *gorm.DB, entry *AuditLog) string {
	// 缓存里额外记住当时的 DB 句柄：测试会在同一个进程里换库，句柄变了
	// 就必须重新读链头，否则新库的第一条记录会把 PrevHash 指向上一个库。
	if !auditChainLoaded || auditChainDB != db {
		auditChainLastHash = auditChainHead(db)
		auditChainLoaded = true
		auditChainDB = db
	}
	entry.PrevHash = auditChainLastHash
	digest := sha256.Sum256([]byte(auditCanonicalPayload(entry.PrevHash, entry)))
	entry.Hash = hex.EncodeToString(digest[:])
	return entry.Hash
}

// AuditChainBreak describes one integrity violation found while replaying the
// chain.
type AuditChainBreak struct {
	Id      int    `json:"id"`
	EventId string `json:"event_id"`
	Reason  string `json:"reason"`
}

// AuditChainReport is the verification outcome returned to the operator.
type AuditChainReport struct {
	Checked    int               `json:"checked"`
	Breaks     []AuditChainBreak `json:"breaks"`
	HeadHash   string            `json:"head_hash"`
	LegacyRows int               `json:"legacy_rows"` // rows written before the chain existed
}

// VerifyAuditChain replays every hashed row in id order and reports the rows
// whose recomputed hash or link does not match. limit caps the number of rows
// scanned (0 means all) so a huge table cannot stall the admin API.
func VerifyAuditChain(limit int) (*AuditChainReport, error) {
	report := &AuditChainReport{Breaks: []AuditChainBreak{}}
	var prevHash string
	seenFirst := false
	var lastId int
	batchSize := 500
	for {
		var rows []*AuditLog
		query := LOG_DB.Model(&AuditLog{}).Where("id > ?", lastId).Order("id asc").Limit(batchSize)
		if err := query.Find(&rows).Error; err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			lastId = row.Id
			if row.Hash == "" {
				report.LegacyRows++
				continue
			}
			report.Checked++
			expected := sha256.Sum256([]byte(auditCanonicalPayload(row.PrevHash, row)))
			if hex.EncodeToString(expected[:]) != row.Hash {
				report.Breaks = append(report.Breaks, AuditChainBreak{Id: row.Id, EventId: row.EventId, Reason: "content hash mismatch"})
			} else if seenFirst && row.PrevHash != prevHash {
				report.Breaks = append(report.Breaks, AuditChainBreak{Id: row.Id, EventId: row.EventId, Reason: "previous hash mismatch"})
			}
			seenFirst = true
			prevHash = row.Hash
			report.HeadHash = row.Hash
			if limit > 0 && report.Checked >= limit {
				return report, nil
			}
		}
	}
	return report, nil
}
