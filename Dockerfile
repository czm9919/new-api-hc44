# syntax=docker/dockerfile:1

# =============================================================================
# 三角洲行动 API 中转站（new-api 定制版）—— 全量自构建镜像
#
# 前端（React + rsbuild）与后端（Go）全部在容器内完成，产物为单二进制，
# 内嵌 web/dist，运行时不需要 Node/Bun。
#
#   docker build -t delta-api/new-api:local .
#   docker run -d --name new-api -p 3000:3000 -v $(pwd)/data:/data delta-api/new-api:local
#
# 想跳过前端编译（本机已有 web/dist）请改用 Dockerfile.local-dist。
# =============================================================================

# ---------------------------------------------------------------- 1) 前端构建
FROM oven/bun:1 AS web

WORKDIR /src/web
# 先只拷依赖清单，命中缓存后改业务代码不会重装依赖
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile

COPY web/ ./
RUN bun run build


# ------------------------------------------------- 2) 后端构建（内嵌前端产物）
FROM golang:1.27-alpine AS build

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOWORK=off \
    GO111MODULE=on \
    GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=off

# 默认跟随构建机架构，交叉构建时传 --platform 即可
ARG TARGETARCH=amd64
ENV GOARCH=${TARGETARCH}

WORKDIR /src

# relaykit / tokenkit 是通过 replace 引用的本地子模块，
# 它们自己的 go.mod 必须先就位，否则 go mod download 解析不了模块图。
COPY go.mod go.sum ./
COPY relaykit/go.* ./relaykit/
COPY tokenkit/go.* ./tokenkit/
RUN go mod download

COPY . ./
# 先清掉构建上下文里可能存在的旧 dist，再放入容器内新构建的产物，
# 否则新旧哈希文件混在一起会被 go:embed 一起打进二进制。
RUN rm -rf ./web/dist
COPY --from=web /src/web/dist ./web/dist

# 版本号默认从 VERSION 文件读取，避免镜像里的版本与实际树不一致；
# 需要覆盖时用 --build-arg APP_VERSION=xxx。
#
# 注意：RUN 里的 ${APP_VERSION} 会被 Docker 用 ARG 的值在「解析期」替换，
# 所以不能直接写 -X "...Version=${APP_VERSION}"——ARG 为空时会变成空串。
# 这里先落到 shell 变量 ver（Docker 不认识它，会原样留给 shell 展开）。
ARG APP_VERSION=""
RUN ver="${APP_VERSION}"; \
    [ -n "$ver" ] || ver="$(cat VERSION)"; \
    echo "构建版本：$ver"; \
    go build -trimpath \
        -ldflags "-s -w -X github.com/QuantumNous/new-api/common.Version=$ver" \
        -o /out/new-api .


# ------------------------------------------------------------------ 3) 运行层
# 后端是 CGO_ENABLED=0 的静态二进制，用 alpine 即可，镜像比 debian 小一个数量级。
FROM alpine:3.21

# tzdata 供 Asia/Shanghai 时区与每日 00:00 重置使用；wget 供健康检查使用
RUN apk add --no-cache ca-certificates tzdata wget \
    && update-ca-certificates

COPY --from=build /out/new-api /new-api
COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md /licenses/

# 关键：程序自带的默认库名是 one-api.db，而线上跑的是 new-api.db。
# 不显式指定的话，把旧库拷进 /data 也不会被读到（会另起一个空库）。
# 顺带把默认 DSN 里的 WAL / busy_timeout / txlock 补回来——
# 只写裸路径会丢掉这几个 pragma，并发下容易 SQLITE_BUSY。
ENV TZ=Asia/Shanghai \
    SQLITE_PATH="/data/new-api.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate"

EXPOSE 3000
WORKDIR /data
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=10s --start-period=40s --retries=3 \
    CMD wget -q -O - http://127.0.0.1:3000/api/status | grep -q '"success":true' || exit 1

ENTRYPOINT ["/new-api"]
CMD ["--port", "3000", "--log-dir", "/data/logs"]
