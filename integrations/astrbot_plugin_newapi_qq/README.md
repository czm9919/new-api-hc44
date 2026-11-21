# astrbot_plugin_newapi_qq

## ⚠️ 装之前必读

**本插件只是「调用端」。** 站点侧不装配套接口，所有命令都会回 503。

两个前提缺一不可：

1. **站点必须是配套的 New API 定制分支**（`v1.0.0-rc.51+hc37` 起），它才有 `/api/qq/agent/ping` 与 `/api/qq/agent/verify`。上游 `QuantumNous/new-api` 没有这两个接口。
2. **必须在面板里创建机器人服务令牌**：「幸运大转盘」→「抽奖设置 → 机器人」→「生成随机令牌」，把明文复制进插件的 `agent_token`。明文**只在生成/轮换时显示一次**，之后面板只显示尾号。

这两步没做，`/绑定 测试` 只会回「站点尚未开启机器人接入」。先按「站点侧准备」做完，再装插件。

---

把 QQ 号绑定到 New API 面板账号的 AstrBot 插件。

用户在面板生成一枚 8 位一次性绑定码，私聊机器人发 `/绑定 <码>`，机器人带着服务令牌调站点接口完成绑定。绑定之后，站点侧的「幸运大转盘抽奖」与「免费模型」两道门禁才放行 —— 这样每个免费额度的领取人都能追到具体 QQ。

- 站点不接触 QQ 协议，只信任机器人给的「谁在发消息」
- 绑定码一次性、10 分钟过期、重新生成作废旧码
- 一个 QQ 只绑一个账号，一个账号只绑一个 QQ
- 插件除 AstrBot 框架外只用标准库（`urllib` + `asyncio`），无第三方依赖

> 依赖站点侧接口 `/api/qq/agent/ping`、`/api/qq/agent/verify`。这两个接口属于配套的 New API 定制分支（`v1.0.0-rc.51+hc37` 起），上游 `QuantumNous/new-api` 没有它们。

## 安装

1. 下载本仓库，把整个目录放进 AstrBot 的 `data/plugins/`：

   ```
   data/plugins/astrbot_plugin_newapi_qq/
   ```

2. 重启 AstrBot（或在 WebUI 插件页点重载）。
3. 在 AstrBot WebUI → 插件 → 本插件 → 配置：

   | 配置项 | 默认 | 说明 |
   | --- | --- | --- |
   | `api_base` | `http://127.0.0.1:3000` | 站点地址，**不要**带 `/api` |
   | `agent_token` | 空 | 与服务令牌一致，见下方「站点侧准备」 |
   | `bind_command` | `绑定` | 绑定命令名；改了就按 `/你改的名字 <码>` 使用 |
   | `timeout_seconds` | `10` | 请求站点超时秒数 |
   | `cooldown_seconds` | `30` | 同一 QQ **绑定成功后**的冷却 |
   | `retry_cooldown_seconds` | `3` | 同一 QQ **绑定失败后**的冷却 |

也可以跳过 WebUI 表单，直接写 `data/config/astrbot_plugin_newapi_qq_config.json`：

```json
{
  "api_base": "https://api.example.com",
  "agent_token": "<与服务令牌一致>",
  "bind_command": "绑定",
  "timeout_seconds": 10,
  "cooldown_seconds": 30,
  "retry_cooldown_seconds": 3
}
```

## 站点侧准备

1. 管理员登录面板 → 「幸运大转盘」→「抽奖设置 → 机器人」。
2. 点「生成随机令牌」（服务端 32 随机字节 → 64 位十六进制），**立刻复制明文**；
   也可以自己填 16–128 位、无空格的可见 ASCII 串。
3. 顺手设两个开关：`Require QQ binding`（抽奖要求绑定）、`require_for_free`（免费模型要求绑定）。
4. 把令牌填进上面插件的 `agent_token`。令牌只显示尾号，站点不回显明文；丢了就轮换。
5. 令牌卡片上还有「轮换」（旧的立刻失效）和「撤销」（显式关掉机器人接入）——轮换后记得同步改插件配置。

## 命令

| 命令 | 作用 |
| --- | --- |
| `/绑定 <码>` | 用面板生成的 8 位码完成绑定 |
| `/绑定 测试` | 连通性自检，返回站点是否要求免费模型绑定 QQ |
| `/bind <码>` | 英文别名 |
| `/绑定` | 不带码时回用法提示 |

绑定成功示例：

```
绑定成功 ✅
站点账号：alice
QQ：123****89
现在可以回面板抽奖、使用免费模型了。
```

## 用户流程

1. 面板 →「幸运大转盘」→「生成绑定码」→ 得到 8 位码（10 分钟内有效，一次性）
2. 私聊机器人发 `/绑定 ABCD2345`
3. 机器人回复绑定成功 + 面板用户名
4. 回面板刷新，「QQ 绑定」显示已绑定，可以抽奖 / 用免费模型

## 接口约定

自己对接别的机器人框架也看这里，全部是普通 HTTP：

