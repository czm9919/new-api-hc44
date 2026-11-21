"""
AstrBot 插件：把 QQ 号绑定到 New API 面板账号。

设计要点：
  * 站点不接触 QQ 协议，只信任「谁在发消息」这一个事实；
  * 绑定凭据是面板生成的一次性码（8 位、10 分钟、用过即废）；
  * 本插件用管理员下发的服务令牌调用站点 /api/qq/agent/verify。

除 AstrBot 自带依赖外只用标准库（urllib），避免额外安装 requirements。
"""

from __future__ import annotations

import asyncio
import json
import time
import urllib.error
import urllib.request

from astrbot.api import logger
from astrbot.api.event import AstrMessageEvent, filter
from astrbot.api.star import Context, Star, register

PLUGIN_NAME = "astrbot_plugin_newapi_qq"

# 本地冷却分段口径：
#   * 成功锁 30 秒——绑定不可重入，连点只会白打请求；
#   * 失败锁 3 秒——打错一位的用户要能立刻重试，但连点要被挡下。
# 只锁成功挡不住拿错误码刷接口的人，只锁失败又把正常重试变成折磨，所以分开计。
# 两个数值都能在插件配置里覆盖（cooldown_seconds / retry_cooldown_seconds）。
COOLDOWN_OK_DEFAULT = 30
COOLDOWN_FAIL_DEFAULT = 3
COOLDOWN_STATE_SWEEP_THRESHOLD = 2048

# 站点错误码 → 给用户看的中文提示
ERROR_HINTS = {
    "QQ_BIND_CODE_INVALID": "绑定码无效或已过期，请回面板重新生成（10 分钟内有效）。",
    "QQ_ALREADY_BOUND": "这个 QQ 已经绑定到别的账号了，请先在原账号解绑。",
    "QQ_ACCOUNT_ALREADY_BOUND": "你的账号已经绑定了别的 QQ，请先在面板解绑。",
    "QQ_AGENT_UNAUTHORIZED": "机器人令牌无效，请联系管理员检查配置。",
    "QQ_AGENT_UNAVAILABLE": "站点尚未开启机器人接入（未配置服务令牌），请联系管理员。",
    "QQ_AGENT_RATE_LIMITED": "操作太频繁了，请过一会儿再试。",
    "INVALID_REQUEST": "请求格式不对，请检查绑定码。",
}


