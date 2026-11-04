# 多阶段构建（设计文档 §5.2）：前端 → 后端静态单文件 → alpine 运行时，最终镜像 ~25MB。
# 构建：docker build -t pixsy .
# 运行：docker run -d -p 8080:8080 -v ./data:/app/data pixsy

# 阶段 1：构建前端产物到 web/dist
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# 阶段 2：编译后端（CGO_ENABLED=0 + 纯 Go SQLite，产物为静态单文件）
FROM golang:1.23-alpine AS app
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# go:embed 需要 web/dist 存在；用前端阶段的产物覆盖，保证与源码同步
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /pixsy ./cmd/pixsy

# 阶段 3：运行时镜像（静态二进制，无需额外依赖）
FROM alpine:3.20
COPY --from=app /pixsy /pixsy
WORKDIR /app
EXPOSE 8080
VOLUME /app/data
ENTRYPOINT ["/pixsy"]
