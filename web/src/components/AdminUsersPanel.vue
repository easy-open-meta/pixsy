<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'

import { createUser, deleteUser, getUsers, updateUser } from '../api/admin'
import type { AdminUser, UserRole } from '../api/admin'
import { errorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'
import { useToastStore } from '../stores/toast'

const auth = useAuthStore()
const toast = useToastStore()

const PAGE_SIZE = 20
const ROLES: { value: UserRole; label: string }[] = [
  { value: 'admin', label: '管理员' },
  { value: 'user', label: '普通用户' },
]

// ---------------------------------------------------------------- 列表状态

const users = ref<AdminUser[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(true)
const loadError = ref('')
const searchInput = ref('')
const search = ref('')

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))
const rangeStart = computed(() => (total.value === 0 ? 0 : (page.value - 1) * PAGE_SIZE + 1))
const rangeEnd = computed(() => Math.min(total.value, page.value * PAGE_SIZE))

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const res = await getUsers({
      page: page.value,
      limit: PAGE_SIZE,
      search: search.value || undefined,
    })
    users.value = res.items ?? []
    total.value = res.total ?? 0
    // 删除或搜索后当前页可能已越界，回退到最后一页再取一次
    if (users.value.length === 0 && total.value > 0 && page.value > totalPages.value) {
      page.value = totalPages.value
      await load()
    }
  } catch (err) {
    loadError.value = errorMessage(err, '用户列表加载失败，请稍后重试')
    users.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function goPage(next: number): void {
  if (next < 1 || next > totalPages.value || next === page.value) return
  page.value = next
  void load()
}

function applySearch(): void {
  search.value = searchInput.value.trim()
  page.value = 1
  void load()
}

function resetSearch(): void {
  if (!search.value) return
  searchInput.value = ''
  search.value = ''
  page.value = 1
  void load()
}

function formatDate(raw: string): string {
  if (!raw) return '—'
  // SQLite 的 CURRENT_TIMESTAMP 是 UTC 的 "YYYY-MM-DD HH:MM:SS"，补 T/Z 后按本地时区展示
  const normalized = raw.includes('T') ? raw : `${raw.replace(' ', 'T')}Z`
  const ts = Date.parse(normalized)
  if (Number.isNaN(ts)) return raw
  const d = new Date(ts)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// ---------------------------------------------------------------- 新建 / 编辑

const dialogOpen = ref(false)
const editing = ref<AdminUser | null>(null)
// 密码模式：从「修改密码」入口进入，只允许重置密码，不展示角色下拉。
const pwdMode = ref(false)
const isEdit = computed(() => editing.value !== null)
const editingSelf = computed(() => editing.value !== null && editing.value.id === auth.user?.id)
const dialogTitle = computed(() => {
  const name = editing.value?.username ?? ''
  if (pwdMode.value) return `修改密码「${name}」`
  return isEdit.value ? `编辑用户「${name}」` : '新增用户'
})
const form = reactive<{ username: string; password: string; role: UserRole }>({
  username: '',
  password: '',
  role: 'user',
})
const formError = ref('')
const saving = ref(false)

function openCreate(): void {
  editing.value = null
  pwdMode.value = false
  form.username = ''
  form.password = ''
  form.role = 'user'
  formError.value = ''
  dialogOpen.value = true
}

function openEdit(u: AdminUser, passwordOnly = false): void {
  editing.value = u
  pwdMode.value = passwordOnly
  form.username = u.username
  form.password = ''
  form.role = u.role
  formError.value = ''
  dialogOpen.value = true
}

function closeDialog(): void {
  if (saving.value) return
  dialogOpen.value = false
  editing.value = null
  pwdMode.value = false
}

function validateForm(): string {
  const name = form.username.trim()
  if (isEdit.value) {
    if (pwdMode.value) {
      if (!form.password) return '请填写新密码'
      if (form.password.length < 8) return '新密码长度至少 8 个字符'
    } else if (form.password && form.password.length < 8) {
      return '新密码长度至少 8 个字符'
    }
    return ''
  }
  if (name.length < 3 || name.length > 32) return '用户名长度需在 3~32 个字符之间'
  if (form.password.length < 8) return '密码长度至少 8 个字符'
  return ''
}

async function submitForm(): Promise<void> {
  formError.value = validateForm()
  if (formError.value) return
  saving.value = true
  const newName = form.username.trim()
  try {
    if (isEdit.value && editing.value) {
      const patch: { role?: UserRole; password?: string } = {}
      if (!pwdMode.value && form.role !== editing.value.role) patch.role = form.role
      if (form.password) patch.password = form.password
      if (!patch.role && !patch.password) {
        closeDialog()
        return
      }
      await updateUser(editing.value.id, patch)
      toast.success('用户已更新')
    } else {
      await createUser({ username: newName, password: form.password, role: form.role })
      toast.success(`用户「${newName}」已创建`)
    }
    dialogOpen.value = false
    editing.value = null
    pwdMode.value = false
    await load()
  } catch (err) {
    formError.value = errorMessage(err, isEdit.value ? '更新失败，请稍后重试' : '创建失败，请稍后重试')
  } finally {
    saving.value = false
  }
}

// ---------------------------------------------------------------- 删除

const confirmTarget = ref<AdminUser | null>(null)
const deleting = ref(false)

function askDelete(u: AdminUser): void {
  if (u.id === auth.user?.id) {
    toast.error('不可删除当前登录账号')
    return
  }
  confirmTarget.value = u
}

function cancelDelete(): void {
  if (deleting.value) return
  confirmTarget.value = null
}

async function confirmDelete(): Promise<void> {
  const u = confirmTarget.value
  if (!u) return
  deleting.value = true
  try {
    await deleteUser(u.id)
    toast.success(`用户「${u.username}」已删除`)
    confirmTarget.value = null
    await load()
  } catch (err) {
    // 关闭确认框并把错误同时提示出来，避免用户以为删除成功
    confirmTarget.value = null
    toast.error(errorMessage(err, '删除失败，请稍后重试'))
  } finally {
    deleting.value = false
  }
}

// ---------------------------------------------------------------- 键盘与挂载

function onKeydown(e: KeyboardEvent): void {
  if (e.key !== 'Escape') return
  if (confirmTarget.value) cancelDelete()
  else if (dialogOpen.value) closeDialog()
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  void load()
})

onUnmounted(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <section class="card">
    <div class="spread head">
      <div>
        <h2>用户管理</h2>
        <p class="small muted">新建账号、调整角色、重置密码或删除用户；删除会同时移除该用户的图片。</p>
      </div>
      <button class="btn btn-primary" type="button" @click="openCreate">新增用户</button>
    </div>

    <form class="toolbar" @submit.prevent="applySearch">
      <div class="search-box">
        <input
          v-model="searchInput"
          class="input"
          type="search"
          placeholder="按用户名搜索"
          aria-label="按用户名搜索"
        />
        <button v-if="search" class="btn btn-ghost" type="button" @click="resetSearch">清除</button>
      </div>
      <button class="btn" type="submit" :disabled="loading">搜索</button>
    </form>

    <p v-if="loadError" class="form-error">{{ loadError }}</p>

    <p v-if="loading" class="muted small">加载中…</p>

    <template v-else>
      <p v-if="users.length === 0" class="empty muted">
        {{ search ? `没有匹配「${search}」的用户` : '暂无用户' }}
      </p>

      <div v-else class="table-wrap">
        <table class="user-table">
          <thead>
            <tr>
              <th class="col-id">ID</th>
              <th>用户名</th>
              <th>角色</th>
              <th class="col-time">创建时间</th>
              <th class="col-actions">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in users" :key="u.id">
              <td class="col-id muted" data-label="ID">{{ u.id }}</td>
              <td data-label="用户名">
                <span class="name">{{ u.username }}</span>
                <span v-if="u.id === auth.user?.id" class="small muted">（当前登录）</span>
              </td>
              <td data-label="角色">
                <span class="badge" :class="u.role === 'admin' ? 'badge-admin' : 'badge-user'">
                  {{ u.role === 'admin' ? 'Admin' : 'User' }}
                </span>
              </td>
              <td class="col-time muted small" data-label="创建时间">{{ formatDate(u.created_at) }}</td>
              <td class="col-actions" data-label="操作">
                <div class="row-actions">
                  <button class="btn btn-sm" type="button" @click="openEdit(u, false)">编辑</button>
                  <button class="btn btn-sm" type="button" @click="openEdit(u, true)">修改密码</button>
                  <button
                    class="btn btn-sm btn-danger"
                    type="button"
                    :disabled="u.id === auth.user?.id"
                    :title="u.id === auth.user?.id ? '不可删除当前登录账号' : ''"
                    @click="askDelete(u)"
                  >
                    删除
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="total > PAGE_SIZE" class="pager">
        <button class="btn btn-sm" type="button" :disabled="page <= 1 || loading" @click="goPage(page - 1)">
          上一页
        </button>
        <span class="small muted">第 {{ page }} / {{ totalPages }} 页 · 共 {{ total }} 条</span>
        <button
          class="btn btn-sm"
          type="button"
          :disabled="page >= totalPages || loading"
          @click="goPage(page + 1)"
        >
          下一页
        </button>
      </div>
      <p v-else-if="total > 0" class="small muted pager-hint">共 {{ total }} 位用户</p>
    </template>
  </section>

  <!-- 新增 / 编辑弹窗 -->
  <Teleport to="body">
    <div v-if="dialogOpen" class="overlay" @click.self="closeDialog">
      <div class="modal" role="dialog" aria-modal="true" :aria-label="dialogTitle">
        <h3>{{ dialogTitle }}</h3>
        <p v-if="formError" class="form-error">{{ formError }}</p>
        <form @submit.prevent="submitForm">
          <div class="field">
            <label for="user-name">用户名</label>
            <input
              id="user-name"
              v-model.trim="form.username"
              class="input"
              type="text"
              autocomplete="off"
              :readonly="isEdit"
              :disabled="isEdit"
              placeholder="3~32 个字符"
            />
            <p v-if="!isEdit" class="small muted">3~32 个字符，注册后不可修改。</p>
          </div>
          <div v-if="!pwdMode" class="field">
            <label for="user-role">角色</label>
            <select id="user-role" v-model="form.role" class="input" :disabled="editingSelf">
              <option v-for="r in ROLES" :key="r.value" :value="r.value">{{ r.label }}</option>
            </select>
            <p v-if="editingSelf" class="small muted">不能修改当前登录账号的角色。</p>
          </div>
          <div class="field">
            <label for="user-pass">
              {{ pwdMode ? '新密码' : isEdit ? '重置密码（留空则不修改）' : '初始密码' }}
            </label>
            <input
              id="user-pass"
              v-model="form.password"
              class="input"
              type="password"
              autocomplete="new-password"
              placeholder="至少 8 个字符"
            />
          </div>
          <div class="row-actions">
            <button class="btn btn-primary" type="submit" :disabled="saving">
              {{ saving ? '保存中…' : isEdit ? '保存修改' : '创建用户' }}
            </button>
            <button class="btn" type="button" :disabled="saving" @click="closeDialog">取消</button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>

  <!-- 删除二次确认 -->
  <Teleport to="body">
    <div v-if="confirmTarget" class="overlay" @click.self="cancelDelete">
      <div class="modal" role="alertdialog" aria-modal="true" aria-label="删除用户">
        <h3>删除用户</h3>
        <p>
          确定要删除「{{ confirmTarget.username }}」吗？该用户的图片会一并删除，且无法恢复。
        </p>
        <div class="row-actions">
          <button class="btn btn-danger" type="button" :disabled="deleting" @click="confirmDelete">
            {{ deleting ? '删除中…' : '确认删除' }}
          </button>
          <button class="btn" type="button" :disabled="deleting" @click="cancelDelete">取消</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.head {
  align-items: flex-start;
  gap: var(--space-4);
  margin-bottom: var(--space-4);
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-4);
}

.search-box {
  display: flex;
  flex: 1 1 220px;
  align-items: center;
  gap: var(--space-2);
}

.search-box .input {
  flex: 1;
}

.empty {
  padding: var(--space-5) 0;
  text-align: center;
}

.table-wrap {
  overflow-x: auto;
}

.user-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
}

