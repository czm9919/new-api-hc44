package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupStatementTest wires in-memory SQLite DBs into model.DB and
// model.LOG_DB for statement queries (v1.1.0 M1 · B26).
func setupStatementTest(t *testing.T) {
	t.Helper()
	originalDB, originalLogDB := model.DB, model.LOG_DB
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	logDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.TopUp{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	model.DB = database
	model.LOG_DB = logDB
	t.Cleanup(func() {
		model.DB, model.LOG_DB = originalDB, originalLogDB
	})
}

func callMonthlyStatement(t *testing.T, month string) MonthlyStatement {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/log/statement?month="+month, nil)
	c.Request.Header.Set("Content-Type", "application/json")
	GetMonthlyStatement(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Success bool             `json:"success"`
		Data    MonthlyStatement `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	return resp.Data
}

func TestParseStatementMonth(t *testing.T) {
	start, end, err := parseStatementMonth("2026-01")
	require.NoError(t, err)
	st := time.Unix(start, 0)
	en := time.Unix(end, 0)
	assert.Equal(t, time.January, st.Month())
	assert.Equal(t, 1, st.Day())
	assert.Equal(t, time.February, en.Month())
	assert.Equal(t, 1, en.Day())

	_, _, err = parseStatementMonth("2026-13")
	assert.Error(t, err)
	_, _, err = parseStatementMonth("bad")
	assert.Error(t, err)
}

func TestGetMonthlyStatementAggregatesRechargeConsumeRefundPending(t *testing.T) {
	setupStatementTest(t)

	month := time.Now().Format("2006-01")
	start, end, err := parseStatementMonth(month)
	require.NoError(t, err)
	// 任取月中两天（避开月底边界），保证都落在本月内。
	dayA := start + 3600
	dayB := start + 90000

	require.NoError(t, model.LOG_DB.Create(&model.Log{CreatedAt: dayA, Type: model.LogTypeTopup, Quota: 1000}).Error)
	require.NoError(t, model.LOG_DB.Create(&model.Log{CreatedAt: dayA, Type: model.LogTypeConsume, Quota: -400}).Error)
	require.NoError(t, model.LOG_DB.Create(&model.Log{CreatedAt: dayB, Type: model.LogTypeConsume, Quota: -600}).Error)
	require.NoError(t, model.LOG_DB.Create(&model.Log{CreatedAt: dayB, Type: model.LogTypeRefund, Quota: 50}).Error)
	// 窗口外日志不应计入
	require.NoError(t, model.LOG_DB.Create(&model.Log{CreatedAt: end + 10, Type: model.LogTypeConsume, Quota: -99999}).Error)
	require.NoError(t, model.LOG_DB.Create(&model.Log{CreatedAt: start - 10, Type: model.LogTypeTopup, Quota: 88888}).Error)

	require.NoError(t, model.DB.Create(&model.TopUp{TradeNo: "t-success-a", Amount: 10, Money: 12.5, CreateTime: dayA, Status: common.TopUpStatusSuccess}).Error)
	require.NoError(t, model.DB.Create(&model.TopUp{TradeNo: "t-success-b", Amount: 20, Money: 25.0, CreateTime: dayB, Status: common.TopUpStatusSuccess}).Error)
	require.NoError(t, model.DB.Create(&model.TopUp{TradeNo: "t-pending", Amount: 5, Money: 6.0, CreateTime: dayB, Status: common.TopUpStatusPending}).Error)

	stmt := callMonthlyStatement(t, month)

	assert.Equal(t, int64(1000), stmt.RechargeQuota)
	assert.Equal(t, int64(-1000), stmt.ConsumeQuota)
	assert.Equal(t, int64(50), stmt.RefundQuota)
	assert.Equal(t, int64(2), stmt.RechargeOrders)
	assert.InDelta(t, 37.5, stmt.RechargeMoney, 1e-6)
	assert.Equal(t, int64(1), stmt.PendingOrders)
	assert.InDelta(t, 6.0, stmt.PendingMoney, 1e-6)

	require.NotEmpty(t, stmt.Days)
	// 升序排列且各日小计命中
	var rechargeSeen, consumeSeen int64
	for _, d := range stmt.Days {
		rechargeSeen += d.RechargeQuota
		consumeSeen += d.ConsumeQuota
	}
	assert.Equal(t, stmt.RechargeQuota, rechargeSeen)
	assert.Equal(t, stmt.ConsumeQuota, consumeSeen)
}
