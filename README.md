# pixsy

> 自托管的轻量图床：单二进制、零外部依赖、开箱即用。

![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)
![Vue](https://img.shields.io/badge/Vue-3-4FC08D?logo=vuedotjs&logoColor=white)
![SQLite](https://img.shields.io/badge/SQLite-embedded-003B57?logo=sqlite&logoColor=white)

简体中文 | [English](README.en.md)

## 简介

pixsy 是一个用 Go + Vue 3 编写的自托管图床服务。前端构建产物通过 `go:embed` 直接内嵌进后端二进制，配合纯 Go 实现的 SQLite 驱动（无 CGO），最终交付物只有一个可执行文件——拷贝到服务器上即可运行，不需要安装数据库，也不依赖任何外部服务。

## 功能特性

- **单二进制部署** — 前端经 `go:embed` 内嵌，一个可执行文件即整站
- **Docker 一键部署** — 多阶段构建，最终镜像基于 alpine 约 25MB，`docker compose up -d --build` 即起
- **零外部依赖** — SQLite（纯 Go 驱动，无 CGO）+ 本地磁盘存储，交叉编译友好
- **内容寻址去重** — 图片按 SHA-256 哈希落盘，相同内容只存一份；删除时若仍被其他记录引用则保留文件
- **自动缩略图** — 上传即生成最长边 350px 的 JPEG 缩略图，列表页加载更快
- **多格式支持** — JPEG / PNG / GIF / WebP
- **多种外链格式** — URL / Markdown / HTML / BBCode 一键复制
- **用户与权限** — JWT 认证；首个注册用户自动成为管理员；登录接口限速防爆破
- **在线管理后台** — 站点名称、开放注册、开放上传、单图/批量上限，修改即时生效、无需重启
- **原图直出** — 支持 HTTP Range 请求，浏览器与下载工具行为正常

## 环境要求

| 组件 | 版本 | 用途 |
| --- | --- | --- |
| Go | 1.22+ | 编译后端 |
| Node.js + npm | 18+ | 构建前端（仅构建时需要） |
| Docker | 20+（可选） | 容器化部署（构建与运行） |

## 快速开始

```bash
# 1. 构建前端（产物输出到 web/dist，供 go:embed 内嵌）
npm --prefix web install
npm --prefix web run build

# 2. 编译后端
go build -o pixsy ./cmd/pixsy

# 3. 运行（默认监听 :8080）
./pixsy
```

打开 <http://localhost:8080> 即可使用。**首个注册的用户自动成为管理员**，登录后进入后台即可调整站点设置。

数据（SQLite 数据库与图片文件）默认存放在工作目录下的 `data/`，可通过 `--data` 修改。

## 部署

### 交叉编译

仓库根目录提供 Makefile 统一构建入口（目标会自动先构建前端产物）：

```bash
make linux    # 交叉编译 linux-amd64 / linux-arm64 静态单文件到 bin/
make windows  # 交叉编译 windows-amd64 到 bin/
make all      # 全部分发产物
```

产物为 `CGO_ENABLED=0` 的静态单文件（SQLite 使用纯 Go 驱动，无 libc 依赖）：Linux 产物可直接在任意发行版上运行，Windows 产物见下文。未安装 make 时可手动执行等价命令：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/pixsy-linux-amd64 ./cmd/pixsy
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o bin/pixsy-linux-arm64 ./cmd/pixsy
```

### Docker

镜像采用多阶段构建（前端 → 后端静态单文件 → alpine 运行时），最终镜像约 25MB：

```bash
# 方式一：docker compose（推荐，已含数据卷与重启策略）
docker compose up -d --build

# 方式二：docker build + docker run
docker build -t pixsy .
docker run -d -p 8080:8080 -v ./data:/app/data pixsy
```

数据（SQLite 与图片文件）持久化在宿主机 `./data` 目录，升级镜像不丢失。需要自定义监听端口、外链前缀或 JWT 密钥时，通过 `PIXSY_*` 环境变量传入（见 `docker-compose.yml` 内注释与下方配置表）。

### Linux（systemd）

1. 上传二进制，创建专用运行用户与数据目录：

```bash
sudo useradd -r -s /usr/sbin/nologin pixsy
sudo mkdir -p /opt/pixsy/data
sudo install -m 0755 bin/pixsy-linux-amd64 /opt/pixsy/pixsy
sudo chown -R pixsy:pixsy /opt/pixsy
```

2. 安装并启动服务（unit 文件见 `deploy/pixsy.service`）：

```bash
sudo cp deploy/pixsy.service /etc/systemd/system/pixsy.service
sudo systemctl daemon-reload
sudo systemctl enable --now pixsy
sudo systemctl status pixsy   # 确认 active (running)
```

服务默认监听 `:8080`、数据目录 `/opt/pixsy/data`；修改 `ExecStart` 参数或环境变量后需重新执行 `sudo systemctl daemon-reload`。

### Windows

`make windows` 产出 `bin/pixsy-windows-amd64.exe`（约 12MB 的单文件，前端已内嵌），拷贝到目标机器任意目录即可运行，无需安装任何依赖：

```powershell
# 前台运行（默认监听 :8080，数据目录为工作目录下的 data\）
.\pixsy-windows-amd64.exe

# 自定义端口与数据目录
.\pixsy-windows-amd64.exe --addr :8080 --data D:\pixsy\data

# 也可以把 pixsy.json 放在 exe 同目录（改端口、后台设置都写在里面），直接双击运行
# 参考 pixsy.json.example
```

Windows 上启动后会自动用默认浏览器打开页面（`--open-browser=false` 或配置文件 `"open_browser": false` 可关闭）。

未安装 make 时可在任意装有 Go 的机器上手动交叉编译：

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/pixsy-windows-amd64.exe ./cmd/pixsy
```

使用要点：

- **防火墙**：首次运行 Windows 会弹出防火墙提示，勾选允许「专用网络」访问；仅本机使用可直接取消。
- **开机自启**：程序本身是前台进程，可用任务计划程序注册开机启动（以 SYSTEM 身份在后台运行）：

```powershell
schtasks /Create /TN pixsy /SC ONSTART /RU SYSTEM /TR "D:\pixsy\pixsy-windows-amd64.exe --addr :8080 --data D:\pixsy\data"
```

- **数据目录**：SQLite 数据库、`jwt.secret` 与图片文件都在 `--data` 指向的目录内，备份该目录即备份全站；卸载只需删除可执行文件与数据目录。

### 部署冒烟

服务启动后执行 `curl http://127.0.0.1:8080/api/v1/config`，返回站点配置 JSON 即表示服务可用（Linux 原生对应设计文档 TC-DEPLOY-001，Windows 对应 TC-DEPLOY-002，Docker 部署对应 TC-DEPLOY-003）。

## 配置

优先级：**命令行 flag > `PIXSY_*` 环境变量 > 配置文件 > 默认值**。

| 命令行 flag | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--config` | `PIXSY_CONFIG` | 空（自动查找当前目录 `pixsy.json`） | 配置文件路径（JSON） |
| `--addr` | `PIXSY_ADDR` | `:8080` | HTTP 监听地址 |
| `--data` | `PIXSY_DATA` | `./data` | 数据目录（SQLite 与图片文件） |
| `--base-url` | `PIXSY_BASE_URL` | 空 | 图片外链前缀（如 CDN 域名）；空则按请求 Host 生成 |
| `--max-upload-mb` | `PIXSY_MAX_UPLOAD_MB` | `20` | 首次启动写入设置的默认单图上限（MB），运行时可在后台调整 |
| `--jwt-secret` | `PIXSY_JWT_SECRET` | 空 | JWT 签名密钥；留空则自动生成并存入数据目录 |
| `--static-dir` | `PIXSY_STATIC_DIR` | 空 | 前端静态资源目录；留空使用内嵌产物（开发热更新用） |
| `--open-browser` | `PIXSY_OPEN_BROWSER` | Windows `true`，其他平台 `false` | 启动后自动用默认浏览器打开页面；`--open-browser=false` 可关闭 |

### 配置文件（pixsy.json）

不传 `--config` 时，程序会自动加载**当前目录**下的 `pixsy.json`（不存在则忽略）。Windows 下双击 exe 或把 exe 加到任务计划时，工作目录就是 exe 所在目录，因此把配置文件放在 exe 旁边即可，无需再写启动参数。参考 `pixsy.json.example`。

只改端口的最小配置：

```json
{ "port": 8090 }
```

完整示例：

```json
{
  "port": 8080,
  "data": "./data",
  "base_url": "",
  "jwt_secret": "",
  "static_dir": "",
  "settings": {
    "site_name": "pixsy",
    "allow_register": true,
    "allow_upload": true,
    "max_upload_mb": 20,
    "max_batch": 10
  }
}
```

| 键 | 说明 |
| --- | --- |
| `port` | 监听端口（`1~65535`），等价于 `addr: ":<port>"`；与 `addr` 二选一 |
| `addr` | 完整监听地址，如 `":8080"`、`"127.0.0.1:8080"` |
| `data` | 数据目录 |
| `base_url` | 图片外链前缀，可填 CDN 域名 |
| `jwt_secret` | JWT 签名密钥；留空自动生成 |
| `static_dir` | 前端静态资源目录；留空使用内嵌产物 |
| `open_browser` | 启动后自动打开浏览器；Windows 默认 `true`，其他平台默认 `false` |
| `settings.*` | 后台页面的站点设置，含义与默认值见下方「站点设置」 |

`settings` 段的语义：**写进配置文件的项，每次启动都会强制覆盖后台页面的修改**；没写的项不受影响（后台改过的值跨重启保留）。设置值非法（如 `max_upload_mb` 超出范围）会让启动直接失败并提示原因，避免配置静默失效。配置文件里拼错的键同样会报错。

### 数据目录结构

```
data/
├── pixsy.db      # SQLite 数据库
├── jwt.secret    # JWT 签名密钥（未通过 --jwt-secret 显式指定时自动生成）
├── files/        # 原图，按内容哈希命名
└── thumbs/       # 缩略图（<hash>.jpg）
```

### 站点设置（管理员后台）

运行期设置存于数据库，修改即时生效：

| 设置项 | 默认值 | 说明 |
| --- | --- | --- |
| `site_name` | `pixsy` | 站点名称（1~64 字符） |
| `allow_register` | 开 | 是否开放注册 |
| `allow_upload` | 开 | 是否开放上传 |
| `max_upload_mb` | `20` | 单图大小上限（1~1024 MB） |
| `max_batch` | `10` | 单批上传文件数上限（1~50） |

## 使用

### Web 界面

- **首页**：多选上传（逐文件进度条）、「我的图片 / 全部图片」（后者仅管理员）分页浏览、一键复制外链、删除
- **后台**（管理员）：站点名称与各项开关、上限调整

### API

所有接口返回统一包络：成功为 `{"data": ...}`，失败为 `{"error":{"code":"...","message":"..."}}`；鉴权接口使用 `Authorization: Bearer <token>` 请求头。

| 方法 | 路径 | 说明 | 鉴权 |
| --- | --- | --- | --- |
| GET | `/api/v1/config` | 公开站点配置（站点名、开关、上限） | - |
| POST | `/api/v1/auth/register` | 注册（首个用户成为管理员；可在后台关闭） | - |
| POST | `/api/v1/auth/login` | 登录，返回 JWT | - |
| GET | `/api/v1/auth/me` | 当前登录用户 | Bearer |
| POST | `/api/v1/images` | 上传图片，multipart 字段 `file`（可重复以批量上传） | Bearer |
| GET | `/api/v1/images` | 图片列表，支持 `page` / `page_size` / `scope=mine\|all`（`all` 仅管理员） | Bearer |
| DELETE | `/api/v1/images/{id}` | 删除图片 | Bearer |
| GET | `/api/v1/admin/settings` | 查看站点设置 | 管理员 |
| PUT | `/api/v1/admin/settings` | 修改站点设置 | 管理员 |
| POST | `/api/v1/admin/migrate/lsky` | 启动 Lsky Pro 迁移(JSON:`base_url`/`email`/`password`) | 管理员 |
| GET | `/api/v1/admin/migrate/lsky` | 查看迁移进度(轮询) | 管理员 |
| POST | `/api/v1/admin/migrate/lsky/cancel` | 取消迁移 | 管理员 |
| GET | `/api/v1/admin/migrate/lsky/csv` | 下载迁移映射 CSV(旧URL→新URL) | 管理员 |
| GET | `/i/{hash}.{ext}` | 原图直出（支持 Range） | - |
| GET | `/t/{hash}.jpg` | 缩略图直出 | - |

#### curl 示例

```bash
BASE=http://localhost:8080

# 注册（首个注册用户自动成为管理员）
curl -sX POST $BASE/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"change-me-123"}'

# 登录获取 token，响应形如 {"data":{"token":"…","user":{…}}}
curl -sX POST $BASE/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"change-me-123"}'

