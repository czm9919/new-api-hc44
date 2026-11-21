# hc46：号池健康守卫 + 每日战报

hc46 补齐了上一轮建议清单里的第 2 项（号池健康探测/自动熔断告警）和第 8 项（每日战报到管理群）。
两项都是**纯后台任务**，不需要前端改动，配置项自动落库、自动回填。

## 1. 号池健康守卫（`service/pool_health.go`）

每 2 分钟扫一遍渠道表，按 `group` 汇总可用数量，只在**状态跳变**时发通知。

判定规则：

| 状态 | 条件 | 含义 |
| --- | --- | --- |
| `down` | 分组内 `启用 + 手动禁用 + 自动禁用` 中启用数为 0 | 该分组已无可用渠道，新请求必然失败 |
| `degraded` | 自动禁用渠道占比 ≥ 60% | 号池正在塌，但还没全挂 |
| `ok` | 其余情况 | 正常 |

- 一个渠道可以属于多个分组（`group` 字段逗号分隔），每个分组各算一次。
- 只在 `down → ok`、`degraded → ok` 这类恢复跳变时才发恢复通知，避免每轮重复刷。
- 分组被整体删除后，状态表里的残留会被清掉，不会误报恢复。
- 启动后先 `sleep 30s` 再开始 tick，避开数据库迁移未就绪的窗口；`IsMasterNode` 守卫保证多节点只跑一份。
- 告警自带限频：`adminAlertLimiter` 按 `pool:<state>:<group>` 做 1 小时滑动窗口，配额 2 条。
- **不受 `BillingAlertEnabled` 影响**——那个开关是给计费告警用的，号池全挂属于可用性事故。

## 2. 每日战报（`service/daily_report.go`）

每天固定时刻把**昨日**的运营数字推给 root。

配置项 `daily_report_setting`（DB key 前缀 `daily_report_setting.`）：

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `enabled` | `true` | 总开关 |
| `hour` | `9` | 发送时刻（24 小时制，服务器本地时区） |
| `minute` | `0` | 分钟 |
| `top_users` | `3` | 消耗排行取前几名，0 表示不列 |

战报内容：

- 请求数、消耗额度（$ 折算）、活跃用户数
- 单次均耗
- 新增用户、新增 QQ 绑定
- 消耗 Top N
- 渠道状态：启用 / 自动禁用 / 手动禁用
- 渠道全挂时追加一行 `⚠️ 当前没有任何可用渠道`

触发窗口：30 秒 tick，命中 `hour:minute` 且当天未发才发；错过时刻 3 小时内仍会补发，超过 3 小时直接跳过（避免服务器半夜重启后早上收到一条昨天的战报）。

统计口径复用 `model/usage_stats.go`：`type = LogTypeConsume`，时间用 `created_at >= ? AND created_at < ?` 的 Unix 秒半开区间，SQLite / MySQL / PostgreSQL 通用，不写各库的日期函数。

## 3. 通知类型拆分（hc46 真机验证发现的问题）

真机端到端验证时暴露了一个真实缺陷：**每日战报和号池熔断共用 `admin_alert` 通知类型**，而 `CheckNotificationLimit` 的限流键是 `用户 + 类型`，默认 `10 分钟 2 条`（`NOTIFY_LIMIT_COUNT` / `NOTIFICATION_LIMIT_DURATION_MINUTE`）。

复现路径：

1. 19:10 战报发出 → `admin_alert` 用掉 1 个名额
2. 19:11 号池熔断 → 用掉第 2 个
3. 19:13 号池恢复 → **超限被静默丢弃**，只在 `[SYS]` 日志留一行 `notification limit exceeded`

修复：按告警域拆成独立的限流桶（`relaykit/dto/notify.go`）。

| 常量 | 值 | 用途 |
| --- | --- | --- |
| `NotifyTypeAdminAlert` | `admin_alert` | 事故告警：内存、任务失败、计费异常 |
| `NotifyTypeDailyReport` | `daily_report` | 每日战报 |
| `NotifyTypePoolHealth` | `pool_health` | 号池熔断 / 恢复 |

站内收件箱的 `type` 字段同步变得更精确；前端对未知类型回落到默认 `outline` 徽章，无需改动。

回归测试：`service/notify_type_isolation_test.go`，断言三个类型互不相同的字符串，且战报配额用满后 `admin_alert`、`pool_health` 仍可发送。

## 4. 部署注意事项：Gotify URL 会被 SSRF 防护拦截

真机验证时踩到的坑，写在这里避免重复踩：

`sendGotifyNotify` 走 `GetSSRFProtectedHTTPClient`，会走 `fetch_setting` 的 SSRF 校验：

- `allowed_ports` 默认 `["80","443","8080","8443"]`，**非这些端口直接拒绝**（报 `port xxx is not allowed`）
- `allow_private_ip` 默认 `false`，**局域网地址也会被拒**（`private IP address not allowed`）

失败时的表现是「站内收件箱有记录、但外部推送收不到」，日志里只有一行 `[SYS] 向用户 1 发送通知 xxx 失败: request reject: ...`，很容易误判成配置没生效。

自建 Gotify 如果监听在非常规端口或内网 IP，需要二选一：

1. 把端口加进 `fetch_setting.allowed_ports`，并打开 `fetch_setting.allow_private_ip`；
2. 用反向代理把 Gotify 挂到 80/443 的公网域名下（推荐，SSRF 面最小）。

这个限制是上游原有的 SSRF 防护逻辑，hc46 未做改动，只在文档里说明。

## 5. 验证记录

- `go build ./...` 通过；`go test ./service/ ./model/ ./router/` 全绿。
- 单测：`service/daily_report_test.go`（时间窗口 7 个子例 + 均耗 + 数量格式化）、`service/pool_health_test.go`（分组汇总、down/recover、degraded 升级、纯手动禁用不误报、分组删除）、`service/notify_type_isolation_test.go`。
- 真机端到端（本地假 Gotify 收件）：

```
19:10:16  daily report sent to 1 root user(s)        → 收到「昨日战报 2026-11-20」
19:11:46  pool alert: [号池熔断] 分组 default 已无可用渠道 → 收到熔断告警
19:13:46  pool alert: [号池恢复] 分组 default 可用渠道 1 个 → 收到恢复告警
```

三条全部送达，且战报在渠道全挂时正确追加了 `⚠️ 当前没有任何可用渠道`。
