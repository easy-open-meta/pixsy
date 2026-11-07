# pixsy 轻量图库系统 · 设计文档

| | |
|---|---|
| 版本 | v1.0 |
| 日期 | 2026-11-02 |
| 状态 | 已评审，作为 P1 前后端开发任务的实现依据 |
| 关联任务 | P0-评估并设计轻量图库文档、P1-前后端开发、P2-评审对齐 |

---

## 1. 项目概述

pixsy 是一个**自托管轻量图库（图床）系统**。核心场景：个人/小团队部署在低配置服务器上，把自己或他人网站的图片托管在 pixsy，通过外链 URL 引用。

设计目标（按优先级）：

1. **外链加载快**：即使服务器配置低、网络拥塞，第三方网站引用的图片 URL 也应尽快完成加载。
2. **系统轻量**：单二进制 + SQLite，资源占用极低，部署与运维极简。
3. **简洁现代的 UI**：直观、无冗余元素，移动端可用。

非目标（本期不做）：对象存储直传、外链统计、图片审核、多级目录/相册管理、多语言。

## 2. 需求分析与方案映射

| 编号 | 需求 | 方案要点 | 详见 |
|---|---|---|---|
| R1 | 低配服务器+网络拥塞下，第三方引用图片 URL 快速优先加载 | 内容寻址文件名 + `Cache-Control: immutable` 一年长缓存 + ETag/304；URL 无重定向直出；可配 CDN 外链域名；缩略图供预览；Go 静态服务（sendfile）；反代启用 HTTP/2 | §4.5、§6.4 |
| R2 | 轻量体积、简洁直观现代 UI | Go 单二进制（常驻内存 ~20MB）、前端自写 CSS 无组件库（gzip < 200KB）、设计令牌统一风格 | §2.3、§5.3 |
| R3 | 第一个注册用户自动成为超级管理员 | 注册事务内判断用户数为 0 → `role=admin`；无用户时即使关闭注册也放行首注（引导兜底） | §4.2 |
| R4 | 支持 jpg/png/gif 等通用格式，限制最大体积 | 白名单 jpg/png/gif/webp；魔数校验（非仅扩展名）；`max_upload_mb` 可配置，双重限制（预检 + 流式硬限） | §4.4 |
| R5 | 超级管理员控制系统注册/上传等核心开关 | `settings` 表：`allow_register`、`allow_upload`、`max_upload_mb`；管理 API + 管理页，即时生效 | §4.7 |
| R6 | Linux / Windows / Docker 多端部署 | 纯 Go（免 CGO）交叉编译单文件；多阶段 Dockerfile（镜像 ~25MB）；docker-compose / systemd 示例 | §6 |
| R7 | 上传后自动返回图片链接（网页与 API 一致） | 同一上传接口；响应统一含 `url`/`thumb_url`/`markdown`/`html`/`bbcode` | §4.3 |
| R8 | 上传、登录等核心操作暴露公开 API | 全部能力 REST 化并文档化，附 curl 示例；前后端同源共用 | §4.3 |

### 2.1 关键技术决策

- **后端语言：Go（1.22+）**。理由：编译为单个静态二进制（Windows/Linux 交叉编译免 CGO），常驻内存 ~20MB，无运行时依赖，完美匹配"低配置服务器 + 多端部署 + 轻量"。
- **Web 框架：不引入，使用标准库 `net/http`**（Go 1.22 的 ServeMux 支持 `METHOD /path/{param}`）。减少依赖与体积。
- **数据库：SQLite**，驱动用 `modernc.org/sqlite`（纯 Go、免 CGO，Windows 交叉编译无需 gcc）。图库是"读多写少"场景，单文件 SQLite 足够。
- **前端：Vue 3 + TypeScript + Vite + Pinia**。不引入 UI 组件库，自写样式。**构建产物通过 Go `embed` 打进二进制**，最终交付单文件，部署时无需 Node。
- **图片处理**：`golang.org/x/crypto`（bcrypt）、`golang-jwt/jwt/v5`、`github.com/disintegration/imaging`（缩略图）、`golang.org/x/image`（webp/webp 解码配置）。缩略图统一输出 **JPEG**（纯 Go 编码器稳定；WebP 编码器依赖 CGO，会破坏 Windows 交叉编译，列为后续可选优化）。

### 2.2 总体架构

