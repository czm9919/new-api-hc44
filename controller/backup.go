package controller

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ExportDatabaseBackup 导出一份主库（或日志库）的一致性快照
// （v1.1.0 M2 · B13）。
//
// 为什么不是直接复制 .db 文件：SQLite 开了 WAL，直接 cp 会漏掉还没 checkpoint
// 的日志页，也可能复制到写事务中间态。`VACUUM INTO` 由 SQLite 自己在读事务里
// 生成一份整理过的、自洽的库文件，等价于在线热备。
//
// 只对 SQLite 提供：MySQL/PostgreSQL 的快照应由 mysqldump / pg_dump 或云厂商
// 快照承担，用 Go 进程流式导出大库会把内存和连接都拖死。这里显式返回 501，
// 而不是给出一个「看起来能点、实际会超时」的按钮。
func ExportDatabaseBackup(c *gin.Context) {
	scope := strings.TrimSpace(c.Query("scope"))
	db := model.DB
	dbLabel := "main"
	if scope == "log" {
		if !common.UsingLogDatabase(common.DatabaseTypeSQLite) {
			common.ApiErrorStatus(c, http.StatusNotImplemented, common.NewMessage("log database backup is only available for SQLite"))
			return
		}
		db = model.LOG_DB
		dbLabel = "log"
	} else if !common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		common.ApiErrorStatus(c, http.StatusNotImplemented, common.NewMessage("database backup is only available for SQLite; use mysqldump / pg_dump for server databases"))
		return
	}
	if db == nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("database is not initialized"))
		return
	}

	tmpFile, err := os.CreateTemp("", "new-api-backup-*.db")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	backupPath := tmpFile.Name()
	_ = tmpFile.Close()
	// VACUUM INTO 要求目标文件不存在，CreateTemp 已经创建了它，先删掉。
	if err := os.Remove(backupPath); err != nil {
		common.ApiError(c, err)
		return
	}
	defer os.Remove(backupPath)

	// 路径来自 os.CreateTemp，理论上不含单引号；仍然转义一次，避免任何
	// 平台差异导致的 SQL 注入面（这里不接受用户传入的路径）。
	sqlPath := strings.ReplaceAll(backupPath, "'", "''")
	if err := db.Exec(fmt.Sprintf("VACUUM INTO '%s'", sqlPath)).Error; err != nil {
		common.SysError(common.LogText("database backup failed: %s", err.Error()))
		common.ApiError(c, err)
		return
	}

	info, err := os.Stat(backupPath)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, 0, "database.backup", map[string]any{
		"scope": dbLabel,
		"bytes": info.Size(),
	})
	filename := fmt.Sprintf("new-api-%s-%s.db", dbLabel, time.Now().Format("20060102-150405"))
	c.FileAttachment(backupPath, filename)
}
