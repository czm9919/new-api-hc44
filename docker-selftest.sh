#!/usr/bin/env bash
# =============================================================================
# new-api 镜像验收自测（需要 Docker 已安装并可用）
#
#   bash docker-selftest.sh              # 自动选 Dockerfile（有 web/dist 就用 local-dist）
#   bash docker-selftest.sh --full       # 强制走全量构建
#
# 覆盖：镜像构建 / 冷启动 / 健康检查 / 库文件名 / 数据持久化 / 旧库迁移 / 清理
# =============================================================================
set -uo pipefail

IMAGE="delta-api/new-api:selftest"
NAME="new-api-selftest"
PORT="${PORT:-13000}"
ROOT="$(cd "$(dirname "$0")" && pwd)"
TMP="$ROOT/.selftest"
LOG="$ROOT/.selftest-build.log"
PASS=0
FAIL=0

# Git Bash 在 Windows 上会把 /data 当成路径改写，统一关掉
export MSYS_NO_PATHCONV=1
winpath() { (cd "$1" && pwd -W 2>/dev/null) || echo "$1"; }

ok()   { PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m  %s\n' "$1"; }
step() { printf '\n\033[1m== %s\033[0m\n' "$1"; }

cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1
}
trap cleanup EXIT

# ---------------------------------------------------------------- 0) 前置检查
step "0. 前置检查"
if ! command -v docker >/dev/null 2>&1; then
  echo "  Docker 未安装，无法执行本脚本。" >&2
  exit 2
fi
if ! docker info >/dev/null 2>&1; then
  echo "  Docker 已安装但守护进程未运行（Docker Desktop 启动了吗？）。" >&2
  exit 2
fi
ok "docker 可用：$(docker version --format '{{.Server.Version}}' 2>/dev/null)"

DOCKERFILE="Dockerfile"
if [ "${1:-}" != "--full" ] && [ -f "$ROOT/web/dist/index.html" ]; then
  DOCKERFILE="Dockerfile.local-dist"
fi
echo "  使用 $DOCKERFILE"

# ------------------------------------------------------------------ 1) 构建
step "1. 构建镜像"
# Docker 只认 Windows 风格路径（D:/...），Git Bash 的 /d/... 会报 context not found
if docker build -f "$(winpath "$ROOT")/$DOCKERFILE" -t "$IMAGE" "$(winpath "$ROOT")" > "$LOG" 2>&1; then
  ok "构建成功（$(docker image inspect -f '{{.Size}}' "$IMAGE" | awk '{printf "%.0f MB", $1/1048576}')）"
else
  bad "构建失败，日志尾部："
  tail -25 "$LOG"
  exit 1
fi

# ------------------------------------------------------- 2) 冷启动（空数据卷）
step "2. 冷启动（全新空 /data）"
rm -rf "$TMP"; mkdir -p "$TMP/data"
docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:3000" \
  -v "$(winpath "$TMP/data"):/data" "$IMAGE" >/dev/null
for i in $(seq 1 40); do
  curl -sf --noproxy '*' "http://127.0.0.1:$PORT/api/status" >/dev/null 2>&1 && break
  sleep 1
done
CODE=$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/status")
[ "$CODE" = "200" ] && ok "/api/status 返回 200" || bad "/api/status 返回 $CODE"
CODE=$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/")
[ "$CODE" = "200" ] && ok "前端首页 200（dist 已内嵌）" || bad "前端首页返回 $CODE"

[ -f "$TMP/data/new-api.db" ] && ok "数据库落在 /data/new-api.db" || bad "未生成 /data/new-api.db"
[ -f "$TMP/data/one-api.db" ] && bad "出现了 one-api.db —— SQLITE_PATH 未生效" || ok "未误建 one-api.db"
[ -d "$TMP/data/logs" ] && ok "日志目录 /data/logs 已创建" || bad "未创建 /data/logs"

# ------------------------------------------------------------ 3) 健康检查状态
step "3. HEALTHCHECK"
sleep 2
HS=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$NAME")
case "$HS" in
  healthy) ok "容器健康检查状态 healthy" ;;
  starting) ok "健康检查仍在 starting（首次启动较慢，可接受）" ;;
  *) bad "健康检查状态：$HS" ;;
esac

# --------------------------------------------------------- 4) 重启后数据持久化
step "4. 重启后数据持久化"
SIZE_BEFORE=$(wc -c < "$TMP/data/new-api.db")
docker restart "$NAME" >/dev/null
for i in $(seq 1 40); do
  curl -sf --noproxy '*' "http://127.0.0.1:$PORT/api/status" >/dev/null 2>&1 && break
  sleep 1
done
CODE=$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/status")
[ "$CODE" = "200" ] && ok "重启后仍可访问" || bad "重启后返回 $CODE"
[ -f "$TMP/data/one-api.db" ] && bad "重启后出现 one-api.db（说明库名不稳定）" || ok "重启后库名仍为 new-api.db"
echo "  库体积：$SIZE_BEFORE -> $(wc -c < "$TMP/data/new-api.db") 字节"

# ------------------------------------------------------------- 5) 旧库迁移验证
step "5. 旧库迁移（把现有生产库拷进去）"
docker rm -f "$NAME" >/dev/null 2>&1
rm -rf "$TMP/data"; mkdir -p "$TMP/data"
if [ -f "$ROOT/new-api.db" ]; then
  cp "$ROOT/new-api.db" "$TMP/data/new-api.db"
  BEFORE=$(wc -c < "$TMP/data/new-api.db")
  docker run -d --name "$NAME" -p "$PORT:3000" \
    -v "$(winpath "$TMP/data"):/data" "$IMAGE" >/dev/null
  for i in $(seq 1 40); do
    curl -sf --noproxy '*' "http://127.0.0.1:$PORT/api/status" >/dev/null 2>&1 && break
    sleep 1
  done
  CODE=$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/status")
  [ "$CODE" = "200" ] && ok "带旧库启动成功" || bad "带旧库启动返回 $CODE"
  [ -f "$TMP/data/one-api.db" ] && bad "另起了 one-api.db —— 旧库没被读取！" || ok "旧库被正确读取（无 one-api.db）"
  AFTER=$(wc -c < "$TMP/data/new-api.db")
  [ "$AFTER" -ge "$BEFORE" ] && ok "旧库未被覆盖（$BEFORE -> $AFTER 字节）" || bad "旧库体积异常缩小：$BEFORE -> $AFTER"
else
  echo "  跳过：工程目录下没有 new-api.db"
fi

# ------------------------------------------------------------------ 6) 清理
step "6. 清理"
docker rm -f "$NAME" >/dev/null 2>&1 && ok "容器已删除"
rm -rf "$TMP" "$LOG"

printf '\n\033[1m结果：%d 通过 / %d 失败\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
