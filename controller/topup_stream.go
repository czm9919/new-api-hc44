package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/topupevents"

	"github.com/gin-gonic/gin"
)

// 订单状态推送（v1.1.0 M1 · B14）。
//
// 仓库里原来的 SSE 全部是上游流式中继，订单状态只能前端轮询。这里给
// 支付页一条真正的推送通道：
//
//	GET /api/user/topup/stream   (UserAuth)
//	→ data: {"orders":[{trade_no,status,amount,money,create_time,complete_time}, ...]}
//
// 三条设计约束：
//  1. 以库为准。每帧都是「该用户最近订单的完整快照」，前端做全量替换即可，
//     不需要自己维护增量合并逻辑，也不会因为丢一帧而状态错乱。
//  2. 双通道触发。本实例完成状态迁移时 topupevents 立刻唤醒推送（毫秒级）；
//     同时每 2 秒对一次库，兜住「支付回调落在另一台实例」的多实例场景。
//  3. 只推最近窗口内的订单（默认 30 分钟），避免把历史订单整份推下去；
//     快照未变化时不发送，节省带宽。

const (
	topUpStreamWindow    = 30 * time.Minute
	topUpStreamPollTick  = 2 * time.Second
	topUpStreamHeartbeat = 15 * time.Second
	topUpStreamMaxOrders = 20
)

// topUpStatusSnapshot 读取用户最近窗口内的订单快照。
// create_time 走 B07 建立的 (user_id, create_time) 复合索引。
func topUpStatusSnapshot(userId int) ([]*model.TopUp, error) {
	cutoff := time.Now().Add(-topUpStreamWindow).Unix()
	var orders []*model.TopUp
	err := model.DB.
		Model(&model.TopUp{}).
		Where("user_id = ? AND create_time >= ?", userId, cutoff).
		Order("id desc").
		Limit(topUpStreamMaxOrders).
		Find(&orders).Error
	return orders, err
}

func marshalTopUpSnapshot(orders []*model.TopUp) string {
	payload := struct {
		Orders []*model.TopUp `json:"orders"`
	}{Orders: orders}
	if payload.Orders == nil {
		payload.Orders = []*model.TopUp{}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

// StreamTopUpStatus 是订单状态 SSE 端点。
func StreamTopUpStatus(c *gin.Context) {
	userId := c.GetInt("id")
	if userId <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "unauthorized"})
		return
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		// 不允许降级成一次性响应：前端拿到 200 却不是流会误以为连接正常。
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "streaming is not supported"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	// 关掉代理缓冲，否则 Nginx 会攒满一整个 buffer 才发出去。
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher.Flush()

	events, cancel := topupevents.Subscribe(userId)
	defer cancel()

	lastSent := ""
	push := func() bool {
		orders, err := topUpStatusSnapshot(userId)
		if err != nil {
			common.SysError(common.LogText("topup stream snapshot failed for user %d: %s", userId, err.Error()))
			return true
		}
		snapshot := marshalTopUpSnapshot(orders)
		if snapshot == "" || snapshot == lastSent {
			return true
		}
		lastSent = snapshot
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", snapshot); err != nil {
			return false
		}
		flusher.Flush()
		return c.Request.Context().Err() == nil
	}

	// 首帧立即下发当前快照：前端连上就能渲染，不必等第一次变更。
	if !push() {
		return
	}

	poll := time.NewTicker(topUpStreamPollTick)
	defer poll.Stop()
	heartbeat := time.NewTicker(topUpStreamHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-events:
			// 多次事件合并成一次快照推送：以库为准，重复读没有副作用。
			if !push() {
				return
			}
		case <-poll.C:
			if !push() {
				return
			}
		case <-heartbeat.C:
			// 注释行心跳，既保活又不触发前端 onmessage。
			if _, err := fmt.Fprint(c.Writer, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
