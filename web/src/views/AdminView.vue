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
import type { LskyMigrationStatus } from '../api/admin'
import { errorMessage } from '../api/client'
import type { SiteConfig } from '../api/config'
import { useConfigStore } from '../stores/config'
import { useToastStore } from '../stores/toast'

const configStore = useConfigStore()
const toast = useToastStore()

const form = reactive<SiteConfig>({
  site_name: '',
  allow_register: false,
  allow_upload: false,
  max_upload_mb: 10,
  max_batch: 5,
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

function validate(): string {
  if (!form.site_name.trim()) return '站点名称不能为空'
  // v-model.number 在输入框被清空时可能得到非数字值，这里统一兜底校验
  if (!Number.isFinite(form.max_upload_mb) || form.max_upload_mb < 1) {
    return '单图大小上限需为不小于 1 的数字'
  }
  if (!Number.isFinite(form.max_batch) || form.max_batch < 1) {
    return '单批上传数量需为不小于 1 的数字'
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
        <button class="btn btn-primary" type="submit" :disabled="submitting">
          {{ submitting ? '保存中…' : '保存设置' }}
        </button>
      </form>
    </section>

    <section class="card">
      <h2>从 Lsky Pro 迁移图片</h2>
      <p class="small muted">
        迁移在服务器后台进行，图片导入到当前管理员账号；关闭页面不影响进度。完成后可下载「旧链接 →
        新链接」映射表，用于替换文章里已引用的旧地址。
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
</template>

<style scoped>
.narrow {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  max-width: 560px;
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
