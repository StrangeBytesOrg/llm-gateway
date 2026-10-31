import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '')
  const admin = env.ADMIN_PROXY || 'http://127.0.0.1:42081'
  return {
    plugins: [vue(), tailwindcss()],
    build: {
      outDir: 'dist',
      emptyOutDir: true,
    },
    server: {
      proxy: {
        '/admin': admin,
        '/health': admin,
        '/metrics': admin,
        '/openapi.json': admin,
        '/openapi.yaml': admin,
      },
    },
  }
})
