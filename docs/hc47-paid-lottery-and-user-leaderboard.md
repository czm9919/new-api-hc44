# hc47 · 付费抽奖 + 用户可见排行榜

本版只做两件面向用户的事：**让抽奖能收钱**，**让用户看见自己的名次**。顺带把上一版遗留的
前端 lint 债清干净。

站点版本：`v1.0.0-rc.51+hc47`
镜像：`ghcr.io/czm9919/new-api-hc44:latest`

---

## 一、付费抽奖

免费次数抽完后，用户可以用**余额**继续抽。运营侧三个开关全在「幸运大转盘 → 抽奖设置」里。

### 配置项

| Option Key | 含义 | 默认 | 区间 |
| --- | --- | --- | --- |
| `lottery_paid_enabled` | 付费抽奖总开关 | `false` | — |
| `lottery_paid_price` | 单次价格（额度） | `5000` | `0 ~ 1000000` |
| `lottery_paid_daily_limit` | 每日付费次数上限 | `5` | `0 ~ 200` |

默认**关闭**：不显式打开就不会向用户收一分钱。两个上限都是防手抖的护栏——价格多打几个 0
会把用户余额一次掏空，次数不设天花板则等于开了个无限抽奖机。

### 余额语义（关键）

只扣**付费余额**，不动免费额度。站内定义：

```
付费余额 = quota - free_quota
```

扣减走 `model.TryReserveUserQuotaUseFree(..., useFree=false)`，落到 SQL 是：

```sql
UPDATE users SET quota = quota - ?
WHERE id = ? AND quota - free_quota >= ?
```

带余额条件的单条 UPDATE，因此：

- **余额不足直接 `ok=false`**，不会出现「扣成负数」；
- 并发抽奖不会因为先查后改而超扣；
- 免费额度不会被抽奖消耗掉（免费额度只服务免费模型）。

### 接口

```
POST /api/user/lottery/spin
Body: {"paid": true}     # 省略 body = 免费抽奖，兼容老前端
```

新增错误码：

| HTTP | code | 含义 |
| --- | --- | --- |
| 403 | `LOTTERY_PAID_DISABLED` | 运营没开付费抽奖 |
| 429 | `LOTTERY_PAID_NO_SPINS` | 今日付费次数用尽 |
| 402 | `LOTTERY_INSUFFICIENT_QUOTA` | 付费余额不足 |

### 扣费与发奖的顺序（重要）

1. 先原子扣费（余额不足就此返回，什么都没发生）；
2. 再写 `lottery_spins` 落库；
3. **落库失败 → 退款**；
4. **落库成功但发奖失败 → 不退款**（奖已经抽出，凭据在库里，退款等于白送）。

`lottery_spins` 上的唯一索引 `idx_lottery_user_day_index (user_id, day_key, spin_index)`
是并发防线，`spin_index` 取当日**总**次数（免费 + 付费共用一个序列），所以付费抽奖
不会和免费抽奖抢到同一个序号。

### 前端

转盘页在免费次数用尽后出现「用余额再抽一次（消耗 X）」按钮，余额不足时按钮置灰并显示
差额。管理端设置卡片新增「付费抽奖」一组，含开关、单价、每日上限，带行内校验——
`paid_price` 越界直接在字段上报错，不发请求。

---

## 二、用户可见排行榜

`/rankings` 页面新增「用户消耗榜」区块。

### 与既有榜单的关系

| 页面/接口 | 受众 | 内容 |
| --- | --- | --- |
| `/api/user/consumption_rank`（hc34） | 管理员 | 余额、累计消耗、已删除用户，用于运营决策 |
| `/api/rankings`（hc34） | 所有人 | 模型榜、厂商榜、涨跌榜（价格/用量） |
| `/api/rankings/users`（**hc47**） | 所有人 | 用户消耗榜 + 「我的排名」 |

### 接口

```
GET /api/rankings/users?period=today|week|month|year&limit=20
```

- 匿名可看榜：用户名一律走 `MaskUsername` 打码（1 字原样、2 字首 + `*`、
  3~4 字留首尾、>4 字留前 2 后 4）；
- 登录后额外下发 `self`：名次、我的消耗、**距上一名还差多少**、超过了百分之多少的人；
- 未登录时 `self` 字段整体不下发，前端显示「登录后查看我的排名」。

响应结构：

```json
{
  "period": "week",
  "leaderboard": [
    {"rank":1,"user_id":10,"username":"al***ce","used_quota":123456,
     "request_count":88,"quota_share":0.42,"is_self":false}
  ],
  "totals": {"used_quota":294000,"request_count":210,"user_count":37},
  "self": {"ranked":true,"rank":2,"used_quota":88000,"request_count":31,
           "quota_share":0.299,"gap_to_prev":35456,"ahead_of_percent":0.94}
}
```

### 名次口径

沿用 hc34 `aff_leaderboard` 的范式：**名次 = 严格大于我的人数 + 1**，并列同名次。
这样「我第 2 名」不会因为别人并列而跳号，也不会出现「第 1 名有两个人但只显示一个人」。

### 缓存

- 榜单聚合缓存 **60 秒**（进程内）：`logs` 的 `SUM(quota) GROUP BY user_id` 是重查询，
  而今日榜又要求足够新，一分钟是两者的折中；
- **「我的排名」不进缓存**，永远实时——用户点开就想看到当前名次，等一分钟后才更新
  会直接毁掉「好胜心」这个设计目的。

### 数据面

新增 `model.GetUserConsumptionRank` / `GetUserConsumptionTotals` /
`GetUserConsumptionProfiles`（`model/consumption_rank.go`）。

`LOG_DB` 可指向 ClickHouse，日志表与用户表**不一定同库**，所以聚合一律用 GORM 派生表
（`LOG_DB.Table("(?) as ranked", agg)`，先例见 `model/channel.go`），不做跨库 JOIN。

### 导航开关

与 `/api/rankings` 共用同一个 `rankings` 导航模块开关（`HeaderNavModuleAuth("rankings")`）：
关掉这个模块时榜单接口返回 403，前端也不渲染。鉴权用 `TryUserAuth()`——匿名放行，
登录则拿到 `id`，不强制登录。

---

## 三、顺带修的

- `rankings/lib/format.ts` 4 处 `curly`；
- `rankings/index.tsx` 的嵌套三元（拆出 `SnapshotSections` 组件）与 `prefer-set-has`
  （`VALID_PERIODS` 改成 `Set`）；
- `lottery.test.tsx` 的冗余 spread 兜底、`admin-settings.test.tsx` 的 import 顺序。

`oxlint` 对 `src/features/rankings` + `src/features/lottery`：**0 warnings / 0 errors**。

---

## 四、验证

| 项 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test ./service/... ./model/... ./controller/...` | 全部通过 |
| `tsgo -b` | 通过 |
| `vitest run`（全量） | 251 文件 / 2978 用例全绿 |
| `oxlint`（改动目录） | 0 warnings / 0 errors |
| `rsbuild build` | 通过 |

新增后端用例：付费抽奖（余额不足不扣、成功扣费、退款边界、次数上限、开关关闭）、
用户排行榜（名次并列、窗口换算、打码、匿名不带 self）。
新增前端用例：`user-leaderboard-section.test.tsx` 5 项（登录上榜 / 登录无消耗 / 匿名 /
空榜 / 接口失败）。

---

## 五、配套插件

QQ 插件同步升到 **v1.0.5**（独立仓库），主题是「连不上站点时给出可执行的答案」：
容器回环地址识别、默认绕代理、只对连接未建立重试、六类网络失败人话文案、启动自检。
与本版后端无接口变更，可独立升级。
