import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const target = process.env.PASTURE_API ?? 'http://127.0.0.1:37373'

export default defineConfig({
  plugins: [react()],
  // The production bundle is committed here and embedded into the pasture
  // binary, so `pasture serve` needs no Node process. `web/dist` is never used.
  build: {
    outDir: '../internal/webui/assets',
    emptyOutDir: true,
  },
  server: {
    host: '127.0.0.1',
    port: 5180,
    strictPort: false,
    proxy: {
      '/api': { target, changeOrigin: false, ws: true },
      '/openapi.json': { target, changeOrigin: false },
    },
  },
})
