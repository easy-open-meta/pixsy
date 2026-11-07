import { ApiError, getToken, request, toApiError } from './client'
import type { SiteConfig } from './config'

/** 后台设置：在公开配置基础上追加两个外链白名单（公开接口不返回这两项，§3.3）。 */
export type AdminSettings = SiteConfig & {
  hotlink_whitelist: string
  hotlink_referers: string
}

export type SettingsPatch = Partial<AdminSettings>

export function getSettings(): Promise<AdminSettings> {
  return request<AdminSettings>('GET', '/admin/settings')
}

export function updateSettings(patch: SettingsPatch): Promise<AdminSettings> {
  return request<AdminSettings>('PUT', '/admin/settings', patch)
}

// ---------------------------------------------------------------- Lsky 迁移

export interface LskyMigrationInput {
  base_url: string
  email: string
  password: string
}

export interface LskyMigrationStatus {
  running: boolean
  total: number
  done: number
  failed: number
  current: string
  error: string
  failures: string[]
  started_at?: string
  finished_at?: string
  mapping_count: number
}

export function startLskyMigration(input: LskyMigrationInput): Promise<LskyMigrationStatus> {
  return request<LskyMigrationStatus>('POST', '/admin/migrate/lsky', input)
}

export function getLskyMigration(): Promise<LskyMigrationStatus> {
  return request<LskyMigrationStatus>('GET', '/admin/migrate/lsky')
}

export function cancelLskyMigration(): Promise<LskyMigrationStatus> {
  return request<LskyMigrationStatus>('POST', '/admin/migrate/lsky/cancel')
}

/** 下载 旧URL→新URL 映射 CSV（CSV 不是 JSON，不能走 request）。 */
export async function downloadLskyMappingCsv(): Promise<Blob> {
  const headers: Record<string, string> = { Accept: 'text/csv' }
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`

  let res: Response
  try {
    res = await fetch('/api/v1/admin/migrate/lsky/csv', { headers })
  } catch {
    throw new ApiError(0, 'network', '网络连接失败，请稍后重试')
  }
  if (!res.ok) {
    let body: unknown = null
    try {
      body = await res.json()
    } catch {
      // 非 JSON 错误体，交给默认文案
    }
    throw toApiError(res.status, body, '下载失败，请稍后重试')
  }
  return res.blob()
}
