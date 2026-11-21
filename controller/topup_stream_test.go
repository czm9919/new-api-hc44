package controller

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/topupevents"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupTopUpStreamTestDB 建一个只含 TopUp 表的库并接到 model.DB 上。
func setupTopUpStreamTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}))
	previous := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previous
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
}

// readSSEData 读下一帧 data: 行，超时视为失败（避免 SSE 测试挂死）。
func readSSEData(t *testing.T, reader *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- result{err: err}
				return
			}
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "data: ") {
				done <- result{line: strings.TrimPrefix(line, "data: ")}
				return
			}
		}
	}()
	select {
	case got := <-done:
		require.NoError(t, got.err)
		return got.line
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for an SSE data frame")
		return ""
	}
}

type topUpStreamPayload struct {
	Orders []struct {
		TradeNo string `json:"trade_no"`
		Status  string `json:"status"`
	} `json:"orders"`
}

// B14：连上即推首帧快照；本实例发生的状态迁移通过 hub 立刻推到已连接的客户端。
func TestStreamTopUpStatusPushesSnapshotAndLiveUpdates(t *testing.T) {
	setupTopUpStreamTestDB(t)
	const userId = 7
	createTime := time.Now().Unix()
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId:     userId,
		TradeNo:    "trade-pending",
		Amount:     10,
		Money:      10,
		CreateTime: createTime,
		Status:     common.TopUpStatusPending,
	}).Error)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/user/topup/stream", func(c *gin.Context) {
		c.Set("id", userId)
		StreamTopUpStatus(c)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/user/topup/stream")
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	require.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	assert.Equal(t, "no", response.Header.Get("X-Accel-Buffering"), "proxies must not buffer the stream")

	reader := bufio.NewReader(response.Body)
	var first topUpStreamPayload
	require.NoError(t, json.Unmarshal([]byte(readSSEData(t, reader, 3*time.Second)), &first))
	require.Len(t, first.Orders, 1)
	assert.Equal(t, "trade-pending", first.Orders[0].TradeNo)
	assert.Equal(t, common.TopUpStatusPending, first.Orders[0].Status)

	// 模拟支付回调：库里状态迁移 +（同实例）发布事件。
	require.NoError(t, model.DB.Model(&model.TopUp{}).
		Where("trade_no = ?", "trade-pending").
		Updates(map[string]any{"status": common.TopUpStatusSuccess, "complete_time": time.Now().Unix()}).Error)
	topupevents.Publish(userId, topupevents.Event{TradeNo: "trade-pending", Status: common.TopUpStatusSuccess})

	var second topUpStreamPayload
	require.NoError(t, json.Unmarshal([]byte(readSSEData(t, reader, 3*time.Second)), &second))
	require.Len(t, second.Orders, 1)
	assert.Equal(t, common.TopUpStatusSuccess, second.Orders[0].Status)
}

// B14：未认证请求不建立流，直接 401 —— 订单流按用户隔离，不能有匿名订阅。
func TestStreamTopUpStatusRejectsAnonymous(t *testing.T) {
	setupTopUpStreamTestDB(t)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/topup/stream", nil)

	StreamTopUpStatus(ctx)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

// B14：只推最近窗口内的订单；更早的历史订单不进流，避免每次连接都推全量。
func TestTopUpStatusSnapshotOnlyReturnsRecentOrders(t *testing.T) {
	setupTopUpStreamTestDB(t)
	now := time.Now()
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId: 9, TradeNo: "old", CreateTime: now.Add(-2 * time.Hour).Unix(), Status: common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId: 9, TradeNo: "fresh", CreateTime: now.Unix(), Status: common.TopUpStatusPending,
	}).Error)

	orders, err := topUpStatusSnapshot(9)
	require.NoError(t, err)
	require.Len(t, orders, 1)
	assert.Equal(t, "fresh", orders[0].TradeNo)

	snapshot := marshalTopUpSnapshot(nil)
	assert.Equal(t, `{"orders":[]}`, snapshot, "empty snapshot must be an empty array, not null")
}