```
 浏览器 / 第三方网站
        │  <img src="https://img.example.com/i/<sha256>.jpg">
        ▼
 [CDN（可选，强烈建议生产接入）]        ← 命中 immutable 长缓存，源站零压力
        ▼ 回源
 [Caddy / Nginx（可选）: TLS + HTTP/2]  ← 拥塞网络下多路复用降低延迟
        ▼
 pixsy 单二进制（Go）
   ├─ 静态资源（embed 的前端 dist）：/、/login、/images…
   ├─ 公开 API：/api/v1/*
   └─ 图片直出：/i/{hash}.{ext}、/t/{hash}.jpg   ← 无重定向、长缓存、304
        ▼
 SQLite（data/pixsy.db） + 本地磁盘（data/files/）
```

### 2.3 仓库目录规划

```
pixsy/
├── cmd/pixsy/main.go          # 入口：配置加载、路由装配、启动
├── internal/
│   ├── api/                   # HTTP handlers（auth/images/admin/config）+ 路由注册
│   ├── auth/                  # JWT 签发校验、bcrypt、认证中间件
│   ├── config/                # 环境变量/flag 配置
│   ├── model/                 # 数据模型与 SQLite 迁移
│   ├── setting/               # 系统设置（KV，内存缓存）
│   ├── storage/               # 哈希、魔数校验、落盘、缩略图、尺寸解析
│   └── server/                # HTTP server、embed 静态资源、图片响应（缓存头）
├── web/                       # Vue3 前端源码（构建产物 dist 被 embed）
├── docs/design.md             # 本文档
├── Dockerfile / docker-compose.yml
└── AGENT.md
```

数据目录（运行时生成，Docker 挂载 volume）：

```
data/
├── pixsy.db                                   # SQLite
├── files/ab/cd/<sha256-hex>.jpg|png|gif|webp  # 原图，按 hash 前两位分目录
└── thumbs/ab/cd/<sha256-hex>.jpg              # 缩略图（最长边 350px）
```

## 3. 后端设计

### 3.1 数据模型

```sql
CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT    NOT NULL UNIQUE,            -- 3~32 位，字母数字下划线
  password_hash TEXT    NOT NULL,                   -- bcrypt, cost=10
  role          TEXT    NOT NULL DEFAULT 'user' CHECK (role IN ('admin','user')),
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE images (
  id         INTEGER  PRIMARY KEY AUTOINCREMENT,
  hash       TEXT     NOT NULL,      -- 文件 sha256 hex（64 字符），内容寻址
  ext        TEXT     NOT NULL,      -- 按魔数判定：jpg/png/gif/webp
  filename   TEXT     NOT NULL,      -- 原始文件名，仅展示用
  mime       TEXT     NOT NULL,      -- image/jpeg 等
  size       INTEGER  NOT NULL,      -- 字节
  width      INTEGER  NOT NULL DEFAULT 0,
  height     INTEGER  NOT NULL DEFAULT 0,
  user_id    INTEGER  NOT NULL REFERENCES users(id),
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_images_user ON images(user_id, id DESC);
CREATE INDEX idx_images_hash ON images(hash);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,   -- site_name / allow_register / allow_upload / max_upload_mb / max_batch
  value TEXT NOT NULL
);
```

内容寻址说明：同一文件（同 hash）被多个用户上传时**只存一份磁盘文件，各建一条 `images` 记录**；删除某条记录时，仅当该 hash 不再被任何记录引用才删除磁盘文件。副作用是"秒传"：重复上传同一文件立即返回。

### 3.2 认证与首个管理员（R3）

- 密码 bcrypt（cost 10）存储；登录成功签发 JWT（HS256，payload 含 `uid`、`role`，有效期 7 天）。
- JWT 密钥：默认首次启动自动生成并写入 `data/jwt.secret`（文件权限 600）；多实例部署用 `PIXSY_JWT_SECRET` 显式指定。
- **首个管理员**：注册处理在同一事务内执行 `SELECT COUNT(*) FROM users`，为 0 则该用户 `role='admin'`。**引导兜底**：用户数为 0 时，即使 `allow_register=false` 也放行该次注册（避免误关开关后系统永远无法产生管理员）。
- 中间件：`RequireAuth`（校验 Bearer token）、`RequireAdmin`（校验 role）。
- 防爆破：登录/注册接口按 IP 限速 5 次/分钟（内存令牌桶），超限返回 429。

