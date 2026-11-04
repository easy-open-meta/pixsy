import { defineStore } from 'pinia'
import { ref } from 'vue'

import { fetchConfig } from '../api/config'
import type { SiteConfig } from '../api/config'

/** 公开站点配置：站名、注册/上传开关与限额（§3.3）。 */
export const useConfigStore = defineStore('config', () => {
  const config = ref<SiteConfig | null>(null)
  const loaded = ref(false)

  async function load(): Promise<void> {
    try {
      config.value = await fetchConfig()
    } catch {
      // 配置读取失败时按默认开放策略降级，避免整站不可用
      config.value = {
        site_name: 'pixsy',
        allow_register: false,
        allow_upload: false,
        max_upload_mb: 0,
        max_batch: 0,
      }
    } finally {
      loaded.value = true
    }
  }

  return { config, loaded, load }
})
