<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import { useAuthStore } from '../stores/auth'
import { useConfigStore } from '../stores/config'

const auth = useAuthStore()
const config = useConfigStore()
const router = useRouter()

const siteName = computed(() => config.config?.site_name || 'pixsy')
const isAdmin = computed(() => auth.user?.role === 'admin')

function logout(): void {
  auth.logout()
  void router.push({ name: 'home' })
}
</script>

<template>
  <header class="header">
    <div class="container header-inner">
      <RouterLink :to="{ name: 'home' }" class="brand">
        <img src="/favicon.svg" alt="" width="22" height="22" />
        <span>{{ siteName }}</span>
      </RouterLink>

      <nav class="nav">
        <RouterLink v-if="isAdmin" :to="{ name: 'admin' }" class="nav-link">后台</RouterLink>

        <template v-if="auth.isLoggedIn">
          <span class="nav-user small muted">{{ auth.user?.username }}</span>
          <button type="button" class="btn btn-sm" @click="logout">退出</button>
        </template>
        <template v-else>
          <RouterLink :to="{ name: 'login' }" class="nav-link">登录</RouterLink>
          <RouterLink
            v-if="config.config?.allow_register"
            :to="{ name: 'register' }"
            class="btn btn-primary btn-sm"
          >
            注册
          </RouterLink>
        </template>
      </nav>
    </div>
  </header>
</template>

<style scoped>
.header {
  position: sticky;
  top: 0;
  z-index: 20;
  background: var(--surface);
  border-bottom: 1px solid var(--border);
}

.header-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  height: 56px;
}

.brand {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--text);
  font-weight: 600;
}

.nav {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.nav-link {
  color: var(--text-muted);
}

.nav-link:hover,
.nav-link.router-link-active {
  color: var(--text);
}

.nav-user {
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.btn-sm {
  padding: 5px 12px;
  font-size: 13px;
}

@media (max-width: 640px) {
  .nav-user {
    display: none;
  }
}
</style>
