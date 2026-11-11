<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'

import {
  cancelLskyMigration,
  downloadLskyMappingCsv,
  getLskyMigration,
  getSettings,
  startLskyMigration,
  updateSettings,
} from '../api/admin'
import type { AdminSettings, LskyMigrationStatus } from '../api/admin'
import { errorMessage } from '../api/client'
import AdminUsersPanel from '../components/AdminUsersPanel.vue'
import { useConfigStore } from '../stores/config'
import { useToastStore } from '../stores/toast'

const configStore = useConfigStore()
const toast = useToastStore()

const form = reactive<AdminSettings>({
  site_name: '',
  allow_register: false,
  allow_upload: false,
  max_upload_mb: 10,
  max_batch: 5,
  hotlink_whitelist: '',
  hotlink_referers: '',
  hotlink_embed_only: false,
})

const loading = ref(true)
const loadError = ref('')
const error = ref('')
const submitting = ref(false)

const mig = reactive({ base_url: '', email: '', password: '' })
const migStatus = ref<LskyMigrationStatus | null>(null)
const migError = ref('')
const migStarting = ref(false)
let pollTimer: number | undefined

const progressPct = computed(() => {
  const s = migStatus.value
  if (!s || s.total <= 0) return 0
  return Math.min(100, Math.round(((s.done + s.failed) / s.total) * 100))
})

function stopPolling(): void {
  if (pollTimer !== undefined) {
    window.clearInterval(pollTimer)
    pollTimer = undefined
  }
}

async function refreshMigrate(): Promise<void> {
  try {
    migStatus.value = await getLskyMigration()
    if (!migStatus.value.running) stopPolling()
  } catch (err) {
    stopPolling()
    migError.value = errorMessage(err, '获取迁移进度失败')
  }
}

function startPolling(): void {
  stopPolling()
  pollTimer = window.setInterval(refreshMigrate, 1000)
}

onMounted(async () => {
  try {
    Object.assign(form, await getSettings())
  } catch (err) {
    loadError.value = errorMessage(err, '设置加载失败，请稍后重试')
  } finally {
    loading.value = false
  }
  // 迁移任务可能由上次访问或另一个标签页启动，进来先对一次状态
  await refreshMigrate()
  if (migStatus.value?.running) startPolling()
})

onUnmounted(stopPolling)