### 3.3 API 设计（R7、R8）

统一约定：
- 路径前缀 `/api/v1`；认证用 `Authorization: Bearer <token>`。
- 成功：HTTP 2xx，响应 `{"data": ...}`；失败：HTTP 4xx/5xx，响应 `{"error":{"code":"...","message":"..."}}`。
- 错误码：400 参数错误、401 未认证、403 无权限/被开关禁止、404 不存在、409 冲突（用户名重复）、413 体积超限、429 限速、500 内部错误。

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| GET | `/api/v1/config` | 公开 | 站点公开配置（前端首屏用） |
| POST | `/api/v1/auth/register` | 公开* | 注册；首个用户自动成为 admin；受 `allow_register` 控制 |
| POST | `/api/v1/auth/login` | 公开 | 登录，返回 token 与用户信息 |
| GET | `/api/v1/auth/me` | 登录 | 当前用户信息 |
| POST | `/api/v1/images` | 登录 | 上传图片（multipart，字段名 `file`，可多文件），返回链接 |
| GET | `/api/v1/images` | 登录 | 分页列出**当前用户**图片；admin 传 `?scope=all` 可看全部 |
| DELETE | `/api/v1/images/{id}` | 登录 | 删除；仅 owner 或 admin |
| GET | `/api/v1/admin/settings` | admin | 读取系统设置 |
| PUT | `/api/v1/admin/settings` | admin | 修改系统设置，即时生效 |

**图片访问（公开、直出）**：

| 路径 | 说明 |
|---|---|
| `GET /i/{hash}.{ext}` | 原图，无重定向 200 直出 |
| `GET /t/{hash}.jpg` | 缩略图（最长边 350px），供网页预览与第三方小图场景 |

公开配置响应示例：

```json
GET /api/v1/config
{"data":{"site_name":"pixsy","allow_register":true,"allow_upload":true,"max_upload_mb":20,"max_batch":10}}
```

核心流程示例（也是 R8 的 API 使用文档）：

```bash
# 1. 注册（首个用户自动成为管理员）
curl -X POST https://demo.example.com/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Str0ng-Pass"}'
# => {"data":{"user":{"id":1,"username":"admin","role":"admin"}}}

# 2. 登录
curl -X POST https://demo.example.com/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Str0ng-Pass"}'
# => {"data":{"token":"eyJhbGciOi...","user":{"id":1,"username":"admin","role":"admin"}}}

# 3. API 上传（与网页上传共用同一接口）
curl -X POST https://demo.example.com/api/v1/images \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@cat.jpg"
```

上传响应（R7：自动返回链接，含常用引用格式）：

```json
{"data":{"images":[{
  "id": 42,
  "filename": "cat.jpg",
  "url": "https://demo.example.com/i/2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881.jpg",
  "thumb_url": "https://demo.example.com/t/2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881.jpg",
  "markdown": "![cat.jpg](https://demo.example.com/i/2d71...4881.jpg)",
  "html": "<img src=\"https://demo.example.com/i/2d71...4881.jpg\" alt=\"cat.jpg\" />",
  "bbcode": "[img]https://demo.example.com/i/2d71...4881.jpg[/img]",
  "size": 204800, "width": 1920, "height": 1080
}]}}
```

说明：URL 域名取 `PIXSY_BASE_URL`；未配置时按请求的 `Host` 动态生成（便于本地试用）。

### 3.4 上传与存储流程（R4）

单文件处理流程：

1. **预检**：请求 `Content-Length` 超过 `max_upload_mb` 直接 413；未登录 401；`allow_upload=false` 时 403（admin 不受限）。
2. **流式硬限**：`io.LimitReader` 限制读取字节数 = 上限 + 1，超出即终止连接（防止伪造 Content-Length）。
3. **魔数校验**：读前 32 字节判定真实格式，白名单外返回 400：
   - JPEG `FF D8 FF`、PNG `89 50 4E 47`、GIF `GIF87a/GIF89a`、WebP `RIFF....WEBP`
   - 扩展名仅用于展示，**不参与安全判定**（改后缀的恶意文件被拒绝）。
