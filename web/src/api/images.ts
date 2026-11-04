import { ApiError, getToken, readData, request, toApiError } from './client'

export interface ImageItem {
  id: number
  filename: string
  url: string
  thumb_url: string
  markdown: string
  html: string
  bbcode: string
  size: number
  width: number
  height: number
  hash: string
  ext: string
  user_id: number
  created_at: string
}

export interface ImagePage {
  images: ImageItem[]
  total: number
  page: number
  page_size: number
}

export const PAGE_SIZE = 40

export function listImages(page: number, scope: 'mine' | 'all' = 'mine'): Promise<ImagePage> {
  const q = new URLSearchParams({ page: String(page), page_size: String(PAGE_SIZE) })
  if (scope === 'all') q.set('scope', 'all')
  return request<ImagePage>('GET', `/images?${q.toString()}`)
}

export function deleteImage(id: number): Promise<{ deleted: boolean; id: number }> {
  return request<{ deleted: boolean; id: number }>('DELETE', `/images/${id}`)
}

export interface UploadHandle {
  promise: Promise<ImageItem>
  abort: () => void
}

/**
 * 单文件上传走 XMLHttpRequest，fetch 无法回调上传进度（§4.1）。
 * 逐文件上传便于展示每个文件的独立状态与进度条。
 */
export function uploadImage(file: File, onProgress: (percent: number) => void): UploadHandle {
  const xhr = new XMLHttpRequest()
  const promise = new Promise<ImageItem>((resolve, reject) => {
    const form = new FormData()
    form.append('file', file, file.name)

    xhr.open('POST', '/api/v1/images')
    const token = getToken()
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    xhr.setRequestHeader('Accept', 'application/json')

    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) {
        onProgress(Math.min(99, Math.round((e.loaded / e.total) * 100)))
      }
    }
    xhr.onerror = () => reject(new ApiError(0, 'network', '网络连接失败，请稍后重试'))
    xhr.onabort = () => reject(new ApiError(0, 'aborted', '上传已取消'))
    xhr.onload = () => {
      let body: unknown = null
      try {
        body = JSON.parse(xhr.responseText)
      } catch {
        body = null
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        reject(toApiError(xhr.status, body, `上传失败（HTTP ${xhr.status}）`))
        if (xhr.status === 401) window.dispatchEvent(new Event('pixsy:unauthorized'))
        return
      }
      readData<{ images: ImageItem[] }>(new Response(xhr.responseText, { status: xhr.status }))
        .then((data) => {
          const item = data?.images?.[0]
          if (item) resolve(item)
          else reject(new ApiError(xhr.status, 'empty', '服务器未返回图片信息'))
        })
        .catch(reject)
    }
    xhr.send(form)
  })
  return { promise, abort: () => xhr.abort() }
}

export function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let v = bytes
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

export function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