/** 把逗号/换行分隔的白名单输入拆成非空条目。 */
function splitList(raw: string): string[] {
  return raw
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function validate(): string {
  if (!form.site_name.trim()) return '站点名称不能为空'
  // v-model.number 在输入框被清空时可能得到非数字值，这里统一兜底校验
  if (!Number.isFinite(form.max_upload_mb) || form.max_upload_mb < 1) {
    return '单图大小上限需为不小于 1 的数字'
  }
  if (!Number.isFinite(form.max_batch) || form.max_batch < 1) {
    return '单批上传数量需为不小于 1 的数字'
  }
  const ips = splitList(form.hotlink_whitelist)
  if (ips.length > 64) return 'IP 白名单不能超过 64 条'
  const domains = splitList(form.hotlink_referers)
  if (domains.length > 64) return '引用域名白名单不能超过 64 条'
  for (const d of domains) {
    // 只做格式预检，合法性与 CIDR 解析由后端校验并回显具体条目
    if (!/^[a-z0-9.-]+$/i.test(d) || d.startsWith('.') || d.endsWith('.') || d.includes('..')) {
      return `引用域名「${d}」格式无效（只填域名本身，不带 https:// 与路径）`
    }
  }
  return ''
}

async function submit(): Promise<void> {
  error.value = validate()
  if (error.value) return
  submitting.value = true
  try {
    const saved = await updateSettings({
      site_name: form.site_name.trim(),
      allow_register: form.allow_register,
      allow_upload: form.allow_upload,
      max_upload_mb: form.max_upload_mb,
      max_batch: form.max_batch,
      hotlink_whitelist: splitList(form.hotlink_whitelist).join(','),
      hotlink_referers: splitList(form.hotlink_referers).join(','),
      hotlink_embed_only: form.hotlink_embed_only,
    })
    Object.assign(form, saved)
    await configStore.load() // 站名等配置即刻反映到页面头部
    toast.success('设置已保存')
  } catch (err) {
    error.value = errorMessage(err, '保存失败，请稍后重试')
  } finally {
    submitting.value = false
  }
}

async function startMigrate(): Promise<void> {
  migError.value = ''
  if (!mig.base_url.trim()) {
    migError.value = '请填写 Lsky 地址'
    return
  }
  if (!mig.email.trim()) {
    migError.value = '请填写 Lsky 登录邮箱'
    return
  }
  if (!mig.password) {
    migError.value = '请填写 Lsky 登录密码'
    return
  }
  migStarting.value = true
  try {
    migStatus.value = await startLskyMigration({
      base_url: mig.base_url.trim(),
      email: mig.email.trim(),
      password: mig.password,
    })
    startPolling()
  } catch (err) {
    migError.value = errorMessage(err, '启动迁移失败，请稍后重试')
  } finally {
    migStarting.value = false
  }
}

async function cancelMigrate(): Promise<void> {
  try {
    migStatus.value = await cancelLskyMigration()
    if (migStatus.value.running) startPolling()
  } catch (err) {
    migError.value = errorMessage(err, '取消迁移失败')
  }
}

async function downloadMapping(): Promise<void> {
  try {
    const blob = await downloadLskyMappingCsv()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'lsky-pixsy-mapping.csv'
    a.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (err) {
    migError.value = errorMessage(err, '下载映射文件失败')
  }
}
</script>

<template>
  <div class="admin-page">
    <div class="container narrow">
    <section class="card">
      <h1>站点设置</h1>
      <p class="small muted">保存后立即生效，影响全站的注册与上传行为。</p>
      <p v-if="loadError" class="form-error">{{ loadError }}</p>
      <p v-if="loading" class="muted">加载中…</p>
      <form v-else @submit.prevent="submit">
        <p v-if="error" class="form-error">{{ error }}</p>
        <div class="field">
          <label for="set-site-name">站点名称</label>
          <input id="set-site-name" v-model.trim="form.site_name" class="input" type="text" />
        </div>
        <label class="check-row">
          <input v-model="form.allow_register" type="checkbox" />
          <span>允许新用户注册</span>
        </label>
        <label class="check-row">
          <input v-model="form.allow_upload" type="checkbox" />
          <span>允许上传图片</span>
        </label>
        <div class="field">
          <label for="set-max-upload">单图大小上限（MB）</label>
          <input
            id="set-max-upload"
            v-model.number="form.max_upload_mb"
            class="input"
            type="number"
            min="1"
          />
        </div>
        <div class="field">
          <label for="set-max-batch">单批上传数量上限</label>
          <input
            id="set-max-batch"
            v-model.number="form.max_batch"
            class="input"
            type="number"
            min="1"
          />
        </div>
        <div class="field">
          <label for="set-hotlink-ip">外链 IP 白名单（防盗链）</label>
          <textarea
            id="set-hotlink-ip"
            v-model="form.hotlink_whitelist"
            class="input"
            rows="3"
            placeholder="每行一个 IP 或网段，如 203.0.113.7、192.168.1.0/24"
          ></textarea>
          <p class="small muted">
            留空不启用。命中任一白名单（IP 或下方引用域名）即可访问图片外链，其余请求返回空内容；登录用户访问不受限制，无需添加自己的 IP。
          </p>
        </div>
        <div class="field">
          <label for="set-hotlink-referer">外链引用域名白名单（防盗链）</label>
          <textarea
            id="set-hotlink-referer"
            v-model="form.hotlink_referers"
            class="input"
            rows="3"
            placeholder="每行一个域名，如 example.com（子域名自动放行）"
          ></textarea>
          <p class="small muted">
            允许嵌图的站点域名：填入后，这些站点页面里的外链图片对它的所有访客可见。留空不启用；启用后直接打开图片地址等无来源页面的请求不受影响。Referer 可被伪造，此项用于阻止盗链而非严格访问控制。
          </p>
        </div>
        <label class="check-row">
          <input v-model="form.hotlink_embed_only" type="checkbox" />
          <span>仅允许嵌图加载（防 F12 新标签页打开）</span>
        </label>
        <p class="small muted">
          开启后，浏览器以独立页面方式打开图片（开发者工具「在新标签页打开」、地址栏直开、iframe 嵌入）一律返回空内容，只有网页里的图片元素可正常加载；RSS
          阅读器、老浏览器等不上报 Sec-Fetch 头的客户端不受影响。登录用户（含图库「打开原图」）不受影响。
        </p>
        <button class="btn btn-primary" type="submit" :disabled="submitting">
          {{ submitting ? '保存中…' : '保存设置' }}
        </button>
      </form>
    </section>

    <section class="card">
      <h2>从 Lsky Pro 迁移图片</h2>
      <p class="small muted">
        迁移在服务器后台进行，图片导入到当前管理员账号；关闭页面不影响进度。迁移会记录旧站路径别名，
        历史文章里的 /i/… 链接换域名后可直接访问；也可下载「旧链接 → 新链接」映射表自行替换。
      </p>
      <form @submit.prevent="startMigrate">
        <div class="field">
          <label for="mig-base">Lsky 地址</label>
          <input
            id="mig-base"
            v-model.trim="mig.base_url"
            class="input"
            type="url"
            placeholder="https://img.example.com"
          />
        </div>
        <div class="field">
          <label for="mig-email">Lsky 登录邮箱</label>
          <input
            id="mig-email"
            v-model.trim="mig.email"
            class="input"
            type="email"
            placeholder="you@example.com"
          />
        </div>
        <div class="field">
          <label for="mig-pass">Lsky 密码</label>
          <input id="mig-pass" v-model="mig.password" class="input" type="password" autocomplete="off" />
        </div>
        <p v-if="migError" class="form-error">{{ migError }}</p>
        <div class="row-actions">
          <button class="btn btn-primary" type="submit" :disabled="migStarting || migStatus?.running">
            {{ migStatus?.running ? '迁移进行中…' : '开始迁移' }}
          </button>
          <button v-if="migStatus?.running" class="btn btn-danger" type="button" @click="cancelMigrate">
            取消迁移
          </button>
        </div>
      </form>

      <div v-if="migStatus?.running || migStatus?.total || migStatus?.started_at" class="mig-status">
        <p v-if="migStatus.running && migStatus.total === 0" class="small muted">
          正在读取 Lsky 图片清单…
        </p>
        <template v-else>
          <div
            v-if="migStatus.total > 0"
            class="progress"
            role="progressbar"
            :aria-valuenow="progressPct"
            aria-valuemin="0"
            aria-valuemax="100"
          >
            <div class="progress-bar" :style="{ width: progressPct + '%' }"></div>
          </div>
          <p class="small">
            已完成 {{ migStatus.done }} / {{ migStatus.total }}
            <template v-if="migStatus.failed"> · 失败 {{ migStatus.failed }}</template>
            <span v-if="migStatus.running && migStatus.current" class="muted">
              · 正在处理 {{ migStatus.current }}
            </span>
          </p>
        </template>
        <p v-if="migStatus.error" class="form-error">{{ migStatus.error }}</p>
        <details v-if="migStatus.failures?.length" class="failures">
          <summary class="small">失败明细（最多显示 20 条）</summary>
          <ul class="small">
            <li v-for="(f, i) in migStatus.failures" :key="i">{{ f }}</li>
          </ul>
        </details>
        <p v-if="migStatus.mapping_count > 0" class="csv-row">
          <button class="btn" type="button" @click="downloadMapping">
            下载映射 CSV（旧链接 → 新链接）
          </button>
        </p>
      </div>
    </section>
    </div>

    <div class="users-col">
      <AdminUsersPanel />
    </div>
  </div>
</template>

<style scoped>
.narrow {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  max-width: 560px;
}

/* 用户管理表格较宽，与设置表单共用两栏式响应布局：宽屏并排，窄屏堆叠。 */
.admin-page {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: var(--space-4);
  width: 100%;
  max-width: var(--container);
  margin: 0 auto;
  padding: 0 var(--space-4);
}

.admin-page > .narrow {
  flex: 1 1 420px;
  padding: 0;
  max-width: none;
}

.users-col {
  flex: 1 1 520px;
  min-width: 0;
}

@media (max-width: 640px) {
  .admin-page {
    flex-direction: column;
    padding: 0 var(--space-4);
  }
}

.check-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-4);
  cursor: pointer;
}

.row-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.mig-status {
  margin-top: var(--space-4);
}

.progress {
  height: 8px;
  margin-bottom: var(--space-2);
  overflow: hidden;
  background: var(--surface-2);
  border-radius: 999px;
}

.progress-bar {
  height: 100%;
  background: var(--primary);
  transition: width var(--transition);
}

.failures {
  margin-top: var(--space-2);
}

.failures ul {
  margin: var(--space-1) 0 0;
  padding-left: 1.2em;
}

.csv-row {
  margin-top: var(--space-3);
}
</style>
