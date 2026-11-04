import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发时把接口与图片请求转发给本机后端；生产由 Go 直接托管 web/dist。
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/i': 'http://127.0.0.1:8080',
      '/t': 'http://127.0.0.1:8080',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
