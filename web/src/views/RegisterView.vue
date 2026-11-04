<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import { errorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'
import { useConfigStore } from '../stores/config'

const auth = useAuthStore()
const config = useConfigStore()
const router = useRouter()

const username = ref('')
const password = ref('')
const confirmPassword = ref('')
const error = ref('')
const submitting = ref(false)

const allowRegister = computed(() => config.config?.allow_register ?? false)

function validate(): string {
  if (!allowRegister.value) return '当前站点未开放注册'
  const name = username.value.trim()
  if (name.length < 3 || name.length > 32) return '用户名长度需在 3~32 个字符之间'
  if (password.value.length < 8) return '密码长度至少 8 个字符'
  if (password.value !== confirmPassword.value) return '两次输入的密码不一致'
  return ''
}

async function submit(): Promise<void> {
  error.value = validate()
  if (error.value) return
  submitting.value = true
  try {
    // 注册成功后 store 内部会自动登录
    await auth.register(username.value.trim(), password.value)
    await router.push({ name: 'home' })
  } catch (err) {
    error.value = errorMessage(err, '注册失败，请稍后重试')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="container narrow">
    <section class="card auth-card">
      <h1>注册</h1>
      <p v-if="!allowRegister" class="form-error">当前站点未开放注册，请联系管理员。</p>
      <p v-else-if="error" class="form-error">{{ error }}</p>
      <form @submit.prevent="submit">
        <div class="field">
          <label for="reg-username">用户名</label>
          <input
            id="reg-username"
            v-model.trim="username"
            class="input"
            type="text"
            autocomplete="username"
            placeholder="3~32 个字符"
            :disabled="!allowRegister"
          />
        </div>
        <div class="field">
          <label for="reg-password">密码</label>
          <input
            id="reg-password"
            v-model="password"
            class="input"
            type="password"
            autocomplete="new-password"
            placeholder="至少 8 个字符"
            :disabled="!allowRegister"
          />
        </div>
        <div class="field">
          <label for="reg-confirm">确认密码</label>
          <input
            id="reg-confirm"
            v-model="confirmPassword"
            class="input"
            type="password"
            autocomplete="new-password"
            placeholder="再次输入密码"
            :disabled="!allowRegister"
          />
        </div>
        <button
          class="btn btn-primary btn-block"
          type="submit"
          :disabled="submitting || !allowRegister"
        >
          {{ submitting ? '注册中…' : '注册' }}
        </button>
      </form>
      <p class="small muted auth-alt">
        已有账号？<RouterLink :to="{ name: 'login' }">去登录</RouterLink>
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
