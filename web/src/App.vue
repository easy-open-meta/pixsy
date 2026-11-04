<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { RouterView, useRoute } from 'vue-router'

import AppHeader from './components/AppHeader.vue'
import ToastHost from './components/ToastHost.vue'
import { useAuthStore } from './stores/auth'
import { useConfigStore } from './stores/config'

const auth = useAuthStore()
const config = useConfigStore()
const route = useRoute()

const showHeader = computed(() => !route.meta.hideHeader)

onMounted(() => {
  auth.bootstrap()
  void config.load()
})
</script>

<template>
  <AppHeader v-if="showHeader" />
  <main class="app-main">
    <RouterView />
  </main>
  <ToastHost />
</template>

<style scoped>
.app-main {
  padding: var(--space-6) 0 var(--space-6);
  min-height: 60vh;
}
</style>
