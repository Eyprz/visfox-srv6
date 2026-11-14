import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

// https://vite.dev/config/ (vitest.config.ts extends this config for tests)
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  // IEL (or mock server) base URL. All UI traffic goes through /api (REST + SSE).
  const apiTarget = env.VISFOX_API_TARGET || 'http://localhost:8080'

  return {
    plugins: [react(), tailwindcss()],
    server: {
      host: '0.0.0.0',
      port: 5173,
      strictPort: true,
      proxy: {
        '/api': {
          target: apiTarget,
          changeOrigin: true,
        },
      },
    },
  }
})
