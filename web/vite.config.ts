import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react({ compiler: true }), tailwindcss()],
  server: {
    port: 5173,
    proxy: { '/api': 'http://127.0.0.1:8788', '/f': 'http://127.0.0.1:8788' },
  },
  build: { outDir: 'dist', emptyOutDir: true },
})
