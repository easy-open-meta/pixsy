import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import * as authApi from '../api/auth'
import type { User } from '../api/auth'
import { getToken, setToken } from '../api/client'

/** 登录态：token 持久化在 localStorage，用户信息由 /auth/me 校验（§4.4）。 */
export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const token = ref<string | null>(getToken())
  const ready = ref(false)

  const isLoggedIn = computed(() => token.value !== null && user.value !== null)
  const isAdmin = computed(() => user.value?.role === 'admin')

  function clear(): void {
    user.value = null
    token.value = null
    setToken(null)
  }

  async function login(username: string, password: string): Promise<void> {
    const result = await authApi.login({ username, password })
    setToken(result.token)
    token.value = result.token
    user.value = result.user
  }

  async function register(username: string, password: string): Promise<void> {
    await authApi.register({ username, password })
    await login(username, password)
  }

  /** 启动时校验本地 token 是否仍然有效；失效则清理登录态。 */
  async function bootstrap(): Promise<void> {
    if (!token.value) {
      ready.value = true
      return
    }
    try {
      const result = await authApi.me()
      user.value = result.user
    } catch {
      clear()
    } finally {
      ready.value = true
    }
  }

  /** 退出登录：清空本地登录态（AppHeader 调用）。 */
  function logout(): void {
    clear()
  }

  return { user, token, ready, isLoggedIn, isAdmin, login, register, bootstrap, clear, logout }
})
