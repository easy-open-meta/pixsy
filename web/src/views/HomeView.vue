<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'

import { errorMessage } from '../api/client'
import type { ImageItem } from '../api/images'
import { PAGE_SIZE, deleteImage, formatSize, listImages, uploadImage } from '../api/images'
import ImageCard from '../components/ImageCard.vue'
import { useAuthStore } from '../stores/auth'
import { useConfigStore } from '../stores/config'
import { useToastStore } from '../stores/toast'

const auth = useAuthStore()
const config = useConfigStore()
const toast = useToastStore()

const siteName = computed(() => config.config?.site_name || 'pixsy')

/* ---------- 图片列表 ---------- */
const images = ref<ImageItem[]>([])
const total = ref(0)
const page = ref(1)
const scope = ref<'mine' | 'all'>('mine')
const loading = ref(false)
const loadError = ref('')

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))
const emptyText = computed(() =>
  scope.value === 'all' ? '暂无任何图片' : '还没有图片，上传一张试试吧。',
)

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const result = await listImages(page.value, scope.value)
    images.value = result.images
    total.value = result.total
  } catch (err) {
    loadError.value = errorMessage(err, '图片列表加载失败，请稍后重试')
  } finally {
    loading.value = false
  }
}

function switchScope(next: 'mine' | 'all'): void {
  if (scope.value === next) return
  scope.value = next
  page.value = 1
  void load()
}

function goPage(next: number): void {
  if (next < 1 || next > totalPages.value || next === page.value) return
  page.value = next
  void load()
}

// 登录态就绪后再拉列表；登出时清空，避免残留越权数据。
watch(
  () => auth.isLoggedIn,
  (loggedIn) => {
    if (loggedIn) {
      scope.value = 'mine'
      page.value = 1
      void load()
    } else {
      images.value = []
      total.value = 0
    }
  },
)

onMounted(() => {
  if (auth.isLoggedIn) void load()
})

async function removeImage(image: ImageItem): Promise<void> {
  if (!window.confirm(`确定删除「${image.filename}」吗？删除后不可恢复。`)) return
  try {
    await deleteImage(image.id)
    toast.success('图片已删除')
    if (images.value.length === 1 && page.value > 1) {
      page.value -= 1 // 当前页删空后自动前翻
    }
    await load()
  } catch (err) {
    toast.error(errorMessage(err, '删除失败，请稍后重试'))
  }
}

/* ---------- 上传 ---------- */
interface UploadTask {
  name: string
  percent: number
  status: 'uploading' | 'done' | 'failed'
  error?: string
}

const fileInput = ref<HTMLInputElement | null>(null)
const tasks = ref<UploadTask[]>([])
const uploading = ref(false)

const allowUpload = computed(() => auth.isLoggedIn && (config.config?.allow_upload ?? false))
const maxUploadMb = computed(() => config.config?.max_upload_mb ?? 0)
const maxBatch = computed(() => config.config?.max_batch ?? 0)

function pickFiles(): void {
  fileInput.value?.click()
}

/**
 * 云端占位文件（OneDrive 按需下载等）读内容会立刻失败，浏览器只能报笼统的
 * 网络错误；上传前先试读文件头部，把真实原因提前反馈给用户。
 */
async function preflightReadError(file: File): Promise<string | null> {
  try {
    await file.slice(0, 64 * 1024).arrayBuffer()
    return null
  } catch (err) {
    if (err instanceof DOMException && err.name === 'NotFoundError') {
      return '文件不存在或已被移动'
    }
    return '本地文件读取失败：云端文件可能尚未同步到本地，请先同步（OneDrive 可右键「始终保留在此设备」）后重试'
  }
}

async function onFilesChosen(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = '' // 清空以便可再次选择同一批文件
  if (!files.length || uploading.value) return

  if (maxBatch.value > 0 && files.length > maxBatch.value) {
    toast.error(`单批最多上传 ${maxBatch.value} 个文件`)
    return
  }

  const maxBytes = maxUploadMb.value > 0 ? maxUploadMb.value * 1024 * 1024 : 0
  const accepted = files.filter((file) => {
    if (maxBytes > 0 && file.size > maxBytes) {
      toast.error(`「${file.name}」超过单图 ${maxUploadMb.value} MB 上限`)
      return false
    }
    return true
  })
  if (!accepted.length) return

  // 逐文件串行上传：进度条互不干扰，也避免占满带宽
  uploading.value = true
  for (const file of accepted) {
    const readError = await preflightReadError(file)
    if (readError) {
      tasks.value.unshift(
        reactive<UploadTask>({ name: file.name, percent: 0, status: 'failed', error: readError }),
      )
      continue
    }
    const task = reactive<UploadTask>({ name: file.name, percent: 0, status: 'uploading' })
    tasks.value.unshift(task)
    try {
      await uploadImage(file, (percent) => {
        task.percent = percent
      }).promise
      task.percent = 100
      task.status = 'done'
    } catch (err) {
      task.status = 'failed'
      task.error = errorMessage(err, '上传失败')
    }
  }
  uploading.value = false

  // 回到第一页刷新，让新图立刻可见
  if (scope.value !== 'mine' || page.value !== 1) {
    scope.value = 'mine'
    page.value = 1
    void load()
  } else {
    await load()
  }
}

/* ---------- 大图预览 ---------- */
const preview = ref<ImageItem | null>(null)
</script>

