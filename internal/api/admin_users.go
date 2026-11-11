// Admin 用户管理接口（§3.3）：列表/新建/更新/删除，全部挂在 RequireAdmin 之后。
package api

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/pixsy/pixsy/internal/auth"
	"github.com/pixsy/pixsy/internal/httpx"
	"github.com/pixsy/pixsy/internal/model"
)

// adminDefaultPageSize / adminMaxPageSize 与图片列表分页上限保持一致。
const (
	adminDefaultPageSize = 20
	adminMaxPageSize     = 100
)

// registerRoutes 把 Admin 用户管理路由挂到 mux 上（Handler() 里调用）。
func (s *Server) registerAdminUserRoutes(mux *http.ServeMux, require func(http.Handler) http.Handler) {
	admin := func(h http.HandlerFunc) http.Handler {
		return require(auth.RequireAdmin(h))
	}
	mux.Handle("GET /api/v1/admin/users", admin(s.handleAdminListUsers))
	mux.Handle("POST /api/v1/admin/users", admin(s.handleAdminCreateUser))
	mux.Handle("PUT /api/v1/admin/users/{id}", admin(s.handleAdminUpdateUser))
	mux.Handle("DELETE /api/v1/admin/users/{id}", admin(s.handleAdminDeleteUser))
}

// ---------------------------------------------------------------- GET 列表

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := atoiDefault(q.Get("page"), 1)
	if page < 1 {
		page = 1
	}
	if page > maxPage {
		page = maxPage
	}
	limit := atoiDefault(q.Get("limit"), adminDefaultPageSize)
	if limit < 1 {
		limit = adminDefaultPageSize
	}
	if limit > adminMaxPageSize {
		limit = adminMaxPageSize
	}

	users, total, err := model.ListUsers(r.Context(), s.db, page, limit, q.Get("search"))
	if err != nil {
		slog.Error("admin list users failed", "err", err)
		httpx.Fail(w, http.StatusInternalServerError, "读取用户列表失败")
		return
	}
	httpx.Data(w, http.StatusOK, map[string]any{
		"items": users,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// ---------------------------------------------------------------- POST 新建

type adminCreateUserInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var in adminCreateUserInput
	if !decodeJSON(w, r, &in) {
		return
	}
	name := auth.NormalizeUsername(in.Username)
	if err := auth.ValidateUsername(name); err != nil {
		httpx.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.ValidatePassword(in.Password); err != nil {
		httpx.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	role := in.Role
	if role == "" {
		role = model.RoleUser
	}
	if role != model.RoleAdmin && role != model.RoleUser {
		httpx.Fail(w, http.StatusBadRequest, "角色只能是 admin 或 user")
		return
	}
	pwHash, err := auth.HashPassword(in.Password)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "密码处理失败")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "数据库不可用")
		return
	}
	defer tx.Rollback()

	// users.username 的 UNIQUE 区分大小写，而登录按 NOCASE 匹配；为避免
	// Alice/alice 同时存在后登录行为不确定，这里与登录口径一致做预检查。
	if _, err := model.GetUserByName(ctx, tx, name); err == nil {
		httpx.Fail(w, http.StatusConflict, "用户名已被占用")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		slog.Error("admin create user failed", "err", err)
		httpx.Fail(w, http.StatusInternalServerError, "创建用户失败")
		return
	}

	u, err := model.CreateUserTx(ctx, tx, name, pwHash, role)
	if err != nil {
		if errors.Is(err, model.ErrUsernameTaken) || model.IsUniqueViolation(err) {
			httpx.Fail(w, http.StatusConflict, "用户名已被占用")
			return
		}
		slog.Error("admin create user failed", "err", err)
		httpx.Fail(w, http.StatusInternalServerError, "创建用户失败")
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "创建用户失败")
		return
	}
	httpx.Data(w, http.StatusOK, map[string]any{"user": u})
}

// ---------------------------------------------------------------- PUT 更新

type adminUpdateUserInput struct {
	Role     *string `json:"role"`
	Password *string `json:"password"`
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(w, http.StatusBadRequest, "无效的用户 ID")
		return
	}
	var in adminUpdateUserInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Role == nil && in.Password == nil {
		httpx.Fail(w, http.StatusBadRequest, "至少提供 role 或 password 之一")
		return
	}
	if in.Role != nil && *in.Role != model.RoleAdmin && *in.Role != model.RoleUser {
		httpx.Fail(w, http.StatusBadRequest, "角色只能是 admin 或 user")
		return
	}
	if in.Password != nil {
		if err := auth.ValidatePassword(*in.Password); err != nil {
			httpx.Fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	// 安全保护：禁止管理员把自身降级为普通用户（§3.3）。
	me := auth.UserFrom(r.Context())
	if me != nil && me.ID == id && in.Role != nil && *in.Role != model.RoleAdmin {
		httpx.Fail(w, http.StatusForbidden, "不能修改自己的角色")
		return
	}

	var pwHash *string
	if in.Password != nil {
		h, err := auth.HashPassword(*in.Password)
		if err != nil {
			httpx.Fail(w, http.StatusInternalServerError, "密码处理失败")
			return
		}
		pwHash = &h
	}

	u, err := model.UpdateUser(r.Context(), s.db, id, in.Role, pwHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.Fail(w, http.StatusNotFound, "用户不存在")
			return
		}
		slog.Error("admin update user failed", "err", err)
		httpx.Fail(w, http.StatusInternalServerError, "更新用户失败")
		return
	}
	httpx.Data(w, http.StatusOK, map[string]any{"user": u})
}

// ---------------------------------------------------------------- DELETE 删除

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(w, http.StatusBadRequest, "无效的用户 ID")
		return
	}
	// 安全保护：禁止管理员删除当前登录账号自身（§3.3）。
	me := auth.UserFrom(r.Context())
	if me != nil && me.ID == id {
		httpx.Fail(w, http.StatusForbidden, "不能删除当前登录账号")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// 先取文件引用清单（事务外），删除后按引用数清理磁盘。
	refs, err := model.ListUserFiles(ctx, s.db, id)
	if err != nil {
		slog.Error("admin list user files failed", "err", err)
		httpx.Fail(w, http.StatusInternalServerError, "删除用户失败")
		return
	}

	if err := model.DeleteUser(ctx, s.db, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.Fail(w, http.StatusNotFound, "用户不存在")
			return
		}
		slog.Error("admin delete user failed", "err", err)
		httpx.Fail(w, http.StatusInternalServerError, "删除用户失败")
		return
	}

	// 内容寻址：仅当没有其它记录引用同一 hash 时才删除磁盘文件（§3.1），
	// 同时清理指向它的旧路径别名。失败只告警，不影响删除结果。
	for _, f := range refs {
		n, err := model.CountImagesByHash(ctx, s.db, f.Hash)
		if err != nil {
			slog.Warn("count image refs failed", "hash", f.Hash, "err", err)
			continue
		}
		if n == 0 {
			_ = s.store.Remove(f.Hash, f.Ext)
			_ = model.DeleteAliasesByHash(ctx, s.db, f.Hash)
		}
	}
	httpx.Data(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}
