# hc44 · 官方价同步（basellm + models.dev + LiteLLM）

## 为什么要改

hc43 的「一键同步官方定价」只吃 **basellm** 一个源（403 条 `billing_expr`），
而本站 85 个模型是 NVIDIA NIM 目录里的 id，basellm 只覆盖 7 个。
剩下的模型落到 `GetModelRatio` 的 37.5 兜底 → 站内显示 **$75 / 1M tokens**，
这就是「上游价格不对」的直接来源。

## 现在怎么取价

三个源，全部是公开的厂商价/官方价表，**不做任何估算或分档**：

| 源 | 地址 | 条目 | 说明 |
| --- | --- | --- | --- |
| basellm | `basellm.github.io/llm-metadata/api/newapi/ratio_config-v1-base.json` | 403 | 官方 `billing_expr`，用计费引擎求值 |
| models.dev | `models.dev/api.json` | 3399 | 各家 provider 的 `$/1M` |
| LiteLLM | `raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json` | 4224 | 厂商价表，覆盖面最广 |

合并优先级 **basellm > models.dev > litellm**；同名多写法时「有价优先 → 来源可信度」。

## 匹配口径

`officialPriceIndex` 三级降级，价格榜与落库共用同一套：

1. 精确名（`nemotron-3-super-120b-a12b`）
2. 去厂商前缀（`nvidia/xxx` → `xxx`）
3. 去版本/量化/上下文尾巴（`-instruct` `-it` `-v1` `-128k` `-fp8` `-2025-04-14` …）

官方价 0（源里标成免费/占位）不参与覆盖，也不在价格榜上显示 0 折扣。

## 一键同步

管理端 → 系统设置 → 模型 → **一键同步官方定价**：

- 前端打 `POST /api/ratio_sync/fetch`，`upstreams[0].id = -103`（合并预设）；
- 后端 `buildMergedOfficialRatioData` 读快照，按**本站模型名**出键返回；
- 用户在预览里确认后 `PATCH /api/option/model_pricing` 落库（`ModelRatio` / `CompletionRatio` / `CacheRatio`）。

换算：`ModelRatio = 官方 $/1M ÷ 2`（`QuotaPerUnit = 500000`），`CompletionRatio = out / in`。

独立预设仍可单独选：`-100` basellm、`-101` models.dev、`-102` LiteLLM。

## 当前覆盖（hc44 实测）

- 官方价命中 **37 / 85**，站点价与官方价 0 差异（`/api/pricing/leaderboard` 校验）；
- **48 个**模型在四个公开源（含 OpenRouter）里都没有按 token 报价，例如
  `nvidia/nemotron-parse`、`nvidia/embed-qa-4`、`nvidia/nvclip`、
  `writer/palmyra-med-70b`、`zyphra/zamba2-7b-instruct` —— 这些是 NIM 目录里
  NVIDIA 不按 token 售卖（按 GPU 时/免费额度）或厂商已下架的条目。
  它们保持平台默认 37.5，**不会被编一个价**。
- 明细：`price-probe/official-sync-report.csv`（含 `usd_in_per_1m` / `usd_out_per_1m` / `applied`）。

## 验证

```bash
python hc44-official-sync.py prepare    # 插临时 root
python hc44-official-sync.py run        # 刷新快照 → 合并预设 → 落库 → 校验
python hc44-official-sync.py cleanup
```

15/15 通过：三源快照、合并预设 37 行、PATCH 200、公开价格页与官方倍率一致。
