# ============================================================
# Token Factory - 多阶段构建 Dockerfile
# ============================================================
# 阶段1: 构建前端 (Vue 3 + Vite)
# 阶段2: 构建后端 (Go + embed 嵌入前端产物)
# 阶段3: 最小化运行镜像
# ============================================================

# -------------------- 阶段1: 构建前端 --------------------
FROM node:20-alpine AS frontend-builder

WORKDIR /build/web

# 先复制依赖文件，利用 Docker 缓存层加速构建
COPY web/package.json web/package-lock.json ./

# 安装前端依赖
RUN npm ci --prefer-offline

# 复制前端源代码
COPY web/ ./

# 构建前端，产物输出到 ../server/web/dist (由 vite.config.ts 决定)
RUN npm run build

# -------------------- 阶段2: 构建后端 --------------------
FROM golang:1.21-alpine AS backend-builder

WORKDIR /build

# 先复制依赖文件，利用 Docker 缓存层
COPY server/go.mod server/go.sum ./

# 下载 Go 依赖
RUN go mod download

# 复制后端源代码
COPY server/ ./

# 从前端构建阶段复制构建产物到嵌入目录
COPY --from=frontend-builder /build/server/web/dist ./web/dist/

# 编译 Go 二进制（静态链接，禁用 CGO 以适配 Alpine）
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /token-factory .

# -------------------- 阶段3: 运行镜像 --------------------
FROM alpine:3.19

# 安装运行时依赖（ca-certificates 用于 HTTPS 请求，tzdata 用于时区支持）
RUN apk add --no-cache ca-certificates tzdata \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone \
    && apk del tzdata

# 创建非 root 用户运行服务
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# 创建数据目录（SQLite 数据库存放位置）
RUN mkdir -p /app/data && chown -R appuser:appgroup /app

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=backend-builder /token-factory ./

# 复制默认配置文件
COPY config.yaml ./

# 确保数据目录权限
RUN chown -R appuser:appgroup /app

# 切换到非 root 用户
USER appuser

# 暴露端口
# 11444: API 代理服务端口
# 8080:  Web 管理界面端口
EXPOSE 11444 8080

# 数据目录作为卷挂载点（持久化 SQLite 数据库）
VOLUME ["/app/data"]

# 健康检查：检测管理端服务是否可用
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/ || exit 1

# 启动服务
ENTRYPOINT ["./token-factory"]
CMD ["config.yaml"]
