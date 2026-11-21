"""v1.0.1 插件自检：不需要 AstrBot，也不需要站点。

用三个桩模块把 main.py 跑起来，验证本轮加固的五个点：
  1. 成功冷却 30 秒
  2. 失败冷却 3 秒
  3. 配置可覆盖冷却（含脏值回落）
  4. terminate() 清空内存态
  5. 429 映射 / 异常不外泄
"""

import asyncio
import importlib.util
import sys
import time
import types
from pathlib import Path

_CANDIDATES = [
    Path(__file__).resolve().parent / "main.py",
    Path("integrations/astrbot_plugin_newapi_qq/main.py"),
]
PLUGIN_MAIN = (
    Path(sys.argv[1])
    if len(sys.argv) > 1
    else next((p for p in _CANDIDATES if p.exists()), _CANDIDATES[0])
)
if not PLUGIN_MAIN.exists():
    sys.exit(f"main.py not found: {PLUGIN_MAIN}")

# ----------------------------------------------------------------- stubs
api = types.ModuleType("astrbot.api")
api.logger = __import__("logging").getLogger("plugin-selftest")

ev = types.ModuleType("astrbot.api.event")


class _Filter:
    class EventMessageType:
        ALL = "all"

    def command(self, _name):
        return lambda fn: fn

    def event_message_type(self, _t):
        return lambda fn: fn


ev.filter = _Filter()
ev.AstrMessageEvent = object

star = types.ModuleType("astrbot.api.star")


class Star:
    def __init__(self, context=None):
        pass


class Context:
    pass


star.Star = Star
star.Context = Context
star.register = lambda *a, **k: (lambda cls: cls)

sys.modules.update(
    {
        "astrbot": types.ModuleType("astrbot"),
        "astrbot.api": api,
        "astrbot.api.event": ev,
        "astrbot.api.star": star,
    }
)


class Result:
    def __init__(self, text):
        self.text = text


class FakeEvent:
    def __init__(self, qq="123456789", explode=False):
        self._qq = qq
        self._explode = explode

    def get_sender_id(self):
        return self._qq

    def plain_result(self, text):
        if self._explode:
            raise RuntimeError("plain_result boom")
        return Result(text)


# ------------------------------------------------------------------ load
spec = importlib.util.spec_from_file_location("newapi_qq_plugin", PLUGIN_MAIN)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

PASS, FAIL = [], []


def check(name, ok, detail=""):
    (PASS if ok else FAIL).append(name)
    print(("PASS " if ok else "FAIL ") + name + (("  <- " + detail) if detail and not ok else ""))


def build(config, handler):
    plugin = mod.NewApiQQBindPlugin(None, config)
    return plugin, handler(plugin)


async def run(plugin, event, code):
    out = []
    async for item in plugin._handle_bind(event, code):
        out.append(item.text if hasattr(item, "text") else str(item))
    return out


