import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The build lands directly in the Go embed directory, so `go build` picks it up
// without any copying step.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8077',
        changeOrigin: false,
      },
    },
  },
})
