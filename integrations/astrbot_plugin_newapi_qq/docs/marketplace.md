# 在 AstrBot 插件市场里找到本插件

## 为什么市场里搜不到

AstrBot 的插件市场**不是爬 GitHub 的**。它读的是一份官方索引，索引里的条目由
`cloud.astrbot.app` 的发布流水线生成（会重新打包 zip 放到 AstrBot 的 S3，并附上
`download_url`、`commit_sha`、`stars`、`download_count`）。

也就是说：仓库推到 GitHub ≠ 出现在市场。必须提交一次。

## 客户端到底请求了什么

读 AstrBot 源码（`astrbot/dashboard/services/plugin_service.py`）可以确认默认源的取数链路：

```python
# astrbot/dashboard/services/plugin_service.py:1086-1090
md5_url = "https://api.soulter.top/astrbot/plugins-md5"
urls = [
    "https://api.soulter.top/astrbot/plugins",
    "https://github.com/AstrBotDevs/AstrBot_Plugins_Collection/raw/refs/heads/main/plugin_cache_original.json",
]
```

- 先拉 `plugins` 索引，再拉 `plugins-md5` 存进本地缓存（`data/plugins.json`），
  下次先用 MD5 比对决定要不要重新拉。
- 装包时按条目的 `download_url` 下载；没有 `download_url` 才退回 `repo` 的 GitHub 归档。
- 条目 ID 是 `author/name`（`get_market_plugin_id()`，:338-363），不是仓库名。

所以「市场里没有」只有三种可能：

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| 市场能打开，搜不到本插件 | 索引里没有（从未提交） | 走下面方案 A 或 B |
| 市场空白 / 加载失败 | 机器到 `api.soulter.top` 不通或超时 | 见文末「排障」 |
| 市场有旧信息 | 本地缓存 MD5 未变 | 市场页点刷新，或删 `data/plugins.json` |

自检命令（在跑 AstrBot 的机器上）：

```bash
curl -m 10 -s https://api.soulter.top/astrbot/plugins-md5      # 应回 {"md5": "..."}
curl -m 10 -s https://api.soulter.top/astrbot/plugins | head -c 200
```

## 方案 A：自定义源（现在就能用，不需要注册任何账号）

AstrBot 支持在 WebUI 里添加**自定义插件源**，源就是一个 JSON 索引，格式与官方一致。
本仓库已经带了这份索引：

```
https://raw.githubusercontent.com/czm9919/astrbot_plugin_newapi_qq/main/market/plugins.json
```

步骤：

1. AstrBot WebUI → **插件** → 切到 **插件市场** 标签；
2. 右上角 **源管理**（`market.sourceManagement`）→ **添加源**：
   - 名称：`New API 官方插件`
   - URL：上面那行 `.../market/plugins.json`
3. 保存 → 回到市场 → **刷新**；
4. 搜 `newapi` 或 `New API`，出现 **New API QQ 绑定** → 安装。

索引旁边还有一个 `plugins-md5.json`（`{"md5": "..."}`）。这不是装饰：客户端会把
自定义源的 URL 从 `.json` 换成 `-md5.json` 去取它，取不到就**永远用旧缓存**。
本仓库两个文件都提供，所以索引一改就会自动失效重拉。

## 方案 B：发布到官方市场

官方入口：<https://cloud.astrbot.app/publish>（需要注册 AstrBot Cloud 账号，网页表单，
无法用脚本代提交）。提交前请确认以下都满足：

| 检查项 | 要求 | 本仓库现状 |
| --- | --- | --- |
| 仓库公开 | 必须 | ✅ public |
| `metadata.yaml` 五件套 | `name` / `desc` / `version` / `author` / `repo` | ✅ 齐全 |
| 版本号 | 语义化，且**每次发布必须比线上大** | ✅ `v1.0.2` |
| zip 体积 | ≤ 16MB（CI 会直接拒） | ✅ 安装包 7KB |
| 仓库卫生 | 不含 `.git`、`__pycache__`、`node_modules`、真实配置 | ✅ `.gitignore` 已排除，真实配置不入库 |
| 依赖 | 首次运行会自动装 `requirements.txt` | ✅ 只有标准库 |
| 平台声明 | 可选，`support_platforms` | ✅ `aiocqhttp` / `qq_official` |

通过后，市场条目会长这样（ID = `czm9919/astrbot_plugin_newapi_qq`）：

```
author=czm9919  name=astrbot_plugin_newapi_qq  version=1.0.2
download_url=https://.../astrbot_plugin_newapi_qq-1.0.2-<commit12>.zip
category=工具  stars / download_count 由平台统计
```

## 每次发版的维护顺序

自定义源是手写索引，**版本号、下载地址、MD5 三者必须一起改**，否则市场里点安装会 404
或装到旧包：

1. 改 `metadata.yaml` 的 `version`（如 `v1.0.3`）；
2. 打标签、建 Release、上传新的 `astrbot_plugin_newapi_qq-v1.0.3.zip`；
3. 改 `market/plugins.json` 的 `version` 与 `download_url`；
4. 重算 MD5 写进 `market/plugins-md5.json`：

   ```bash
   python - <<'EOF'
   import hashlib, json
   raw = open('market/plugins.json', 'rb').read()
   open('market/plugins-md5.json', 'w').write(
       json.dumps({"md5": hashlib.md5(raw).hexdigest()}) + "\n")
   print(hashlib.md5(raw).hexdigest())
   EOF
   ```

5. 提交推送。客户端下次刷新市场会因 MD5 变化重新拉索引。

> `download_url` 指向 GitHub Release 资产即可，不必像官方那样上传到 S3 ——
> 客户端只要求它是一个能直接下到 zip 的地址，且 zip 内有**单一顶层目录**（本仓库的
> 安装包就是 `astrbot_plugin_newapi_qq/` 一个目录）。

## 排障

**市场一直转圈 / 空白**

```bash
# 在跑 AstrBot 的机器上
curl -m 10 -v https://api.soulter.top/astrbot/plugins-md5
```

超时或 TLS 握手失败，就是机器到 `api.soulter.top` 不通（国内网络常见）。
此时官方源和一个 GitHub raw 上的自定义源都会失败，只有两条路：

- 给 AstrBot 进程配代理（`HTTP_PROXY` / `HTTPS_PROXY`，客户端用 `trust_env=True`，会读环境变量）；
- 或者干脆不用市场，直接上传 Release 里的 zip（WebUI 插件页 → 安装 → 选文件）。

**加了自定义源但搜不到**

- URL 必须直指 JSON，不能是仓库主页；
- 源管理里保存后要点市场右上角的**刷新**；
- 看 AstrBot 日志：成功时会打 `Fetched remote plugin marketplace data successfully; received N plugins.`，
  MD5 不匹配会打 `Plugin cache: local=..., remote=...`。

**能看到但安装失败**

装的是 `download_url` 那个 zip。若 GitHub Release 资产被墙，删除条目里的
`download_url` 字段，客户端会退回 `repo` 的 GitHub 归档地址 —— 两者都在 github.com 上，
所以这条只对「GitHub 可达但 release 域名不可达」的奇怪网络有意义。