4. **落盘**：边写临时文件边计算 sha256；按 hash 前两位/前四位分目录存储 `files/ab/cd/<hash>.<ext>`。已存在同 hash 文件则丢弃本次写入（秒传）。
5. **元信息**：解析图片头得到宽高（GIF 动图保留，缩略图取第一帧）；生成 JPEG 缩略图（最长边 350px，质量 82）写入 `thumbs/`。
6. **入库**：插入 `images` 记录（同用户同 hash 幂等，返回已有记录）。
7. **响应**：返回 §3.3 所示的链接集合。

GIF 防御性限制：解码头获取尺寸即可，**不整帧解码动图**，避免大 GIF 内存放大。

### 3.5 图片访问与加速设计（R1，最高优先级）

第三方站点的加载速度由"RTT 次数 × 传输量"决定，逐项压低：

1. **内容寻址 + 永久不可变缓存**：文件名为 sha256，内容永不变化，因此图片响应固定返回：
   ```
   HTTP/1.1 200 OK
   Content-Type: image/jpeg
   Cache-Control: public, max-age=31536000, immutable
   ETag: "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881"
   Access-Control-Allow-Origin: *
   ```
   浏览器二次访问、CDN 回源后全部直接命中缓存，**零回源**。
2. **条件请求**：基于 `http.ServeContent`，携带 ETag/Last-Modified 自动支持 `If-None-Match` → 304（不传 body）与 Range 断点。
3. **零重定向**：URL 即最终地址，无 302 跳转（省 1 个 RTT）。404 返回纯文本 404。
4. **CDN 友好**：配置 `PIXSY_BASE_URL` 后所有外链指向 CDN 域名，CDN 遵循 immutable 头全量命中；源站仅承担回源。生产强烈建议接入任意 HTTP CDN。
5. **缩略图**：`/t/{hash}.jpg` 供第三方小图/预览场景，体积降低一个数量级。
6. **传输层**：反代（Caddy/Nginx）启用 HTTP/2，拥塞丢包网络下单连接多路复用 + 头部压缩显著降低多图加载时间（见 §6.4）。
7. **服务本身轻**：Go 静态文件直出（走 sendfile），常驻内存 ~20MB，低配机器无 GC 压力瓶颈。
8. **外链访问控制（防盗链，可选）**：后台设置两张白名单与仅嵌图开关，**全部未启用时不做任何检测**（升级完全兼容）；guard 只包裹 `/i/`、`/t/`，`/api` 与 SPA 不受影响：
   - `hotlink_whitelist`：访客 IP/CIDR 网段——控制"**哪些网络能取图**"。注意 `<img>` 嵌图是访客浏览器直连源站，命中的是**访客的 IP** 而非引用站点的服务器 IP（反代部署需配 `trusted-proxies` 才能取到真实访客 IP）。
   - `hotlink_referers`：允许引用的站点域名——控制"**哪些网站能嵌图**"。填入 `example.com` 后，该站点页面里的外链图片对它的**所有访客**可见（子域名自动放行；`evil-example.com` 不会误命中；Referer 取 http/https scheme 的 host，去端口、小写匹配）。
   - `hotlink_embed_only`：仅嵌图模式——利用 Fetch Metadata 的 `Sec-Fetch-Dest`（浏览器强制附加、JS 不可伪造/移除）区分"网页里的图片元素加载"（`image`）与"独立页面打开"（`document`，新标签页/地址栏直开/iframe）。开启后文档级导航只认 IP 白名单与登录会话 Cookie，堵住"从白名单站点在新标签页打开图片"的绕过路径。代价：地址栏直开与文档级导航一并失效；不发该头的 RSS 阅读器/老浏览器按原逻辑（视为嵌图加载）。

   放行公式（按序判定，任一命中即放行）：

   ```text
   放行 ⇔ 防盗链未启用（两表全空 ∧ 未开仅嵌图）
     ∨ 仅嵌图 ∧ 文档级导航（Sec-Fetch-Dest 存在且非 image）
         ∧ (ClientIP ∈ IP表 ∨ 登录会话 Cookie)    ← Referer/空 Referer 放行不适用于文档导航
     ∨ 嵌图加载或未声明（dest=image 或缺失）
         ∧ (两表全空
           ∨ ClientIP ∈ IP表
           ∨ (域名表启用 ∧ (无有效 Referer ∨ Referer 域名命中))
           ∨ 登录会话 Cookie)                     ← 登录时下发的 JWT（HttpOnly+SameSite=Lax，任何角色）
   ```

   未命中返回 **200 空响应体 + `Cache-Control: no-store`**：不向引用方暴露拦截行为，同时防止空响应被 CDN/浏览器按缓存规则存储造成毒化。

   注意事项：
   - 空 Referer（地址栏直开、RSS/Markdown 阅读器、隐私设置等）只在域名表启用时放行；仅启用 IP 表时仍按 IP 严格判定。
   - Referer 可被非浏览器客户端伪造，域名白名单定位是"防白嫖"而非严格访问控制。
   - 登录用户访问不受白名单与仅嵌图限制（会话 Cookie 放行）——图库的「打开原图」是新标签页导航（`dest=document`），不能被误伤；开放注册时注册用户因此可绕过白名单取图，属已接受的取舍。
   - CDN 命中 immutable 缓存后不再回源，会绕过源站白名单——启用白名单时需关闭 `/i/`、`/t/` 的 CDN 缓存，或在 CDN 层配置等价 IP 规则。

