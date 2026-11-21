/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// v1.1.0 M1 · B26 对账月结单：按月汇总充值/消耗/退款/挂单，供运营对账与导出。

// StatementDay 是单日维度的小计，日期格式 YYYY-MM-DD（本地时区）。
type StatementDay struct {
	Date          string `json:"date"`
	RechargeQuota int64  `json:"recharge_quota"`
	ConsumeQuota  int64  `json:"consume_quota"`
	RefundQuota   int64  `json:"refund_quota"`
}

type MonthlyStatement struct {
	Month string `json:"month"`

	// 当月日志侧汇总（quota 口径）
	RechargeQuota int64 `json:"recharge_quota"`
	ConsumeQuota  int64 `json:"consume_quota"`
	RefundQuota   int64 `json:"refund_quota"`

	// 当月成功充值订单（金额口径）
	RechargeOrders int64   `json:"recharge_orders"`
	RechargeMoney  float64 `json:"recharge_money"`
	RechargeAmount int64   `json:"recharge_amount"`

	// 当月仍未完结的挂单（pending 状态订单）
	PendingOrders        int64   `json:"pending_orders"`
	PendingMoney         float64 `json:"pending_money"`
	PendingRechargeQuota int64   `json:"pending_recharge_quota"`

	// 按天分解（升序）
	Days []StatementDay `json:"days"`
}

// parseStatementMonth 解析 "YYYY-MM"，返回该月的 [start, end) 时间戳。
func parseStatementMonth(month string) (int64, int64, error) {
	if len(month) != 7 || month[4] != '-' {
		return 0, 0, fmt.Errorf("invalid month, expected YYYY-MM")
	}
	year, err := strconv.Atoi(month[:4])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid year in month %q", month)
	}
	mon, err := strconv.Atoi(month[5:])
	if err != nil || mon < 1 || mon > 12 {
		return 0, 0, fmt.Errorf("invalid month in month %q", month)
	}
	start := time.Date(year, time.Month(mon), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)
	return start.Unix(), end.Unix(), nil
}

// statementDayKey 把 created_at 时间戳映射到 YYYY-MM-DD。
func statementDayKey(ts int64, loc *time.Location) string {
	return time.Unix(ts, 0).In(loc).Format("2006-01-02")
}

func GetMonthlyStatement(c *gin.Context) {
	month := strings.TrimSpace(c.Query("month"))
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	start, end, err := parseStatementMonth(month)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	rows, err := model.GetStatementLogRows(start, end)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	loc := time.Local
	dayMap := make(map[string]*StatementDay)
	stmt := MonthlyStatement{Month: month}
	startTime, endTime := time.Unix(start, 0).In(loc), time.Unix(end, 0).In(loc)
	for d := startTime; d.Before(endTime); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		dayMap[key] = &StatementDay{Date: key}
	}

	for _, row := range rows {
		key := statementDayKey(row.CreatedAt, loc)
		day, ok := dayMap[key]
		if !ok {
			day = &StatementDay{Date: key}
			dayMap[key] = day
		}
		switch row.Type {
		case model.LogTypeTopup:
			day.RechargeQuota += row.Quota
			stmt.RechargeQuota += row.Quota
		case model.LogTypeConsume:
			day.ConsumeQuota += row.Quota
			stmt.ConsumeQuota += row.Quota
		case model.LogTypeRefund:
			day.RefundQuota += row.Quota
			stmt.RefundQuota += row.Quota
		}
	}

	successAgg, err := model.GetTopUpAggregateByStatus(start, end, common.TopUpStatusSuccess)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	stmt.RechargeOrders = successAgg.OrderCount
	stmt.RechargeMoney = successAgg.MoneySum
	stmt.RechargeAmount = successAgg.AmountSum

	pendingAgg, err := model.GetTopUpAggregateByStatus(start, end, common.TopUpStatusPending)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	stmt.PendingOrders = pendingAgg.OrderCount
	stmt.PendingMoney = pendingAgg.MoneySum
	stmt.PendingRechargeQuota = int64(float64(pendingAgg.AmountSum) * common.QuotaPerUnit)

	stmt.Days = make([]StatementDay, 0, len(dayMap))
	for _, day := range dayMap {
		stmt.Days = append(stmt.Days, *day)
	}
	// 升序排列，导出 CSV 与页面展示都直接可用。
	for i := 1; i < len(stmt.Days); i++ {
		for j := i; j > 0 && stmt.Days[j].Date < stmt.Days[j-1].Date; j-- {
			stmt.Days[j], stmt.Days[j-1] = stmt.Days[j-1], stmt.Days[j]
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    stmt,
	})
}
