# pixsy 部署与开发指南

> 面向首次接触本项目的使用者：从零开始在本机完成构建、部署、开发环境搭建。
> 阅读前提：基本的命令行操作经验。本文所有命令在 Windows（PowerShell / Git Bash）与 Linux（bash）下均可执行，差异处会单独标注。
>
> 相关文档：[README.md](../README.md)（功能与配置速查）、[docs/design.md](design.md)（设计文档）。

## 目录

1. [项目概览](#1-项目概览)
2. [环境准备](#2-环境准备)
3. [从源码构建并运行（本地裸机方式）](#3-从源码构建并运行本地裸机方式)
4. [配置详解](#4-配置详解)
5. [首次使用：注册管理员与基本操作](#5-首次使用注册管理员与基本操作)
6. [Docker 部署](#6-docker-部署)
7. [生产环境部署](#7-生产环境部署)
8. [开发环境搭建](#8-开发环境搭建)
9. [CI/CD 说明](#9-cicd-说明)
10. [常见问题排查](#10-常见问题排查)

---

## 1. 项目概览

pixsy 是一个用 Go + Vue 3 编写的自托管图床服务，核心特点：

- **单二进制交付**：前端构建产物经 `go:embed` 内嵌进后端，配合纯 Go 的 SQLite 驱动（无 CGO），最终产物只有一个可执行文件。
- **零外部依赖**：不需要安装数据库、Redis 或任何外部服务，数据全部落在本地 `data/` 目录。
- **开箱即用**：默认监听 `:8080`，启动即用。

因此部署方式有两大类：

| 方式 | 适合场景 | 需要的工具 |
| --- | --- | --- |
| 裸机单文件 | 本地试用、开发、无 Docker 的服务器 | Go + Node（构建时）；运行时零依赖 |
| Docker | 服务器快速部署、与 CI 联动 | Docker 20+ |

无论哪种方式，**数据（SQLite 数据库 + 图片文件）都存放在数据目录**（默认 `./data`），备份该目录即备份全站。

## 2. 环境准备

### 2.1 工具清单

| 组件 | 版本要求 | 用途 | 检查命令 |
| --- | --- | --- | --- |
| Git | 任意近期版本 | 获取源码 | `git --version` |
| Go | **1.22+** | 编译后端 | `go version` |
| Node.js + npm | **18+**（建议 20） | 构建前端（仅构建时需要） | `node -v && npm -v` |
| make | 可选 | 使用 Makefile 快捷命令 | `make -v` |
| Docker | 20+（可选） | 容器化部署 | `docker -v` |

> ⚠️ Go 与 Node 只在**构建阶段**需要。如果你拿到的是已编译好的单文件（如 `pixsy-windows-amd64.exe` 或 `pixsy-linux-amd64`），目标机器上什么都不用装。

### 2.2 安装步骤

**Windows：**

1. 安装 [Git for Windows](https://git-scm.com/download/win)（自带 Git Bash）。
2. 安装 [Go](https://go.dev/dl/)，安装后确认 `go version` 输出 ≥ 1.22。若 `go` 不在 PATH 中，可使用完整路径（如 `C:\Program Files\Go\bin\go.exe`）或将其加入环境变量。
3. 安装 [Node.js LTS](https://nodejs.org/)（自带 npm）。
4. （可选）安装 [Make for Windows](https://gnuwin32.sourceforge.net/packages/make.htm) 或直接使用下文的手动命令。

**Linux（Debian/Ubuntu 示例）：**

```bash
sudo apt update
sudo apt install -y git make curl
# Go：建议从官网装新版（apt 源里的版本可能过旧）
wget https://go.dev/dl/go1.23.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.23.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc && source ~/.bashrc

# Node.js 20（NodeSource 源）
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt install -y nodejs
```

**国内网络加速（可选）：**

```bash
# Go 模块代理
go env -w GOPROXY=https://goproxy.cn,direct
# npm 镜像
npm config set registry https://registry.npmmirror.com
```

### 2.3 获取源码

```bash
git clone <仓库地址> pixsy
cd pixsy
```

## 3. 从源码构建并运行（本地裸机方式）

构建顺序有讲究：**必须先构建前端**（产物在 `web/dist`），因为 `web/embed.go` 用 `go:embed all:dist` 内嵌前端产物——`web/dist` 不存在时任何 `go build` / `go test` 都会失败。

### 3.1 快捷方式（有 make 时）

```bash
make build    # = 构建前端 + 编译当前平台单文件 ./pixsy
./pixsy       # Windows 下为 .\pixsy.exe
```

其他目标：`make web`（只构建前端）、`make linux` / `make windows` / `make all`（交叉编译到 `bin/`）、`make test`、`make vet`、`make help`（查看全部）。

### 3.2 手动方式（无 make）

```bash
# 第 1 步：构建前端（首次需先安装依赖）
npm --prefix web install
npm --prefix web run build
# 产物输出到 web/dist（含 vue-tsc 类型检查，类型错误会中断构建）

# 第 2 步：编译后端（当前平台）
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o pixsy ./cmd/pixsy
# Windows PowerShell 写法：
#   $env:CGO_ENABLED=0; go build -trimpath -ldflags "-s -w" -o pixsy.exe ./cmd/pixsy

# 第 3 步：运行（默认监听 :8080，数据目录 ./data）
./pixsy
```

> 开发调试时也可以用 `go run ./cmd/pixsy` 直接跑，不必产出二进制；但仍需先完成第 1 步。

### 3.3 验证服务可用

浏览器打开 <http://localhost:8080>，或：

```bash
curl http://127.0.0.1:8080/api/v1/config
# 返回形如 {"data":{"site_name":"pixsy",...}} 即表示服务正常
```

### 3.4 交叉编译分发产物

`CGO_ENABLED=0` + 纯 Go SQLite 驱动意味着交叉编译毫无障碍，产物是静态单文件，可在任意目标平台直接运行：

```bash
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/pixsy-linux-amd64   ./cmd/pixsy
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o bin/pixsy-linux-arm64   ./cmd/pixsy
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/pixsy-windows-amd64.exe ./cmd/pixsy
```

## 4. 配置详解

优先级：**命令行 flag > `PIXSY_*` 环境变量 > 配置文件（pixsy.json）> 默认值**。

### 4.1 命令行 flag / 环境变量

| flag | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--config` | `PIXSY_CONFIG` | 空（自动找当前目录 `pixsy.json`） | 配置文件路径 |
| `--addr` | `PIXSY_ADDR` | `:8080` | HTTP 监听地址，如 `:8090`、`127.0.0.1:8080` |
| `--data` | `PIXSY_DATA` | `./data` | 数据目录（SQLite 与图片文件） |
| `--base-url` | `PIXSY_BASE_URL` | 空 | 图片外链前缀（如 CDN 域名）；空则按请求 Host 生成 |
| `--max-upload-mb` | `PIXSY_MAX_UPLOAD_MB` | `20` | 首次启动写入的默认单图上限（MB），之后在后台调整 |
| `--jwt-secret` | `PIXSY_JWT_SECRET` | 空 | JWT 签名密钥；留空自动生成并存入数据目录 |
| `--static-dir` | `PIXSY_STATIC_DIR` | 空 | 前端静态资源目录；留空用内嵌产物（开发热更新用） |
| `--open-browser` | `PIXSY_OPEN_BROWSER` | Windows `true`，其他 `false` | 启动后自动打开浏览器 |

示例——改端口跑在 8090、数据放别处：

```bash
./pixsy --addr :8090 --data D:\pixsy\data
```

### 4.2 配置文件 pixsy.json

不传 `--config` 时，程序自动加载**当前工作目录**下的 `pixsy.json`（不存在则忽略）。Windows 下双击 exe 时工作目录就是 exe 所在目录，所以把配置文件放在 exe 旁边即可。

最小配置（只改端口）：

```json
{ "port": 8090 }
```

完整示例（同 [pixsy.json.example](../pixsy.json.example)）：

```json
{
  "port": 8080,
  "data": "./data",
  "base_url": "",
  "jwt_secret": "",
  "static_dir": "",
  "open_browser": true,
  "settings": {
    "site_name": "pixsy",
    "allow_register": true,
    "allow_upload": true,
    "max_upload_mb": 20,
    "max_batch": 10
  }
}
```

`settings` 段的语义要特别注意：**写进配置文件的项，每次启动都会强制覆盖后台页面的修改**；没写的项不受影响（后台改过的值跨重启保留在数据库里）。配置文件里有拼错的键、或设置值非法（如 `max_upload_mb` 超范围）时，启动会直接失败并提示原因——这是有意设计，避免配置静默失效。

### 4.3 数据目录结构

```
data/
├── pixsy.db      # SQLite 数据库（用户、图片记录、站点设置）
├── jwt.secret    # JWT 签名密钥（未通过 --jwt-secret 显式指定时自动生成）
├── files/        # 原图，按 SHA-256 内容哈希命名
└── thumbs/       # 缩略图（<hash>.jpg，最长边 350px）
```

- 备份 = 拷贝整个数据目录；迁移 = 拷贝数据目录 + 可执行文件。
- 图片按内容哈希去重：相同内容只存一份；删除时若仍被其他记录引用则保留文件。

## 5. 首次使用：注册管理员与基本操作

1. 启动服务后打开 `http://localhost:8080`（Windows 下若开启了自动打开浏览器会自动弹出）。
2. 点击注册，创建第一个账号——**首个注册的用户自动成为管理员**。
3. 登录后进入「后台」：可修改站点名称、开关注册/上传、调整单图与批量上限（修改即时生效，无需重启）。
4. 首页即可多选上传图片，支持 URL / Markdown / HTML / BBCode 四种外链格式一键复制。

**API 冒烟示例**（也可用于脚本化验证）：

```bash
BASE=http://localhost:8080

# 注册（首个注册用户自动成为管理员）
curl -sX POST $BASE/api/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"your-password"}'

# 登录，取响应中的 data.token
TOKEN=$(curl -sX POST $BASE/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"your-password"}' | jq -r .data.token)

# 上传图片
curl -sX POST $BASE/api/v1/images -H "Authorization: Bearer $TOKEN" -F 'file=@cat.png'
```

> 所有管理接口挂在 `/api/v1/` 前缀下；图片直出为 `/i/{hash}.{ext}`、缩略图为 `/t/{hash}.jpg`。完整 API 列表见 [README.md 的 API 表](../README.md#api)。

## 6. Docker 部署

镜像为多阶段构建（前端 → 后端静态单文件 → alpine 运行时），最终约 25MB。

### 6.1 docker compose（推荐）

仓库根目录的 [docker-compose.yml](../docker-compose.yml) 已含数据卷与重启策略：

```bash
docker compose up -d --build
# 查看日志 / 停止
docker compose logs -f
docker compose down
```

数据持久化在宿主机 `./data` 目录，升级镜像（重新 `up -d --build`）不丢失。

### 6.2 手动 docker build / run

```bash
docker build -t pixsy .
docker run -d --name pixsy --restart unless-stopped \
  -p 8080:8080 -v ./data:/app/data pixsy
```

### 6.3 容器内配置

通过 `PIXSY_*` 环境变量传入，例如：

```bash
docker run -d -p 8080:8080 -v ./data:/app/data \
  -e PIXSY_BASE_URL=https://img.example.com \
  -e PIXSY_JWT_SECRET=change-me-to-a-long-random-string \
  pixsy
```

生产环境建议显式指定 `PIXSY_JWT_SECRET`（勿提交真实值到仓库）；仅本机使用可留空（自动生成）。

冒烟验证（容器内探测，不依赖宿主机 loopback）：

```bash
docker exec pixsy wget -qO- "http://127.0.0.1:8080/api/v1/config"
```

## 7. 生产环境部署

### 7.1 Linux（systemd，推荐裸机方式）

1. 在构建机交叉编译（或在服务器上直接构建），上传产物：

```bash
sudo useradd -r -s /usr/sbin/nologin pixsy
sudo mkdir -p /opt/pixsy/data
sudo install -m 0755 bin/pixsy-linux-amd64 /opt/pixsy/pixsy
sudo chown -R pixsy:pixsy /opt/pixsy
```

2. 安装并启动服务（unit 文件见 [deploy/pixsy.service](../deploy/pixsy.service)，已含低权限用户与基本加固）：

```bash
sudo cp deploy/pixsy.service /etc/systemd/system/pixsy.service
sudo systemctl daemon-reload
sudo systemctl enable --now pixsy
sudo systemctl status pixsy   # 确认 active (running)
```

默认监听 `:8080`、数据目录 `/opt/pixsy/data`。修改 `ExecStart` 参数或环境变量后需重新 `sudo systemctl daemon-reload`。

### 7.2 Windows

`make windows` 产出 `bin/pixsy-windows-amd64.exe`（约 12MB 单文件），拷贝到目标机器任意目录即可运行：

```powershell
.\pixsy-windows-amd64.exe --addr :8080 --data D:\pixsy\data
# 或把 pixsy.json 放在 exe 同目录，直接双击运行
```

要点：

- **防火墙**：首次运行会弹出提示，勾选允许「专用网络」；仅本机使用可直接取消。
- **开机自启**（任务计划程序，以 SYSTEM 身份后台运行）：

```powershell
schtasks /Create /TN pixsy /SC ONSTART /RU SYSTEM /TR "D:\pixsy\pixsy.exe --addr :8080 --data D:\pixsy\data"
```

### 7.3 反向代理与 HTTPS

pixsy 自身只提供 HTTP。公网部署时建议前面挂 Nginx / Caddy 做 TLS 终结：

```nginx
server {
    listen 443 ssl;
    server_name img.example.com;
    client_max_body_size 25m;   # 需大于单图上传上限
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        # 必须传递协议，否则 pixsy 在 HTTPS 站点上生成的图片外链是 http://，
        # 浏览器会按混合内容拦截。
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

#### trusted_proxies：声明哪些代理可信

pixsy 只信任来自**可信代理网段**的 `X-Forwarded-*` 头（用于识别真实客户端 IP 和
HTTPS 协议）。这是安全设计：不配置时伪造 `X-Forwarded-For` 无法绕过登录限速，
HTTPS 反代后的外链也不会被恶意头篡改。

反代部署需要在配置里声明代理网段，否则：

- HTTPS 下生成的图片外链会退化成 `http://`（混合内容拦截）；
- 所有用户共享代理 IP 的一个限流桶。

在 `pixsy.json` 顶层加一行（或用环境变量 `PIXSY_TRUSTED_PROXIES`、命令行
`--trusted-proxies`，优先级 flag > 环境变量 > 配置文件）：

```json
{
  "port": 8080,
  "trusted_proxies": "127.0.0.1/32",
  "settings": { }
}
```

| 部署形态 | 填什么 |
|---|---|
| Nginx 与 pixsy 同机（非容器） | `127.0.0.1/32` |
| Docker 部署，Nginx 在另一容器 | `172.16.0.0/12`（Docker 默认网段） |
| 独立 Nginx 服务器（如 10.0.0.5） | `10.0.0.5/32` 或所在内网段 |
| CDN / 云负载均衡回源 | 固定回源 IP 段；没有固定段则不配 |

注意：

- **不要配成 `0.0.0.0/0`**——等于把信任判断关掉，伪造 XFF 又能绕限速；
- **直连部署（无反代）保持不配**，即最安全形态；
- 配好后验证：HTTPS 下登录一次，上传图片确认复制的外链是 `https://` 开头；
  连续 5 次错误密码第 6 次应提示「操作过于频繁」。

若图片走独立 CDN 域名，设置 `--base-url`（或 `PIXSY_BASE_URL`）让外链统一生成 CDN 前缀。

### 7.4 部署后冒烟清单

```bash
curl http://127.0.0.1:8080/api/v1/config                   # 1. 配置接口返回 site_name
# 2. 浏览器打开首页，注册 → 登录 → 上传一张图
# 3. 复制外链，新开无痕窗口确认图片可访问
# 4. 重启服务，确认已上传图片仍在（数据目录持久化正常）
```

## 8. 开发环境搭建

### 8.1 前后端并行开发（热更新）

前端用 Vite 开发服务器，后端用 `go run`，两者并行运行：

```bash
# 终端 1：后端（先确保 web/dist 已构建过，go:embed 依赖它）
npm --prefix web install
npm --prefix web run build
go run ./cmd/pixsy

# 终端 2：前端开发服务器（http://localhost:5173）
npm --prefix web run dev
```

Vite 已把 `/api`、`/i`、`/t` 代理到 `127.0.0.1:8080`（见 `web/vite.config.ts`），因此**开发时访问的是 5173 端口**，前端改动即时热更新，接口请求自动转发给后端。

> 提示：`go run` 默认占 `:8080`。若端口被占用，可用 `go run ./cmd/pixsy --addr :8090`，但注意 Vite 代理目标写死为 8080，此时需临时改 `vite.config.ts` 的 proxy 目标。

### 8.2 后端单独调试 + 前端产物热更新

若只想改前端样式/组件、不想跑 Vite：以 `PIXSY_STATIC_DIR=web/dist` 启动后端，静态资源改从磁盘读取；重新 `npm --prefix web run build` 后刷新页面即可生效，无需重编二进制。

⚠️ 反之，**改了后端 Go 代码必须重新编译**（`go build` / `go run` 重跑）——单二进制内嵌前端也意味着：分发给别人的二进制要先重新构建前端再编译。

### 8.3 质量检查与测试

```bash
go test ./...                      # 后端测试
go vet ./...                       # 静态检查
npm --prefix web run typecheck     # 前端类型检查（vue-tsc）
```

### 8.4 目录结构速查

```
pixsy/
├── cmd/pixsy/        # 程序入口
├── internal/
│   ├── api/          # REST API 处理器
│   ├── auth/         # JWT 认证、中间件、登录限速
│   ├── config/       # 启动配置（flag > 环境变量 > 配置文件 > 默认值）
│   ├── httpx/        # 统一 JSON 响应 / 错误包络
│   ├── model/        # SQLite 数据访问
│   ├── server/       # 路由装配、静态资源与图片直出
│   ├── setting/      # 站点设置（settings 表 + 内存缓存）
│   └── storage/      # 文件与缩略图存储、内容寻址去重
├── web/              # Vue 3 + TypeScript 前端（dist/ 经 go:embed 内嵌）
│   └── vite.config.ts # 开发代理 /api、/i、/t → 127.0.0.1:8080
├── deploy/           # systemd unit 文件
├── scripts/          # 辅助脚本（如 Lsky Pro CLI 迁移）
├── Dockerfile        # 多阶段构建
├── docker-compose.yml
└── docs/             # 设计文档与本文
```

## 9. CI/CD 说明

仓库自带 Gitea Actions 工作流（[.gitea/workflows/ci.yml](../.gitea/workflows/ci.yml)），语法兼容 GitHub Actions：

- **test job**（push / PR 触发）：前端构建（含 vue-tsc 类型检查）→ `go vet` → `go test` → 发布参数构建二进制。运行标签 `go-vue`。
- **deploy job**（仅 main/master push）：构建 Docker 镜像 → 重建容器（宿主机映射端口 `HOST_PORT=62006`，数据固定挂 `/opt/pixsy/data`）→ 容器内冒烟 `GET /api/v1/config` → 清理悬空镜像。

本地搭建同类 runner 或调整部署目标时，注意 workflow 文件内的注释：数据卷固定挂宿主机绝对路径（勿用 runner 临时 workspace 下的 `./data`），容器模式 runner 需挂载 docker socket。详见 [docs/ci-runner-ubuntu.md](ci-runner-ubuntu.md)。

## 10. 常见问题排查

**Q1：`go build` / `go test` 报 `embed` 相关错误（找不到 web/dist）。**
`go:embed` 要求 `web/dist` 存在。先执行 `npm --prefix web install && npm --prefix web run build`，再执行任何 go 命令。CI 里也是先构建前端再跑 go。

**Q2：端口 8080 被占用（`bind: address already in use` / Windows 下被其他进程占用）。**
换端口启动：`./pixsy --addr :8090`，或写 `pixsy.json` 的 `"port": 8090`。排查占用者：Linux `ss -ltnp | grep 8080`；Windows `netstat -ano | findstr :8080`。

**Q3：前端页面打开了，但上传 / 登录报网络错误。**
确认后端进程还活着、监听端口与访问端口一致；用 Vite 开发模式时确认后端跑在 8080（代理目标）。

**Q4：Windows 双击 exe 一闪而过。**
配置文件非法时启动会直接失败退出（这是设计行为，避免配置静默失效）。在终端里手动运行 exe 看报错信息，通常是 `pixsy.json` 键名拼错或设置值超范围。

**Q5：Windows 上改了前端/后端代码但页面没变。**
改后端代码必须重编二进制；改前端代码后需重新 `npm run build` 重新编译（内嵌产物）或用 `--static-dir` / Vite 开发服务器（见 §8.2）。

**Q6：Docker 容器重启后图片丢失。**
确认启动时挂了数据卷（`-v ./data:/app/data` 或 compose 里的 volumes）。注意 CI 类临时 workspace 下的 `./data` 会被清理，生产环境用绝对路径如 `/opt/pixsy/data`。

**Q7：上传大图失败。**
检查后台「单图上限」（默认 20MB）与批量上限；走反向代理时还需调代理的请求体限制（Nginx 的 `client_max_body_size`）。

**Q8：忘记管理员密码 / 想重置站点。**
删除数据目录（或其中的 `pixsy.db`）会丢失全部数据并回到初始状态——重新注册的第一个用户即管理员。只想换 JWT 密钥则删除 `data/jwt.secret` 后重启（所有登录态失效）。

---

*本文基于仓库当前代码编写；如与实际行为不符，以代码与 [README.md](../README.md) 为准，欢迎提 PR 修正。*
