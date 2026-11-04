import { createApp } from 'vue'
import { createPinia } from 'pinia'

import { setUnauthorizedHandler } from './api/client'
import App from './App.vue'
import router from './router'
import { useAuthStore } from './stores/auth'
import './styles/tokens.css'
import './styles/base.css'

createApp(App).use(createPinia()).use(router).mount('#app')

// 401 统一处理：清除本地登录态并回到登录页（§4.4）。
// XHR 上传（images.ts）不走 request()，失败时通过 window 事件汇到这里。
function handleUnauthorized(): void {
  const auth = useAuthStore()
  auth.clear()
  const current = router.currentRoute.value
  if (current.name !== 'login') {
    void router.push({ name: 'login', query: { redirect: current.fullPath } })
  }
}
setUnauthorizedHandler(handleUnauthorized)
window.addEventListener('pixsy:unauthorized', handleUnauthorized)
