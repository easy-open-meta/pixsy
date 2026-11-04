import { defineStore } from 'pinia'
import { ref } from 'vue'

export type ToastKind = 'success' | 'error' | 'info'

export interface ToastItem {
  id: number
  kind: ToastKind
  text: string
}

let seq = 0

/** 轻量提示队列：最多同时展示 3 条，自动消失（§4.3 反馈）。 */
export const useToastStore = defineStore('toast', () => {
  const items = ref<ToastItem[]>([])

  function remove(id: number): void {
    items.value = items.value.filter((t) => t.id !== id)
  }

  function push(kind: ToastKind, text: string, timeout = 3000): void {
    const id = ++seq
    items.value = [...items.value.slice(-2), { id, kind, text }]
    window.setTimeout(() => remove(id), timeout)
  }

  return {
    items,
    remove,
    success: (text: string) => push('success', text),
    error: (text: string) => push('error', text, 4000),
    info: (text: string) => push('info', text),
  }
})