<template>
  <div class="container">
    <section v-if="!auth.ready" class="card empty">
      <p class="muted">正在加载…</p>
    </section>

    <section v-else-if="!auth.isLoggedIn" class="card empty">
      <h2>欢迎使用 {{ siteName }}</h2>
      <p class="muted">登录后即可上传图片并获取外链。</p>
      <div class="row empty-actions">
        <RouterLink class="btn btn-primary" :to="{ name: 'login' }">去登录</RouterLink>
        <RouterLink v-if="config.config?.allow_register" class="btn" :to="{ name: 'register' }">
          注册账号
        </RouterLink>
      </div>
    </section>

    <template v-else>
      <section v-if="allowUpload" class="card upload-panel">
        <div class="spread">
          <div>
            <h2>上传图片</h2>
            <p class="small muted">单图 ≤ {{ maxUploadMb }} MB · 单批最多 {{ maxBatch }} 张</p>
          </div>
          <button class="btn btn-primary" type="button" :disabled="uploading" @click="pickFiles">
            {{ uploading ? '上传中…' : '选择图片' }}
          </button>
        </div>
        <input
          ref="fileInput"
          class="visually-hidden"
          type="file"
          accept="image/*"
          multiple
          @change="onFilesChosen"
        />
        <ul v-if="tasks.length" class="upload-tasks">
          <li v-for="(task, index) in tasks" :key="`${task.name}-${index}`" class="upload-task">
            <div class="spread">
              <span class="upload-name" :title="task.name">{{ task.name }}</span>
              <span class="small" :class="task.status === 'failed' ? 'upload-failed' : 'muted'">
                <template v-if="task.status === 'uploading'">{{ task.percent }}%</template>
                <template v-else-if="task.status === 'done'">完成</template>
                <template v-else>{{ task.error || '上传失败' }}</template>
              </span>
            </div>
            <div class="upload-bar">
              <div
                class="upload-bar-fill"
                :class="task.status"
                :style="{ width: `${task.percent}%` }"
              />
            </div>
          </li>
        </ul>
      </section>
      <section v-else class="card">
        <p class="muted">当前站点未开放上传。</p>
      </section>

      <div class="spread list-toolbar">
        <div class="row">
          <template v-if="auth.isAdmin">
            <button
              type="button"
              class="btn btn-sm scope-btn"
              :class="{ active: scope === 'mine' }"
              @click="switchScope('mine')"
            >
              我的
            </button>
            <button
              type="button"
              class="btn btn-sm scope-btn"
              :class="{ active: scope === 'all' }"
              @click="switchScope('all')"
            >
              全部
            </button>
          </template>
          <h2 v-else>我的图片</h2>
        </div>
        <span class="small muted">共 {{ total }} 张</span>
      </div>

      <p v-if="loadError" class="form-error">{{ loadError }}</p>

      <div v-if="loading" class="card empty">
        <p class="muted">加载中…</p>
      </div>
      <div v-else-if="images.length" class="grid">
        <ImageCard
          v-for="image in images"
          :key="image.id"
          :image="image"
          @open="preview = $event"
          @remove="removeImage"
        />
      </div>
      <section v-else-if="!loadError" class="card empty">
        <p class="muted">{{ emptyText }}</p>
      </section>

      <nav v-if="totalPages > 1" class="row pager">
        <button class="btn btn-sm" type="button" :disabled="page <= 1" @click="goPage(page - 1)">
          上一页
        </button>
        <span class="small muted">{{ page }} / {{ totalPages }}</span>
        <button
          class="btn btn-sm"
          type="button"
          :disabled="page >= totalPages"
          @click="goPage(page + 1)"
        >
          下一页
        </button>
      </nav>
    </template>

    <div v-if="preview" class="lightbox" @click.self="preview = null">
      <figure class="lightbox-body">
        <img :src="preview.url" :alt="preview.filename" />
        <figcaption class="spread">
          <span class="lightbox-name" :title="preview.filename">{{ preview.filename }}</span>
          <span class="small muted">
            {{ formatSize(preview.size) }}
            <template v-if="preview.width && preview.height">
              · {{ preview.width }}×{{ preview.height }}
            </template>
          </span>
        </figcaption>
        <div class="row">
          <a class="btn btn-sm" :href="preview.url" target="_blank" rel="noopener">打开原图</a>
          <button class="btn btn-sm" type="button" @click="preview = null">关闭</button>
        </div>
      </figure>
    </div>
  </div>
</template>

<style scoped>
.empty {
  text-align: center;
}

.empty-actions {
  justify-content: center;
  margin-top: var(--space-4);
}

.upload-panel {
  margin-bottom: var(--space-5);
}

.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}

.upload-tasks {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin: var(--space-4) 0 0;
  padding: 0;
  list-style: none;
}

.upload-task {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.upload-name {
  max-width: 70%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.upload-failed {
  color: var(--danger);
}

.upload-bar {
  height: 6px;
  border-radius: 999px;
  background: var(--surface-2);
  overflow: hidden;
}

.upload-bar-fill {
  height: 100%;
  border-radius: 999px;
  background: var(--primary);
  transition: width var(--transition);
}

.upload-bar-fill.done {
  background: var(--success);
}

.upload-bar-fill.failed {
  background: var(--danger);
}

.list-toolbar {
  margin: var(--space-5) 0 var(--space-4);
}

.btn-sm {
  padding: 5px 12px;
  font-size: 13px;
}

.scope-btn.active {
  border-color: var(--primary);
  color: var(--primary);
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: var(--space-4);
}

.pager {
  justify-content: center;
  margin-top: var(--space-6);
}

.lightbox {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  background: rgba(0, 0, 0, 0.7);
}

.lightbox-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  width: min(920px, 100%);
  margin: 0;
  padding: var(--space-4);
  background: var(--surface);
  border-radius: var(--radius);
}

.lightbox-body img {
  display: block;
  max-width: 100%;
  max-height: 65vh;
  margin: 0 auto;
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  object-fit: contain;
}

.lightbox-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