### 3.6 权限模型（R5）

| 能力 | 游客 | 普通用户 | 超级管理员 |
|---|---|---|---|
| 查看图片外链 | ✅（有 URL 即可） | ✅ | ✅ |
| 注册 | 受 `allow_register` | — | — |
| 上传 | ❌（必须登录） | 受 `allow_upload` | ✅（不受开关限制） |
| 管理/删除自己的图片 | ❌ | ✅ | ✅ |
| 删除任何图片、修改系统设置、查看全部图片 | ❌ | ❌ | ✅ |

### 3.7 系统设置（R5）

`settings` KV，启动加载进内存、写操作即时刷新缓存：

| 键 | 默认 | 说明 |
|---|---|---|
| `site_name` | `pixsy` | 站点名称 |
| `allow_register` | `true` | 是否允许新用户注册（用户数为 0 时注册始终放行） |
| `allow_upload` | `true` | 是否允许普通用户上传（admin 不受限） |
| `max_upload_mb` | `20`（首次启动取 `PIXSY_MAX_UPLOAD_MB` 种子值） | 单文件上限，范围 1~1024 |
| `max_batch` | `10` | 单次批量上传文件数上限 |
| `hotlink_whitelist` | 空 | 外链 IP/CIDR 白名单（访客网段，逗号分隔）；空=不启用检测（§3.5） |
| `hotlink_referers` | 空 | 外链引用域名白名单（允许嵌图的站点，逗号分隔）；空=不启用检测（§3.5） |
| `hotlink_embed_only` | `false` | 仅嵌图模式：`Sec-Fetch-Dest` 非 image 的文档级导航（新标签页/地址栏）只认 IP 白名单与登录会话 Cookie（§3.5） |

`PUT /api/v1/admin/settings` 请求体：`{"allow_register":false,"allow_upload":true,"max_upload_mb":20,"site_name":"pixsy"}`。**开关修改即时生效**，无需重启。

### 3.8 配置项

| 环境变量 | 命令行 | 默认 | 说明 |
|---|---|---|---|
| `PIXSY_ADDR` | `--addr` | `:8080` | 监听地址 |
| `PIXSY_DATA` | `--data` | `./data` | 数据目录（SQLite + 图片） |
| `PIXSY_BASE_URL` | `--base-url` | 空 | 图片外链前缀（如 CDN 域名）；空则按请求 Host 生成 |
| `PIXSY_MAX_UPLOAD_MB` | `--max-upload-mb` | `20` | 首次启动时写入 settings 的种子值 |
| `PIXSY_JWT_SECRET` | `--jwt-secret` | 自动生成存 `data/jwt.secret` | 多实例/生产显式指定；**不得硬编码进代码或仓库** |

日志：`log/slog` 输出到 stdout（JSON 格式），记录请求摘要（方法、路径、状态、耗时）与关键错误。

## 4. 前端设计

### 4.1 技术栈与结构

- Vue 3 + TypeScript + Vite + Pinia + vue-router；**不引入 UI 组件库**，样式全部自写（设计令牌见 §4.3），构建产物 gzip < 200KB（R2）。
- HTTP 用原生 `fetch` 封装（拦截 401 跳登录、解析 `error.message` toast 提示）；**上传进度用 `XMLHttpRequest`**（fetch 不支持上传进度回调）。