@register(
    PLUGIN_NAME,
    "new-api-operator",
    "把 QQ 号绑定到 New API 面板账号，解锁幸运大转盘与免费模型",
    "1.0.0",
)
class NewApiQQBindPlugin(Star):
    def __init__(self, context: Context, config: dict | None = None):
        super().__init__(context)
        self.config = config or {}
        # 纯内存冷却状态：进程重启即清零，不落库，与站点侧限流互为备份
        self._last_attempt: dict[str, float] = {}
        self._last_success: dict[str, float] = {}

    # ------------------------------------------------------------------ utils

    def _cfg(self, key: str, default=""):
        value = self.config.get(key, default)
        if isinstance(value, str):
            return value.strip()
        return value if value is not None else default

    def _base_url(self) -> str:
        base = self._cfg("api_base", "http://127.0.0.1:3000").rstrip("/")
        # 容错：用户可能填了 .../api
        if base.endswith("/api"):
            base = base[: -len("/api")]
        return base

    def _request(self, method: str, path: str, payload: dict | None = None) -> tuple[int, dict]:
        """同步 HTTP 调用，放到线程池里执行。返回 (status, body_json)。"""
        url = self._base_url() + path
        data = None
        headers = {"Accept": "application/json"}
        token = self._cfg("agent_token")
        if token:
            headers["X-QQ-Agent-Token"] = token
        if payload is not None:
            data = json.dumps(payload).encode("utf-8")
            headers["Content-Type"] = "application/json"

        request = urllib.request.Request(url, data=data, headers=headers, method=method)
        timeout = float(self._cfg("timeout_seconds", 10) or 10)
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                raw = response.read().decode("utf-8", errors="replace")
                return response.status, _safe_json(raw)
        except urllib.error.HTTPError as exc:
            raw = exc.read().decode("utf-8", errors="replace")
            return exc.code, _safe_json(raw)
        except Exception as exc:  # 网络层失败
            logger.warning(f"[{PLUGIN_NAME}] request failed: {exc}")
            return 0, {"message": str(exc)}

    async def _call(self, method: str, path: str, payload: dict | None = None):
        return await asyncio.to_thread(self._request, method, path, payload)

    def _sender_qq(self, event: AstrMessageEvent) -> str:
        getter = getattr(event, "get_sender_id", None)
        if callable(getter):
            try:
                return str(getter())
            except Exception:  # pragma: no cover - 框架差异
                pass
        return ""

    # --------------------------------------------------------------- cooldown

    def _window(self, key: str, default: int) -> float:
        """读冷却秒数；配置写错（负数/字符串/None）一律回落默认值，绝不抛。"""
        raw = self._cfg(key, default)
        try:
            value = float(raw)
        except (TypeError, ValueError):
            return float(default)
        return value if value >= 0 else float(default)

    def _cooldown_remaining(self, qq: str) -> int:
        """还需等待的秒数；0 表示现在可以执行。"""
        now = time.time()
        wait = 0.0
        ok_at = self._last_success.get(qq)
        if ok_at is not None:
            wait = max(wait, ok_at + self._window("cooldown_seconds", COOLDOWN_OK_DEFAULT) - now)
        fail_at = self._last_attempt.get(qq)
        if fail_at is not None:
            wait = max(wait, fail_at + self._window("retry_cooldown_seconds", COOLDOWN_FAIL_DEFAULT) - now)
        return int(wait) + 1 if wait > 0 else 0

    def _mark_attempt(self, qq: str, success: bool) -> None:
        now = time.time()
        self._last_attempt[qq] = now
        if success:
            self._last_success[qq] = now
        self._sweep_state()

    def _sweep_state(self) -> None:
        """字典只增不减会在长跑机器人上攒垃圾，超阈值时清掉一小时前的条目。"""
        if len(self._last_attempt) <= COOLDOWN_STATE_SWEEP_THRESHOLD:
            return
        cutoff = time.time() - 3600
        self._last_attempt = {k: v for k, v in self._last_attempt.items() if v >= cutoff}
        self._last_success = {k: v for k, v in self._last_success.items() if v >= cutoff}

    async def terminate(self):
        """插件卸载/重载时清空内存态，避免热重载后残留冷却。"""
        self._last_attempt.clear()
        self._last_success.clear()

    # ---------------------------------------------------------------- commands

    @filter.command("绑定")
    async def bind(self, event: AstrMessageEvent, code: str | None = None):
        """绑定个人 QQ 到 New API 面板账号：/绑定 <绑定码>"""
        async for result in self._handle_bind(event, code):
            yield result

    @filter.command("bind")
    async def bind_en(self, event: AstrMessageEvent, code: str | None = None):
        """/bind <code> — English alias."""
        async for result in self._handle_bind(event, code):
            yield result

    async def _handle_bind(self, event: AstrMessageEvent, code: str | None):
        """外层兜底。

        插件抛出的异常会被 AstrBot 的钩子链路放大——社区里有插件因为一个未捕获
        异常把整个 AstrBot 进程带崩。这里保证：内部怎么炸，用户至少收到一句可读
        的话，异常绝不向上冒泡。
        """
        try:
            async for result in self._handle_bind_inner(event, code):
                yield result
        except Exception as exc:  # pragma: no cover - 防御性
            logger.error(f"[{PLUGIN_NAME}] bind handler crashed: {exc!r}")
            try:
                yield event.plain_result("处理绑定时出错了，请稍后再试一次。")
            except Exception:
                pass

    async def _handle_bind_inner(self, event: AstrMessageEvent, code: str | None):
        code = (code or "").strip()
        qq = self._sender_qq(event)

        if not qq:
            yield event.plain_result("拿不到你的 QQ 号，请私聊机器人使用本命令。")
            return

        if not self._cfg("agent_token"):
            yield event.plain_result(
                "机器人还没配置服务令牌，请联系站点管理员在面板填写（qq_agent_token）。"
            )
            return

        # 自检：/绑定 测试
        if code in ("测试", "test", "ping"):
            status, body = await self._call("GET", "/api/qq/agent/ping")
            if status == 200 and body.get("success"):
                data = body.get("data") or {}
                yield event.plain_result(
                    "站点连接正常。免费模型绑定要求："
                    + ("开启" if data.get("require_qq_free") else "关闭")
                )
            else:
                yield event.plain_result(self._error_text(status, body))
            return

        remaining = self._cooldown_remaining(qq)
        if remaining > 0:
            yield event.plain_result(f"操作太快了，请 {remaining} 秒后再试。")
            return

        if not code:
            yield event.plain_result(
                "用法：/绑定 <绑定码>\n"
                "绑定码请到面板「幸运大转盘 → QQ 绑定」生成，10 分钟内有效。"
            )
            return

        status, body = await self._call(
            "POST", "/api/qq/agent/verify", {"code": code, "qq": qq}
        )
        if status == 200 and body.get("success"):
            data = body.get("data") or {}
            username = data.get("username") or "未知"
            self._mark_attempt(qq, True)
            yield event.plain_result(
                f"绑定成功 ✅\n站点账号：{username}\nQQ：{data.get('qq', qq)}\n"
                "现在可以回面板抽奖、使用免费模型了。"
            )
            return

        self._mark_attempt(qq, False)
        yield event.plain_result(self._error_text(status, body))

    # 自定义命令名（配置项 bind_command）：只在名字不是默认的两个时才接管
    @filter.event_message_type(filter.EventMessageType.ALL)
    async def alias_router(self, event: AstrMessageEvent):
        custom = self._cfg("bind_command", "绑定")
        if custom in ("", "绑定", "bind"):
            return
        text = (event.message_str or "").strip()
        for prefix in (f"/{custom}", custom):
            if text.startswith(prefix):
                code = text[len(prefix):].strip()
                # 去掉可能的分隔符
                code = code.lstrip(" :：").strip()
                async for result in self._handle_bind(event, code):
                    yield result
                event.stop_event()
                return

    # ------------------------------------------------------------------ helpers

    def _error_text(self, status: int, body: dict) -> str:
        if status == 0:
            return (
                "站点连接失败，请稍后重试。\n"
                f"（管理员检查 api_base 是否可达：{self._base_url()}）"
            )
        if status == 429:
            # 站点限流中间件只回状态码 + Retry-After，没有 JSON body
            return ERROR_HINTS["QQ_AGENT_RATE_LIMITED"]
        code = str(body.get("code") or "")
        if code in ERROR_HINTS:
            return ERROR_HINTS[code]
        message = body.get("message") or body.get("msg") or ""
        if status == 503:
            return ERROR_HINTS["QQ_AGENT_UNAVAILABLE"]
        if status == 401:
            return ERROR_HINTS["QQ_AGENT_UNAUTHORIZED"]
        return f"绑定失败（HTTP {status}）{('：' + message) if message else ''}"


def _safe_json(raw: str) -> dict:
    try:
        parsed = json.loads(raw)
        return parsed if isinstance(parsed, dict) else {"data": parsed}
    except Exception:
        return {"message": raw[:200]}
