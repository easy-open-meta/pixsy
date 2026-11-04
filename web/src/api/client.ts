// 原生 fetch 封装：统一解析 { data } / { error: { code, message } }，
// 401 时清除 token 并跳转登录（§4.4）。
const TOKEN_KEY = 'pixsy.token'

export class ApiError extends Error {
  status: number
  code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

export function getToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY)
  } catch {
    return null // 隐私模式下 localStorage 可能不可用
  }
}

export function setToken(token: string | null): void {
  try {
    if (token) localStorage.setItem(TOKEN_KEY, token)
    else localStorage.removeItem(TOKEN_KEY)
  } catch {
    // 忽略存储失败：内存中的状态仍然可用
  }
}

type UnauthorizedHandler = () => void

let unauthorizedHandler: UnauthorizedHandler | null = null

export function setUnauthorizedHandler(fn: UnauthorizedHandler | null): void {
  unauthorizedHandler = fn
}

interface ErrorEnvelope {
  error?: { code?: string; message?: string }
}

export function toApiError(status: number, body: unknown, fallback: string): ApiError {
  const env = body as ErrorEnvelope | null
  return new ApiError(status, env?.error?.code ?? 'error', env?.error?.message ?? fallback)
}

/** 把响应体解析为 data 字段（后端统一包一层 {"data": ...}）。 */
export async function readData<T>(res: Response): Promise<T> {
  const text = await res.text()
  let body: unknown = null
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      body = null
    }
  }
  if (!res.ok) {
    const err = toApiError(res.status, body, `请求失败（HTTP ${res.status}）`)
    if (res.status === 401) unauthorizedHandler?.()
    throw err
  }
  if (body && typeof body === 'object' && 'data' in body) {
    return (body as { data: T }).data
  }
  return body as T
}

export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  let res: Response
  try {
    res = await fetch(`/api/v1${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError(0, 'network', '网络连接失败，请稍后重试')
  }
  return readData<T>(res)
}

/** 从任意异常里取出可直接展示给用户的文案。 */
export function errorMessage(err: unknown, fallback = '操作失败，请稍后重试'): string {
  if (err instanceof ApiError && err.message) return err.message
  if (err instanceof Error && err.message) return err.message
  return fallback
}