```
web/src/
├── main.ts / App.vue / router.ts
├── api/            # client.ts（fetch 封装）、auth.ts、images.ts、admin.ts
├── stores/         # auth.ts（token/user）、config.ts（公开配置缓存）
├── components/     # Uploader.vue、ImageGrid.vue、ImageDetail.vue、CopyField.vue、Toast.vue
├── views/          # LoginView.vue、RegisterView.vue、UploadView.vue、
│                   # MyImagesView.vue、AdminView.vue、NotFoundView.vue
└── styles/         # tokens.css、base.css
```

### 4.2 页面与交互

| 路由 | 页面 | 要点 |
|---|---|---|
| `/login` `/register` | 登录/注册 | 首屏调 `/api/v1/config`：`allow_register=false` 时注册入口隐藏并提示；表单校验（用户名 3~32 位、密码 ≥ 8 位） |
| `/`（上传） | 上传页 | 拖拽 / 点击选择 / `Ctrl+V` 粘贴截图；多文件队列逐个显示状态与进度条；**成功即时展示该图的链接卡片**（原图/缩略图 URL、Markdown、HTML、BBCode，点击复制）；上传前本地预检类型与大小（读 config） |
| `/images` | 我的图片 | 缩略图网格（每页 40），点击打开详情浮层：大图、尺寸/大小/时间、各格式链接复制、删除按钮（二次确认） |
| `/admin` | 系统设置 | 仅 admin 可见（路由守卫 + 后端双重校验）：`allow_register`、`allow_upload` 开关，`max_upload_mb` 数字输入，保存后 toast 反馈 |

### 4.3 UI 风格规范（R2）

- 设计令牌（`tokens.css`）：中性灰阶 + 单一主色（蓝 #3b82f6）；圆角 8px；卡片 + 充足留白；系统字体栈（`system-ui`）；阴影仅用于浮层。
- 深浅色跟随系统 `prefers-color-scheme`；移动端单列自适应；无多余动效（仅 150ms 的 hover/过渡）。
- 全站中文文案；核心动作（复制链接）点击后有"已复制"反馈。

### 4.4 与后端的契约

- token 存 `localStorage`，请求头 `Authorization: Bearer`；401 时清除并跳登录。
- `allow_upload=false` 时前端禁用上传入口并提示（最终以后端 403 为准，前端提示仅优化体验）。
- 构建：`npm run build` 产出到 `web/dist`，后端 `//go:embed` 打包；开发模式下若设置 `PIXSY_STATIC_DIR` 则从磁盘读取，支持前端热更新。

## 5. 部署设计（R6）

### 5.1 交付制品

| 制品 | 说明 |
|---|---|
| `pixsy-linux-amd64` / `pixsy-linux-arm64` | 静态单文件，`CGO_ENABLED=0 go build` 交叉编译 |
| `pixsy-windows-amd64.exe` | 同上，直接双击或计划任务/服务运行 |
| Docker 镜像 | 多阶段构建，最终基于 alpine，~25MB |

### 5.2 Dockerfile（多阶段）

```dockerfile
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM golang:1.23-alpine AS app
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /pixsy ./cmd/pixsy

FROM alpine:3.20
COPY --from=app /pixsy /pixsy
WORKDIR /app
EXPOSE 8080
VOLUME /app/data
ENTRYPOINT ["/pixsy"]
```

### 5.3 docker-compose 与 systemd

```yaml
# docker-compose.yml
services:
  pixsy:
    build: .
    ports: ["8080:8080"]
    environment:
      - PIXSY_BASE_URL=https://img.example.com
      # - PIXSY_JWT_SECRET=<生产显式指定，勿提交真实值到仓库>
    volumes: ["./data:/app/data"]
    restart: unless-stopped
```

```ini
# /etc/systemd/system/pixsy.service
[Unit]
Description=pixsy image host
After=network.target

[Service]
User=pixsy
WorkingDirectory=/opt/pixsy
ExecStart=/opt/pixsy/pixsy --addr :8080 --data /opt/pixsy/data
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

### 5.4 反向代理与 CDN（配合 R1）

Caddy（自动 TLS + HTTP/2/3，两行搞定）：

```
img.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Nginx 等价片段：

```nginx
server {
    listen 443 ssl http2;
    server_name img.example.com;
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
    }
}
```

CDN 接入：将 `img.example.com` 接入任意 HTTP CDN，回源到源站；因图片响应带 `immutable` 长缓存，CDN 命中率接近 100%，低配源站几乎不承担外链流量。`PIXSY_BASE_URL` 设为 CDN 域名即可让所有外链走 CDN。

