import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Локальна розробка: `npm run dev` → http://localhost:5173,
// запити /api проксуються до оркестратора (task up піднімає його на :8080).
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: { '/api': 'http://localhost:8080' },
  },
})