# 上传图片
curl -sX POST $BASE/api/v1/images \
  -H "Authorization: Bearer $TOKEN" \
  -F 'file=@cat.png'
# => {"data":{"images":[{"id":1,"url":"…/i/<hash>.png","markdown":"…","html":"…","bbcode":"…", …}],"total":1}}

# 分页查看自己的图片（管理员可追加 &scope=all 查看全部）
curl -s "$BASE/api/v1/images?page=1&page_size=20" -H "Authorization: Bearer $TOKEN"

# 删除图片
curl -sX DELETE $BASE/api/v1/images/1 -H "Authorization: Bearer $TOKEN"
```

## 开发

```bash
# 后端测试
go test ./...

# 前端开发服务器（http://localhost:5173，/api、/i、/t 已代理到 127.0.0.1:8080）
npm --prefix web install
npm --prefix web run dev

# 另开一个终端运行后端
go run ./cmd/pixsy
```

前端热更新：以 `PIXSY_STATIC_DIR=web/dist` 启动后端，静态资源改从磁盘读取，配合 Vite 开发服务器即可前后端并行开发。

前端类型检查：`npm --prefix web run typecheck`。

### 目录结构

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
├── web/              # Vue 3 + TypeScript 前端（dist/ 构建产物经 go:embed 内嵌）
├── Dockerfile        # 多阶段构建：前端 → 后端静态单文件 → alpine 运行时
├── docker-compose.yml # 容器编排示例（数据卷挂载 ./data）
└── docs/             # 设计文档
```

## 文档

- [设计文档](docs/design.md) — 架构、数据模型与接口契约
- [设计评审对齐清单](docs/review-alignment.md) — 设计与实现的逐项对齐记录
