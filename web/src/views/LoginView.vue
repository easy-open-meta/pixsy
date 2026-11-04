<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { errorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'
import { useConfigStore } from '../stores/config'

const auth = useAuthStore()
const config = useConfigStore()
const route = useRoute()
const router = useRouter()

const username = ref('')
const password = ref('')
const error = ref('')
const submitting = ref(false)

const canRegister = computed(() => config.config?.allow_register ?? false)

function validate(): string {
  const name = username.value.trim()
  if (name.length < 3 || name.length > 32) return '用户名长度需在 3~32 个字符之间'
  if (password.value.length < 8) return '密码长度至少 8 个字符'
  return ''
}

async function submit(): Promise<void> {
  error.value = validate()
  if (error.value) return
  submitting.value = true
  try {
    await auth.login(username.value.trim(), password.value)
    // 仅接受站内路径，避免开放重定向
    const redirect = route.query.redirect
    if (typeof redirect === 'string' && redirect.startsWith('/') && !redirect.startsWith('//')) {
      await router.push(redirect)
    } else {
      await router.push({ name: 'home' })
    }
  } catch (err) {
    error.value = errorMessage(err, '登录失败，请检查用户名或密码')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="container narrow">
    <section class="card auth-card">
      <h1>登录</h1>
      <p class="small muted">登录后即可上传图片并管理外链。</p>
      <p v-if="error" class="form-error">{{ error }}</p>
      <form @submit.prevent="submit">
        <div class="field">
          <label for="login-username">用户名</label>
          <input
            id="login-username"
            v-model.trim="username"
            class="input"
            type="text"
            autocomplete="username"
            placeholder="3~32 个字符"
          />
        </div>
        <div class="field">
          <label for="login-password">密码</label>
          <input
            id="login-password"
            v-model="password"
            class="input"
            type="password"
            autocomplete="current-password"
            placeholder="至少 8 个字符"
          />
        </div>
        <button class="btn btn-primary btn-block" type="submit" :disabled="submitting">
          {{ submitting ? '登录中…' : '登录' }}
        </button>
      </form>
      <p v-if="canRegister" class="small muted auth-alt">
        还没有账号？<RouterLink :to="{ name: 'register' }">去注册</RouterLink>
      </p>
    </section>
  </div>
</template>

<style scoped>
.narrow {
  max-width: 420px;
}

.auth-card {
  margin-top: var(--space-6);
}

.auth-alt {
  margin-top: var(--space-4);
}

.btn-block {
  width: 100%;
}
</style>