## 6. 开发阶段计划

各阶段有明确验收标准（DoD），P1 编码任务（前端/后端）按本节执行，P2 评审任务按 §7 测试计划核对。

### 阶段 0：脚手架（0.5 天）

- 初始化 Go module、`cmd/pixsy` 入口、config 加载、SQLite 迁移（users/images/settings）、`/api/v1/config` 空实现。
- 初始化 `web/`（Vite + Vue3 + TS）、tokens.css。
- **DoD**：`go build ./...` 与 `npm run build` 通过；访问 `/api/v1/config` 返回 JSON。

### 阶段 1：后端开发（2~3 天，对应 tasks/P1-后端）

1. 认证模块：注册（含首管逻辑与兜底）、登录、JWT、中间件、限速。
2. 设置模块：settings 读写与缓存、admin API。
3. 存储模块：魔数校验、流式上传、sha256 落盘、秒传、缩略图、尺寸解析。
4. 图片服务：`/i/`、`/t/` 直出（immutable + ETag + 304）。
5. 图片管理 API：上传/列表/删除。
6. 单元测试（见 §7.1）随代码同批提交。
- **DoD**：`go vet ./...`、`go test ./...` 全绿；curl 按 §3.3 示例完整跑通注册→登录→上传→访问→管理全流程。

### 阶段 2：前端开发（2~3 天，对应 tasks/P1-前端）

1. API client 与 Pinia store、路由守卫。
2. 登录/注册页 → 上传页（队列/进度/复制链接）→ 我的图片（网格/详情/删除）→ 管理设置页。
3. tokens.css 与深浅色适配。
- **DoD**：`vue-tsc --noEmit` 与 `npm run build` 通过（对应 tasks/P2 编译与类型检查）；按 §7.2 手工走查全部页面。

### 阶段 3：打包与部署（1 天）

1. `embed` 前端产物、`PIXSY_STATIC_DIR` 开发覆盖。
2. Dockerfile、docker-compose.yml、交叉编译脚本（Makefile）、README 部署章节。
- **DoD**：Linux 二进制、Windows exe、Docker 容器三端各自完成 §7.3 中 TC-DEPLOY-001~003 冒烟。

### 阶段 4：测试与评审（1 天，对应 tasks/P2）

- 执行 §7 全部测试用例并记录结果；核对 §7.5 需求覆盖矩阵；修补缺陷。
- **DoD**：全部用例通过或在文档中记录偏差与原因。

## 7. 测试计划（开发后执行）

### 7.1 后端自动化测试（`go test ./...`，httptest + 临时数据目录）

| 测试文件 | 用例 |
|---|---|
| `auth_test.go` | 首个注册用户 role=admin；第二个注册用户 role=user；用户数为 0 时 `allow_register=false` 仍可注册；重复用户名 409；登录成功返回 token；错误密码 401；`/auth/me` 无 token 401；登录限速 429 |
| `upload_test.go` | jpg/png/gif/webp 上传成功且返回 url/thumb_url；伪造扩展名（实为文本）400；超 `max_upload_mb` 413；`allow_upload=false` 时普通用户 403、admin 仍成功；未登录 401；批量上传多文件；同文件重复上传返回相同 URL（秒传） |
| `serve_test.go` | `/i/` 响应含 `Cache-Control: public, max-age=31536000, immutable` 与正确 Content-Type；`If-None-Match` 返回 304；`/t/` 缩略图可访问且最长边 ≤ 350px；不存在 hash 返回 404；URL 无 302 重定向 |
| `settings_test.go` | 普通用户访问 admin API 403；admin 修改 `allow_register=false` 后注册 403；`max_upload_mb` 修改即时生效；非法值 400 |
| `images_test.go` | owner 删除成功且记录与文件移除；非 owner 删除他人图片 403；admin 可删除任意图片；同 hash 多记录引用时文件延迟删除；列表分页与 `scope=all` |

### 7.2 前端测试

- 自动化：`vue-tsc --noEmit` 类型检查、`npm run build` 构建产物检查（gzip 体积 < 200KB）。
- 手工走查清单：登录/注册表单校验与错误提示；拖拽/粘贴/选择三种上传方式；上传进度与失败重试提示；五类链接复制按钮；图片网格分页与详情浮层；删除二次确认；admin 开关保存后 `/api/v1/config` 立即反映；深色模式；移动端宽度（375px）可用。

