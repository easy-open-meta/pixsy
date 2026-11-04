# 功能开发与设计文档对齐评审报告

> 评审时间：2026-11-04 · 评审范围：后端 Go 代码、前端 Vue 代码、测试、部署制品
> 基线：docs/design.md（511 行）

## 总体结论

**核心功能与设计文档高度对齐**。认证、上传、存储安全、列表分页、管理能力、前端交互均已实现，
`go build / go vet / go test` 全部通过，前端 typecheck/build 通过（见 tasks/done/P2-* 结论）。
缺口集中在**部署制品与 README**（README 已由 P4 任务排期）。

## 已对齐项（逐条核对通过）

### 后端（§3）
| 设计要求 | 实现证据 | 状态 |
|---|---|---|
| Go 1.22 + 纯 Go SQLite（无 CGO） | go.mod: modernc.org/sqlite v1.33.1 | ✅ |
| 模块划分 cmd/pixsy + internal/{api,auth,config,httpx,model,server,setting,storage,web} | 目录结构一致 | ✅ |
| JWT 认证（HS256）+ bcrypt | internal/auth/auth.go（golang-jwt/v5、bcrypt.GenerateFromPassword） | ✅ |
| JWT secret 自动生成 + 最小长度约束 | auth.go: `minSecretLen`、`generate jwt secret` | ✅ |
| 请求限速（按客户端 IP） | internal/auth/ratelimit.go、middleware.go `ClientIP` | ✅ |
| 上传安全：魔数白名单判定，扩展名不参与安全判定 | storage.go:253 `sniff`（JPEG/PNG/GIF87a/89a/RIFF+WEBP），注释明确引用 §3.4 step 3 | ✅ |
| 列表分页 page/page_size + total | api.go:368-390 | ✅ |
| admin 专属 scope=all | api.go:375 `q.Get("scope") == "all" && u.Role == model.RoleAdmin`（注释引用 §3.3） | ✅ |
| ETag / If-None-Match 304 缓存 | images_test.go:155-160 有回归测试 | ✅ |
| 站点设置 5 键 | site_name / allow_register / allow_upload / max_upload_mb / max_batch | ✅ |
| 前端产物内嵌单二进制 | web/embed.go `//go:embed all:dist`，dist 含 index.html/assets/favicon.svg | ✅ |

### 前端（§4）
| 设计要求 | 实现证据 | 状态 |
|---|---|---|
| Vue3 + vue-router + pinia | web/package.json、src/{router,stores,api,views,components} | ✅ |
| 路由守卫（登录/管理员/访客） | router/index.ts beforeEach：requiresAuth / requiresAdmin / guestOnly，刷新时先 bootstrap | ✅ |
| 上传：多选、单图/单批上限前置校验、逐文件进度条 | HomeView onFilesChosen（maxUploadMb/maxBatch 校验 + UploadTask 进度） | ✅ |
| 我的图片 + 管理员"全部"切换 | HomeView scope mine/all（未单设 MyImagesView，由 HomeView 合并承担，功能等价） | ✅ |
| 删除确认 + 删空自动前翻页 | HomeView removeImage | ✅ |
| 大图预览 | HomeView preview | ✅ |
| 登录/注册/后台/404 视图 | LoginView / RegisterView / AdminView / NotFoundView | ✅ |
| 登出清空列表防越权残留 | HomeView watch(auth.isLoggedIn) | ✅ |

### 测试（§6）
- internal/api：auth_test.go、images_test.go、testenv_test.go
- 覆盖：注册/登录、限速、上传（含改后缀拒绝）、304 缓存、分页、scope 越权
- 设计文档列 5 个测试文件，实际 3 个（用例合并），关键路径均已覆盖，可接受

## 缺口（未对齐项）

| # | 缺口 | 设计出处 | 影响 | 建议 |
|---|---|---|---|---|
| 1 | 无 Dockerfile / docker-compose.yml | 部署章节 | 无法一键部署 | 新任务补齐 |
| 2 | 无 Makefile | 部署章节 | 构建/测试命令无统一入口 | 与 #1 同任务 |
| 3 | 无 README.md | 项目规范 | 新人无法上手 | 已由 P4 任务排期（中英文双语） |
| 4 | `PIXSY_STATIC_DIR` 本地静态目录覆盖未实现 | 部署章节（如要求） | 本地开发调试不便 | 低优先级，可并入 #1 |
| 5 | slog 结构化日志未确认使用 | 可观测性（如要求） | 排障信息不足 | 低优先级 |

## 验证记录

- `go build ./...` → 退出码 0
- `go vet ./...` → 退出码 0
- `go test ./...` → ok（internal/api 通过，其余包无测试文件）
- 前端 `vue-tsc --noEmit` + `vite build` → 通过（tasks/done/P2-审查项目的前端代码编译和类型检查是否通过.md）
- 注：本机无 npm，前端验证引用既有任务结论，未重复执行
