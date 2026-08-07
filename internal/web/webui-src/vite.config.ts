import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Backend address the Go binary listens on (`ainovel-cli web --port 5173`).
const backend = 'http://127.0.0.1:5173'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5174,
    proxy: {
      // Vite's dev proxy streams by default (no buffering), which SSE requires.
      '/api': { target: backend, changeOrigin: true },
    },
  },
})