### 7.3 端到端验收用例表

| 用例 ID | 模块 | 步骤 | 预期结果 |
|---|---|---|---|
| TC-AUTH-001 | 首管 | 全新数据目录，注册第一个用户 | 该用户 `role=admin`（R3） |
| TC-AUTH-002 | 首管 | 紧接着注册第二个用户 | `role=user`（R3） |
| TC-AUTH-003 | 注册 | 关闭 `allow_register` 后调用注册接口 | 403，提示"注册已关闭"（R5） |
| TC-SET-001 | 开关 | admin 关闭 `allow_upload`，普通用户上传 | 403；admin 自己上传仍成功（R5） |
| TC-SET-002 | 开关 | 普通用户调用 `PUT /api/v1/admin/settings` | 403（R5） |
| TC-UP-001 | 上传 | 依次上传 jpg/png/gif | 均成功并返回可访问的 `url`（R4、R7） |
| TC-UP-002 | 上传 | 上传改后缀为 `.jpg` 的文本文件 | 400，提示非法图片（R4） |
| TC-UP-003 | 上传 | 将 `max_upload_mb` 设为 1，上传 2MB 图片 | 413（R4） |
| TC-UP-004 | 上传 | 未带 token 调用上传 API | 401（R8） |
| TC-UP-005 | 上传 | 同一文件重复上传两次 | 两次返回的 `url` 完全一致（§3.1 秒传） |
| TC-URL-001 | 加速 | `curl -I` 请求图片 URL | 200 直出，含 `immutable` 长缓存与 ETag（R1） |
| TC-URL-002 | 加速 | 带 `If-None-Match` 再次请求 | 304 空响应体（R1） |
| TC-URL-003 | 加速 | 请求缩略图 URL | 200，JPEG 且最长边 ≤ 350px（R1） |
| TC-DEL-001 | 删除 | owner 删除自己的图片 | 记录删除，再次访问 URL 404 |
| TC-DEL-002 | 删除 | 普通用户删除他人图片 | 403；admin 删除成功 |
| TC-API-001 | API | 仅用 curl 完成注册→登录→上传→取回 URL | 全流程成功（R8） |
| TC-DEPLOY-001 | 部署 | Linux 下载二进制直接运行 | 服务可用，data 目录生成（R6） |
| TC-DEPLOY-002 | 部署 | Windows 运行 exe | 同上（R6） |
| TC-DEPLOY-003 | 部署 | `docker compose up` 后走 TC-API-001 | 全流程成功（R6） |
| TC-PERF-001 | 性能 | CDN 场景二次请求图片 | CDN 命中，源站无访问日志（R1） |

### 7.4 需求覆盖矩阵

| 需求 | 设计章节 | 验证用例 |
|---|---|---|
| R1 快速加载 | §3.5、§5.4 | TC-URL-001~003、TC-PERF-001、7.1 serve_test |
| R2 轻量+UI | §2.1、§4.3 | 7.2 体积检查、手工走查 |
| R3 首个用户为管理员 | §3.2 | TC-AUTH-001/002 |
| R4 格式与体积限制 | §3.4 | TC-UP-001~003 |
| R5 管理开关 | §3.7 | TC-SET-001/002、TC-AUTH-003 |
| R6 多端部署 | §5 | TC-DEPLOY-001~003 |
| R7 自动返回链接 | §3.3 | TC-UP-001、TC-API-001 |
| R8 公开 API | §3.3 | TC-API-001、TC-UP-004 |

### 7.5 性能基线（验收参考）

- 2 核 2G 服务器本地直出原图 P99 < 50ms；200 并发图片请求无错误。
- 进程常驻内存 < 50MB（空载）；Docker 镜像 < 30MB；前端 gzip < 200KB。

## 8. 风险与后续扩展

**风险与对策**：SQLite 单写者——图库写频率极低，足够；大 GIF/超大图——流式处理 + 尺寸头解析 + 上限硬限制；滥用（恶意刷上传）——登录 + 限速 + 可关闭注册/上传；JWT 密钥泄露——自动生成文件权限 600，支持环境变量覆盖。

**后续扩展候选**（本期不做，接口设计已预留空间）：S3/对象存储后端、WebP/AVIF 编码、外链统计与热点分析、游客上传开关、用户配额、图片格式转换与压缩等级设置。