.user-table th,
.user-table td {
  padding: 10px 8px;
  text-align: left;
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
}

.user-table th {
  font-size: 13px;
  color: var(--text-muted);
  font-weight: 500;
}

.user-table tbody tr:last-child td {
  border-bottom: none;
}

.name {
  font-weight: 500;
}

.badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 500;
}

.badge-admin {
  background: var(--primary-weak);
  color: var(--primary-hover);
}

.badge-user {
  background: var(--surface-2);
  color: var(--text-muted);
}

.row-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.btn-sm {
  padding: 4px 10px;
  font-size: 13px;
}

.btn-ghost {
  padding: 4px 10px;
  font-size: 13px;
  border-color: transparent;
}

.pager {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  margin-top: var(--space-4);
}

.pager-hint {
  margin-top: var(--space-3);
}

.overlay {
  position: fixed;
  inset: 0;
  z-index: 60;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  background: rgba(15, 23, 42, 0.45);
}

.modal {
  width: 100%;
  max-width: 420px;
  padding: var(--space-5);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
}

.modal h3 {
  margin-bottom: var(--space-4);
}

.modal p + .field,
.modal form .field:first-of-type {
  margin-top: var(--space-3);
}

.modal .row-actions {
  justify-content: flex-end;
  margin-top: var(--space-2);
}

/* 移动端：表格转为堆叠卡片，避免横向挤压错位 */
@media (max-width: 640px) {
  .head {
    flex-direction: column;
    align-items: stretch;
  }

  .head .btn {
    align-self: flex-start;
  }

  .table-wrap {
    overflow: visible;
  }

  .user-table thead {
    display: none;
  }

  .user-table,
  .user-table tbody,
  .user-table tr,
  .user-table td {
    display: block;
    width: 100%;
  }

  .user-table tr {
    margin-bottom: var(--space-3);
    padding: var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
  }

  .user-table td {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    padding: 4px 0;
    border-bottom: none;
    white-space: normal;
  }

  .user-table td::before {
    content: attr(data-label);
    font-size: 12px;
    color: var(--text-muted);
  }

  .col-actions .row-actions {
    justify-content: flex-end;
  }
}
</style>
