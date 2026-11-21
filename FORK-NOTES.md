# 这个仓库是什么

`czm9919/new-api-hc44` 是 [QuantumNous/new-api](https://github.com/QuantumNous/new-api)
的**定制分支**，基线 `v1.0.0-rc.51`。上游的 API 网关、渠道路由、计费内核全部保留，
在它之上加了三块与「QQ 社群运营」相关的能力，当前版本号 `v1.0.0-rc.51+hc44`。

如果你只是要部署一个通用的 New API，用上游仓库即可。这里的改动是为了一套具体场景：
用 QQ 群发放免费额度，并且要能追到「谁领的」。

## 定制点

### 1. QQ 号绑定面板账号

- 站点新增 `/api/qq/agent/ping`、`/api/qq/agent/verify` 两个机器人接口，
  用 `X-QQ-Agent-Token` 鉴权（对应 option `qq_agent_token`）。
- 用户在面板生成 8 位一次性绑定码（10 分钟有效、用过即废、重新生成作废旧码），
  私聊机器人发 `/绑定 <码>` 完成绑定。
- 一个 QQ 只绑一个账号，一个账号只绑一个 QQ。
- 前端入口：幸运大转盘 → QQ 绑定。
- 配套的 AstrBot 插件在 [czm9919/astrbot_plugin_newapi_qq](https://github.com/czm9919/astrbot_plugin_newapi_qq)。

### 2. 幸运大转盘 / 免费额度门禁

- 抽奖可以要求先绑定 QQ（option `lottery_require_qq`）。
- 免费模型可以要求先绑定 QQ（option `qq_require_for_free`）。
- 签到只增加免费额度，不会把含免费的总额当成付费余额。

### 3. 官方价三源同步

`官方价` 来自三个公开价格表，合并优先级 **basellm > models.dev > LiteLLM**：

| 源 | 条目数 | 说明 |
| --- | --- | --- |
| basellm `ratio_config-v1-base.json` | 403 | 官方 `billing_expr`，用计费引擎求值 |
| models.dev `api.json` | 3399 | 各 provider 的 `$/1M` |
| LiteLLM `model_prices_and_context_window.json` | 4224 | 厂商价表，覆盖最广 |

- 名称匹配三级降级：精确 → 去厂商前缀 → 去版本/量化/上下文尾巴。
- 后台每 6 小时自动刷新快照（`options.OfficialPricingSnapshot`），也可在
  系统设置 → 模型 →「一键同步官方定价」手动触发。
- 换算：`ModelRatio = 官方 $/1M ÷ 2`，`CompletionRatio = 输出价 ÷ 输入价`。
- 价格表里没有按 token 报价的模型**不会被编价**，保持平台默认倍率。

### 4. 前端：扁平化 / 极简风

`--radius` 收紧、`--shadow-*` 全族置透明、默认配色改为石墨灰阶 ink；
深度关系由 1px 描边承担。改动集中在 `web/src/styles/theme.css` 与几个页面组件，
语义色与图表色保留。

## 从源码构建

```bash
# 前端（需要 bun）
cd web && bun install && bun run build

# 后端（前端产物会被 embed 进二进制）
go build -trimpath -ldflags "-s -w" -o new-api.exe .
```

`web/dist` 必须在 `go build` 之前存在，否则二进制里没有界面。

## 运行

```bash
PORT=3000 \
SQLITE_PATH="./data/new-api.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate" \
TZ=Asia/Shanghai \
./new-api.exe --port 3000 --log-dir ./data/logs
```

关键 option（管理端 → 系统设置）：

| option | 作用 |
| --- | --- |
| `qq_agent_token` | 机器人服务令牌，插件侧填同一个值；为空时机器人接口一律 503 |
| `lottery_require_qq` | 抽奖是否要求已绑定 QQ |
| `qq_require_for_free` | 免费模型是否要求已绑定 QQ |
| `OfficialPricingSnapshot` | 三源官方价快照（JSON，定时任务维护，不用手改） |

## 验证记录（hc44 发布时）

- `verify-hc44.py` 34/34：定价链路回归、价格榜与落库倍率一致、前端扁平化断言。
- `verify` 插件侧 21/21：管理端接口 + 绑定码全流程。
- 官方价同步探针 15/15：三源快照 8026 条 → 合并预设 → 落库 → 榜价差异 0。
- 85 个模型里 37 个命中真实官方价；其余 48 个四个公开源都没有按 token 报价
  （NVIDIA NIM 按 GPU 时/免费额度发放，或厂商已下架），保持默认倍率。

## 与上游的关系

- 本仓库是快照式导出：两个 commit，`base`（上游 rc.51 融合点）与 `hc44`（定制后的树）。
- 上游更新请直接 `git pull upstream` 或基于上游重建；这里不追上游历史。
- License 跟随上游（见 `LICENSE`）。
