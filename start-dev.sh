#!/usr/bin/env bash
# new-api 二次开发：一键构建并启动（后端 + 嵌入的前端）
# 用法：在 new-api-1.0.0-rc.43 目录下执行  bash start-dev.sh
set -e

cd "$(dirname "$0")"

# 后端用纯 Go SQLite，无需 CGO
export CGO_ENABLED=0
# 中国镜像，加速 Go 模块拉取
export GOPROXY=https://goproxy.cn,direct
export GOSUMDB=off

# 1) 前端未构建则先构建（产出 web/dist，供 Go 嵌入）
if [ ! -d "web/dist" ] || [ ! -f "web/dist/index.html" ]; then
  echo ">>> 前端 web/dist 不存在，开始构建前端..."
  cd web
  bun install
  DISABLE_ESLINT_PLUGIN=true VITE_REACT_APP_VERSION=1.0.0-rc.43 bun run build
  cd ..
fi

# 2) 构建后端二进制（嵌入 web/dist）
if [ ! -f "new-api.exe" ] && [ ! -f "new-api" ]; then
  echo ">>> 构建后端二进制..."
  go build -o new-api.exe .
fi

# 3) 启动（.env 会被自动加载；监听 PORT=3000）
echo ">>> 启动 new-api (http://localhost:3000) ..."
exec ./new-api.exe
