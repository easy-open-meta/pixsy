import { request } from './client'

export interface SiteConfig {
  site_name: string
  allow_register: boolean
  allow_upload: boolean
  max_upload_mb: number
  max_batch: number
}

/** 公开配置与后台设置同一个响应结构（§3.3）。 */
export function fetchConfig(): Promise<SiteConfig> {
  return request<SiteConfig>('GET', '/config')
}
