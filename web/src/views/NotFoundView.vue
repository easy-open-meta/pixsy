<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <div class="container">
    <section class="card nf-card">
      <h1>404</h1>
      <p class="muted">页面不存在或已被移除。</p>
      <RouterLink class="btn btn-primary" :to="{ name: 'home' }">返回首页</RouterLink>
    </section>
  </div>
</template>

<style scoped>
.nf-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-4);
  max-width: 420px;
  margin: var(--space-6) auto 0;
  text-align: center;
}
</style>
