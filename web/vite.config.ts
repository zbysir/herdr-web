import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwind from '@tailwindcss/vite'
import path from 'node:path'

// 开发时 vite 只管前端，/api 和 /pty 一律转给 Go 后端（默认 7788）。
const backend = process.env.HERDR_WEB_BACKEND || 'http://127.0.0.1:7788'

export default defineConfig({
  plugins: [react(), tailwind()],
  resolve: { alias: { '@': path.resolve(__dirname, 'src') } },
  server: {
    proxy: {
      '/api': { target: backend, changeOrigin: true },
      '/pty': { target: backend, ws: true, changeOrigin: true },
    },
  },
  build: { outDir: 'dist', emptyOutDir: true },
})
