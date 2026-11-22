# hc49：付费功能全面美元化 + 加速专线完善

## 为什么改

hc47 的付费抽奖和 hc48 的加速套餐，报价直接用了内部单位 `quota`：

- 管理端填「5000 额度」，用户看到 `$0.01`，两套口径要心算；
- 站点一旦调整 `QuotaPerUnit`，所有历史报价的含义全变；
- 前端各处自己乘除，容易出现分位差异。

hc49 把**对外报价统一成美元**，额度退回成纯内部计费单位。

## 换算规则

```
1 USD = common.QuotaPerUnit 额度（默认 500000）
quota = round(usd * QuotaPerUnit)
```

实现在 `common/currency.go`：

| 函数 | 作用 |
|---|---|
| `QuotaToUSD(quota int64) float64` | 额度 → 美元 |
| `QuotaToUSDValue(quota float64) float64` | 聚合结果（浮点）→ 美元 |
| `USDToQuota(usd float64) int64` | 美元 → 额度，四舍五入到整数 |
| `RoundUSD(usd float64) float64` | 收敛到 4 位小数，避免 JSON 里出现 3.3333333333333335 |

`QuotaPerUnit` 是变量而不是常量，所以换算只能走函数，不能写成常量乘除。

## 接口变化

### 加速套餐

| 方向 | 字段 | 说明 |
|---|---|---|
| 入参 | `price_usd` | **新增且优先**。传了它就用它换算，忽略 `price_per_month` |
| 入参 | `price_per_month` | 保留兼容，旧脚本仍可直接传额度 |
| 出参 | `price_usd` | 月费美元报价（`AccelPlan` / `UserAccelSub` 上都是 `gorm:"-"` 计算字段） |
| 出参 | `total_charged_usd` | 订阅累计扣费美元 |

订阅视图另外补了 `paid_balance_usd`、`usage`、`today_usage`、`limits`：

```json
{
  "paid_balance_usd": 0.12,
  "usage":        { "requests": 12, "quota_usd": 0.0036, "last_used_at": 1795311857 },
  "today_usage":  { "requests": 3,  "quota_usd": 0.0009, "last_used_at": 1795311857 },
  "limits": { "max_price_usd": 2, "price_step_usd": 0.01, "max_concurrency": 1000, "max_extend_days": 3650 }
}
```

### 抽奖

| 方向 | 字段 | 说明 |
|---|---|---|
| 入参 | `paid_price_usd` | 新增且优先 |
| 出参 | `paid_price_usd` | 单次价格美元报价 |
| 出参 | `paid_quota_usd` | 付费余额美元（`/api/user/lottery`） |
| 出参/入参 | `prizes[].quota_usd` | 奖品金额美元；**不落库**，库里仍只存 `quota` |

奖品美元字段在写库前会被 `CanonicalizeLotteryPrizes` 换算回额度并清零，避免
`options` 里存两份互相矛盾的金额。

## 加速专线完善

### 用量统计

`model/accel_usage.go` 的 `GetAccelUsage(userId, group, since)` 直接从 `logs`
按 `(user_id, group, created_at)` 聚合，口径与扣费一致（`type = LogTypeConsume`）：

- 本期用量：`since = subscription.start_at`
- 今日用量：`since = 本地时区 0 点`

不另建用量表的原因：日志已经有这些维度的索引，单用户单分组的数据量很小；
另建表要处理补扣、退款、日志清理后的对账，收益不抵成本。

### 换套餐

`SubscribeAccelPlan(userId, planId, allowSwitch)`：

- `allowSwitch=false` 且订阅生效中 → `409 ACCEL_ALREADY_ACTIVE`（防连点重复扣费）
- `allowSwitch=true` 且套餐不同 → 按新价扣一个月，**周期从当下重算，剩余时长作废**

这是不可逆的资损动作，所以接口要求显式 `switch: true`，前端必须二次确认
（`AlertDialog` 里写明「剩余天数作废」）。

### 管理端订阅管理

| 接口 | 作用 |
|---|---|
| `GET /api/user/accel/subscriptions?p=&page_size=` | 订阅列表（含用户名、套餐名、美元金额） |
| `POST /api/user/accel/subscriptions/extend` `{user_id, days}` | 补时长，不扣费；已过期从当下起算 |
| `POST /api/user/accel/subscriptions/revoke` `{user_id, reason}` | 强制停用，立即断线；**不自动退款** |

停用不退款是刻意设计：资金动作必须人工核对，代码里自动退款最容易出现
「停用 + 退款」双写不一致。

## 前端

- 加速页：套餐卡与月费全部按美元展示与输入；新增本期/今日用量四宫格、
  剩余天数进度条、到期 3 天内的高亮提醒。
- 加速页管理端：套餐表单「月费（USD）」；新增订阅管理列表（补 30 天 / 停用）。
- 抽奖页：付费单价、奖品金额改为美元输入；余额、花费、奖励、奖池、记录
  全部走 `formatQuota` / `formatCurrencyUSD`，不再出现裸额度数字。

## 兼容性

- 老客户端只传 `price_per_month` / `paid_price` / `prizes[].quota`：行为不变。
- 新客户端传 `*_usd`：服务端换算，返回体同时带两种口径，前端只读美元字段。
- 库结构没有新增列，只有两张表新增了 `gorm:"-"` 计算字段（不参与迁移）。

## 验证

- `go build ./...`、`go vet`、`go test ./common ./service ./model`
- 前端 `tsgo -b`、`oxlint`（改动文件 0/0）、`vitest run src/features/lottery`
- 真机 `hc49-probe.py`：套餐美元往返、余额扣减、用量聚合、换套餐、补时长、
  停用、抽奖美元报价，全部通过后清理探针数据并还原 `GroupRatio` 与抽奖配置。