```
GET  {base}/api/qq/agent/ping
      Header: X-QQ-Agent-Token: <token>
      → 200 {"success":true,"data":{"ok":true,"require_qq_free":true}}
      → 401 {"code":"QQ_AGENT_UNAUTHORIZED"}
      → 503 {"code":"QQ_AGENT_UNAVAILABLE"}   站点未配置令牌

POST {base}/api/qq/agent/verify
      Header: X-QQ-Agent-Token: <token>
      Body:   {"code":"ABCD2345","qq":"123456789"}
      → 200 {"success":true,"data":{"user_id":10,"username":"alice","qq":"123****89"}}
      → 400 {"code":"QQ_BIND_CODE_INVALID"}      码不存在/过期/已用
      → 400 {"code":"QQ_BIND_FAILED"}            QQ 号格式不对
      → 409 {"code":"QQ_ALREADY_BOUND"}          该 QQ 已绑别的账号
      → 409 {"code":"QQ_ACCOUNT_ALREADY_BOUND"}  该账号已绑别的 QQ
      → 429  无 body，带 Retry-After 头           同一 IP 请求过于频繁
```

GET 形式同样支持（方便某些框架）：

```
GET {base}/api/qq/agent/verify?code=ABCD2345&qq=123456789&token=<token>
```

### 本地冷却（v1.0.1 起）

插件对同一个 QQ 做两段式本地冷却，请求根本不会打到站点：

| 时机 | 默认 | 为什么 |
| --- | --- | --- |
| 绑定**成功**后 | 30 秒 | 绑定不可重入，连点只是白打请求 |
| 绑定**失败**后 | 3 秒 | 打错一位能立刻重试，但连点会被挡下 |

命中时回复「操作太快了，请 N 秒后再试」，不占站点配额。配合站点按 IP 的
频率限制（60 次/分钟），客户端与服务端两个方向都拦得住。

状态纯内存、进程重启即清零；插件重载时 `terminate()` 会主动清空。

## 错误码 → 用户提示

插件内置映射，用户只会看到中文人话：

| 站点错误码 | 机器人回复 |
| --- | --- |
| `QQ_BIND_CODE_INVALID` | 绑定码无效或已过期，请回面板重新生成（10 分钟内有效）。 |
| `QQ_ALREADY_BOUND` | 这个 QQ 已经绑定到别的账号了，请先在原账号解绑。 |
| `QQ_ACCOUNT_ALREADY_BOUND` | 你的账号已经绑定了别的 QQ，请先在面板解绑。 |
| `QQ_AGENT_UNAUTHORIZED` | 机器人令牌无效，请联系管理员检查配置。 |
| `QQ_AGENT_UNAVAILABLE` | 站点尚未开启机器人接入（未配置服务令牌），请联系管理员。 |

## 安全说明

- 令牌只出现在机器人进程与站点之间，**不要发到群里**；泄露立刻在面板轮换
- 绑定码一次性、10 分钟过期，重新生成会作废旧码
- 建议把绑定命令限制为**私聊**处理，避免用户把码贴进群
- 插件只调用两个公开接口，不读写面板其他数据

## 不装 AstrBot 也能自检

插件本体只用标准库发 HTTP，可以用三个桩模块（`astrbot.api`、`astrbot.api.event`、`astrbot.api.star`）把它跑起来，直接对着站点验证命令解析、令牌校验、错误码映射：

```python
import sys, types, logging
api = types.ModuleType("astrbot.api"); api.logger = logging.getLogger("x")
ev = types.ModuleType("astrbot.api.event")
class _F:
    class EventMessageType: ALL = "all"
    def command(self, _n): return lambda fn: fn
    def event_message_type(self, _t): return lambda fn: fn
ev.filter = _F(); ev.AstrMessageEvent = object
star = types.ModuleType("astrbot.api.star")
class Star:
    def __init__(self, context=None): pass
class Context: pass
star.Star, star.Context = Star, Context
star.register = lambda *a, **k: (lambda cls: cls)
sys.modules.update({"astrbot": types.ModuleType("astrbot"), "astrbot.api": api,
                    "astrbot.api.event": ev, "astrbot.api.star": star})
# 之后按普通模块加载 main.py，构造 FakeEvent（提供 get_sender_id/plain_result），
# 调插件实例的 _handle_bind(event, code) 即可
```

`verify-plugin-v101.py` 就是照这个思路写的完整自检脚本（不装 AstrBot、不连站点，
纯离线跑），覆盖两段冷却、配置覆盖与脏值回落、`terminate()` 清场、429 映射、
异常不外泄、状态字典自动清理，**14/14 通过**。直接跑：

```bash
python verify-plugin-v101.py main.py
```

## 故障排查

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| 回复「站点连接失败」 | `api_base` 不可达 | 检查地址、防火墙、HTTPS 证书 |
| 回复「还没配置服务令牌」 | 插件侧令牌为空 | 在 WebUI 填 `agent_token` |
| 回复「令牌无效」 | 两边不一致 / 站点轮换过 | 重新复制，注意首尾空格 |
| 回复「绑定码无效」 | 超过 10 分钟 / 已用过 / 重新生成过 | 回面板重新生成 |
| 回复「操作太快了」 | 命中本地冷却，或站点回了 429 | 等提示的秒数；本地冷却可在配置里调小 |
| 回复「处理绑定时出错了」 | 插件内部异常（已拦下，未扩散） | 看 AstrBot 日志里 `bind handler crashed` 的堆栈 |
| 站点侧生成码报 503 | 站点未配置服务令牌 | 管理员在面板填令牌并保存 |

## 开发

```bash
python -m py_compile main.py   # 语法检查
```

`main.py` 是单文件、无第三方依赖；加命令时沿用 `@filter.command` + `event.plain_result` 即可。

## License

MIT，见 [LICENSE](LICENSE)。