def main():
    # ---- 1. 成功冷却 ---------------------------------------------------
    async def scenario_success():
        cfg = {"agent_token": "x" * 32}
        plugin = mod.NewApiQQBindPlugin(None, cfg)

        calls = []

        async def fake_call(method, path, payload=None):
            calls.append((method, path))
            return 200, {"success": True, "data": {"username": "alice", "qq": "123****89"}}

        plugin._call = fake_call
        first = await run(plugin, FakeEvent(), "ABCD2345")
        second = await run(plugin, FakeEvent(), "ABCD2345")
        return first, second, plugin

    first, second, plugin = asyncio.run(scenario_success())
    check("成功绑定返回站点账号", any("alice" in t for t in first), repr(first))
    check("成功后立刻再来一次被本地拦下", any("操作太快了" in t for t in second), repr(second))
    check("冷却提示里的秒数是正数", any("请" in t and "秒后再试" in t for t in second), repr(second))
    remain = plugin._cooldown_remaining("123456789")
    check("成功后剩余冷却落在 25~31 秒", 25 <= remain <= 31, f"remain={remain}")

    # ---- 2. 失败冷却 ---------------------------------------------------
    async def scenario_fail():
        cfg = {"agent_token": "x" * 32}
        plugin = mod.NewApiQQBindPlugin(None, cfg)

        async def fake_call(method, path, payload=None):
            return 400, {"code": "QQ_BIND_CODE_INVALID"}

        plugin._call = fake_call
        first = await run(plugin, FakeEvent(), "WRONGCOD")
        remain = plugin._cooldown_remaining("123456789")
        second = await run(plugin, FakeEvent(), "WRONGCOD")
        return first, second, remain

    first, second, remain = asyncio.run(scenario_fail())
    check("失败返回错误码映射文案", any("绑定码无效" in t for t in first), repr(first))
    check("失败后冷却约 3 秒", 1 <= remain <= 3, f"remain={remain}")
    check("失败后立刻重试被拦", any("操作太快了" in t for t in second), repr(second))

    # ---- 3. 配置覆盖 ---------------------------------------------------
    async def scenario_override():
        cfg = {"agent_token": "x" * 32, "cooldown_seconds": 0, "retry_cooldown_seconds": 0}

        async def ok_call(method, path, payload=None):
            return 200, {"success": True, "data": {"username": "bob"}}

        plugin = mod.NewApiQQBindPlugin(None, cfg)
        plugin._call = ok_call
        await run(plugin, FakeEvent(), "ABCD2345")
        return plugin._cooldown_remaining("123456789")

    check("cooldown_seconds=0 时不冷却", asyncio.run(scenario_override()) == 0)

    async def scenario_dirty():
        cfg = {"agent_token": "x" * 32, "cooldown_seconds": "abc", "retry_cooldown_seconds": -5}

        async def ok_call(method, path, payload=None):
            return 200, {"success": True, "data": {"username": "bob"}}

        plugin = mod.NewApiQQBindPlugin(None, cfg)
        plugin._call = ok_call
        await run(plugin, FakeEvent(), "ABCD2345")
        return plugin._cooldown_remaining("123456789")

    dirty_remain = asyncio.run(scenario_dirty())
    check("脏配置回落默认 30 秒", 25 <= dirty_remain <= 31, f"remain={dirty_remain}")

    # ---- 4. terminate 清场 ---------------------------------------------
    asyncio.run(plugin.terminate())
    check(
        "terminate() 清空两个字典",
        not plugin._last_attempt and not plugin._last_success,
        f"{plugin._last_attempt} / {plugin._last_success}",
    )

    # ---- 5. 429 与异常兜底 ---------------------------------------------
    p = mod.NewApiQQBindPlugin(None, {"agent_token": "x" * 32})
    text = p._error_text(429, {})
    check("429 映射到友好文案", "太频繁" in text, text)

    class Boom(mod.NewApiQQBindPlugin):
        async def _handle_bind_inner(self, event, code):
            raise RuntimeError("inner boom")
            yield  # pragma: no cover

    async def scenario_boom():
        inst = Boom(None, {"agent_token": "x" * 32})
        return await run(inst, FakeEvent(), "ABCD2345")

    out = asyncio.run(scenario_boom())
    check("内部异常被外层接住并回话", any("处理绑定时出错了" in t for t in out), repr(out))

    async def scenario_boom_event():
        inst = Boom(None, {"agent_token": "x" * 32})
        # plain_result 本身炸掉时，外层不能再抛
        out2 = await run(inst, FakeEvent(explode=True), "ABCD2345")
        return out2

    try:
        out2 = asyncio.run(scenario_boom_event())
        check("plain_result 也炸时不再抛异常", out2 == [], repr(out2))
    except Exception as exc:  # noqa: BLE001
        check("plain_result 也炸时不再抛异常", False, repr(exc))

    # ---- 6. 状态字典不会无限长 -----------------------------------------
    p2 = mod.NewApiQQBindPlugin(None, {})
    old = time.time() - 7200
    p2._last_attempt = {f"q{i}": old for i in range(mod.COOLDOWN_STATE_SWEEP_THRESHOLD + 10)}
    p2._last_success = {f"q{i}": old for i in range(mod.COOLDOWN_STATE_SWEEP_THRESHOLD + 10)}
    p2._mark_attempt("fresh", False)
    check(
        "超阈值后旧条目被清理",
        len(p2._last_attempt) == 1 and "fresh" in p2._last_attempt,
        f"size={len(p2._last_attempt)}",
    )

    print()
    print(f"{len(PASS)} passed, {len(FAIL)} failed")
    return 1 if FAIL else 0


if __name__ == "__main__":
    sys.exit(main())
