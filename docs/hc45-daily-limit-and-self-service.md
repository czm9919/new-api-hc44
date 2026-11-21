# hc45：每日额度护栏 + QQ 群内自助

两个改动，一个保命，一个提转化。

## 一、每日额度护栏

### 为什么做

原来的限制只有「用户余额够不够」，余额是自己的钱，花光是他的事。但号池是共享的：
单个账号一天内无限刷，会把上游免费额度吃干净，连带影响所有人，还容易触发
上游风控把整批 key 做掉。

### 做了什么

| 项 | 位置 |
| --- | --- |
| 配置项 `daily_limit_setting` | `setting/operation_setting/daily_limit_setting.go` |
| 计数与缓存 | `service/daily_limit.go` |
| 用量聚合查询 | `model/usage_stats.go` |
| 拦截点 | `relay/request_billing.go`（`RealNameGate` 之后、预扣费之前） |

拦截发生在预扣费之前，所以被拒的请求**不占渠道、不扣额度、不出站**。
返回 429 + `daily quota limit exceeded: used X of Y today`。

### 配置

在 `options` 表里（或通过后台配置接口）：

```
daily_limit_setting.enabled      = true
daily_limit_setting.user_quota   = 50000     # 每用户每日额度上限，0 = 不限
daily_limit_setting.group_quota  = {}        # 可选，按分组覆盖
```

单位是 quota：`QuotaPerUnit = 500000`，即 500000 quota = $1。改完需要重启进程。

### 设计取舍

- **只判「已用 >= 上限」，不预估本次消耗。** 流式请求的完成 token 数未知，
  预估会大量误伤；粗判最多多放一个请求的量。
- **不做内存累加，每次从 `logs` 聚合，带 60 秒缓存。** 零状态，进程重启不丢。
  代价是每用户最多每分钟一次聚合查询，走 `idx_user_id_id`。
- **查询失败一律放行。** 护栏的作用是限流，不该在数据库抖动时把整站打挂。
- **依赖消费日志。** `LogConsumeEnabled` 关闭时统计恒为 0，启动日志会打一行警告。

### 验证

`service/daily_limit_test.go` 覆盖：分组回退、正好达上限、非消费日志不计入、
只统计当天、缓存命中与失效、关闭时不限制、当天零点边界。

线上实测（本机 3000 端口）：

```
used 60000 / limit 50000 -> HTTP 429 daily quota limit exceeded
limit 放宽到 100000 后    -> 请求正常到达上游（410，该模型已下线），不再被拦
```

## 二、QQ 群内自助

### 为什么做

用户想知道余额、用量、签到状态，原来只能打开面板。群里问一句「我还有多少」
是最高频的动作，把这三件事搬到 QQ，用户才会持续用。

### 站点侧新增接口

都挂在 `/api/qq/agent` 下，沿用机器人服务令牌（`X-QQ-Agent-Token`），
不参与面板会话鉴权，路由层已有 `QQAgentRateLimit` 兜底。

| 接口 | 作用 |
| --- | --- |
| `GET /api/qq/agent/me?qq=` | 余额、今日用量、当日上限、签到状态；未绑定返回 `bound=false` |
| `POST /api/qq/agent/checkin` | 代签；重复签到返回 `already=true` 而不是报错 |
| `GET /api/qq/agent/rank?limit=` | 今日消耗榜，用户名打码，limit 上限 30 |

实现：`controller/qq_agent_self.go`；QQ 反查账号：`model.GetUserByQQ`。

### 机器人侧命令

| 命令 | 别名 |
| --- | --- |
| `余额` | `查询` / `me` |
| `签到` | `checkin` |
| `排行` | `榜单` / `rank` |

识别规则和绑定命令一致：私聊可直接发裸命令，群聊必须带 `/` 或 `@机器人`，
避免「我余额不够了」这类日常聊天被当成指令。自助命令有 3 秒本地冷却防刷屏
（`self_cooldown_seconds` 可配）。

### 验证

- Go：`go test ./service -run 'Daily|StartOfToday'` 全绿
- 站点端到端 `probe-hc45-self.py run`：**10/10**
  （鉴权 401、未绑定、已绑定字段、非法 QQ 400、首次/重复签到、签到后状态、排行打码、limit 收敛）
- 插件离线：`python -m unittest discover -s tests` **28/28**
- 插件直连真实站点：余额输出 `站点账号 / 余额 $0.0080 / 今日用量 / 签到状态`；
  签到成功返回额度；群聊裸「余额」无响应，`/余额` 正常响应

## 三、顺带修掉的问题

`POST /api/ratio_sync/official/refresh`（hc44 新增）一直没有在
`middleware/access_token_routes.go` 里登记访问令牌作用域，
`TestAccessTokenRouteRulesCoverEveryDashboardRoute` 因此失败。已补上
`option:write`。

## 四、还没做的

- **渠道异常推 QQ**（原计划编号 2）：渠道自动禁用/定时测试已有，缺的是
  「被禁用时主动推一条消息到管理群」，需要先定推送通道（OneBot HTTP / AstrBot webhook）。
- **每日战报**（编号 8）：同上，依赖推送通道。
