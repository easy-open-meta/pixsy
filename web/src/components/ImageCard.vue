<script setup lang="ts">
import { computed, ref } from 'vue'

import type { ImageItem } from '../api/images'
import { formatSize } from '../api/images'
import { useToastStore } from '../stores/toast'

const props = defineProps<{
  image: ImageItem
  selectable?: boolean
  selected?: boolean
}>()

const emit = defineEmits<{
  (e: 'open', image: ImageItem): void
  (e: 'toggle', image: ImageItem): void
  (e: 'remove', image: ImageItem): void
}>()

const toast = useToastStore()
const copied = ref(false)
// 缩略图可能缺失（如动画 WebP 无法生成），加载失败时回退到原图，只回退一次
const thumbFailed = ref(false)

function onThumbError(): void {
  if (!thumbFailed.value) thumbFailed.value = true
}

const dimensions = computed(() =>
  props.image.width && props.image.height ? `${props.image.width}×${props.image.height}` : '',
)

async function copy(text: string, label: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = true
    toast.success(`${label}已复制`)
    setTimeout(() => (copied.value = false), 1200)
  } catch {
    toast.error('复制失败，请手动选择文本')
  }
}
</script>

<template>
  <figure class="img-card" :class="{ selected }">
    <button
      v-if="selectable"
      class="img-check"
      type="button"
      :aria-pressed="selected"
      :title="selected ? '取消选择' : '选择'"
      @click.stop="emit('toggle', image)"
    >
      {{ selected ? '✓' : '' }}
    </button>

    <button class="img-frame" type="button" :title="image.filename" @click="emit('open', image)">
      <img
        :src="thumbFailed ? image.url : image.thumb_url"
        :alt="image.filename"
        loading="lazy"
        @error="onThumbError"
      />
    </button>

    <figcaption class="img-meta">
      <span class="img-name" :title="image.filename">{{ image.filename }}</span>
      <span class="img-sub small muted">
        {{ formatSize(image.size) }}<template v-if="dimensions"> · {{ dimensions }}</template>
      </span>
    </figcaption>

    <div class="row img-actions">
      <button class="btn btn-sm" type="button" @click="copy(image.bbcode, 'BBCode')">
        {{ copied ? '已复制' : 'BBCode' }}
      </button>
      <button class="btn btn-sm" type="button" @click="copy(image.markdown, 'Markdown')">
        Markdown
      </button>
      <button class="btn btn-sm btn-danger" type="button" title="删除" @click="emit('remove', image)">
        删除
      </button>
    </div>
  </figure>
</template>

<style scoped>
.img-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: var(--space-3);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  transition: border-color var(--transition), box-shadow var(--transition);
}

.img-card.selected {
  border-color: var(--primary);
  box-shadow: 0 0 0 2px var(--primary-weak);
}

.img-frame {
  display: block;
  width: 100%;
  aspect-ratio: 1 / 1;
  padding: 0;
  border: none;
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  overflow: hidden;
  cursor: zoom-in;
}

.img-frame img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.img-check {
  position: absolute;
  top: 10px;
  left: 10px;
  z-index: 1;
  width: 22px;
  height: 22px;
  padding: 0;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--surface);
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
}

.img-card.selected .img-check {
  background: var(--primary);
  border-color: var(--primary);
  color: var(--on-primary);
}

.img-meta {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.img-name {
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.img-actions {
  flex-wrap: wrap;
  gap: var(--space-2);
}

.btn-sm {
  padding: 4px 8px;
  font-size: 12px;
}
</style>
