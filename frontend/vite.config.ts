/// <reference types="vitest/config" />
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The dev proxy targets the Gateway by default (upstream flow). A demo backend
// (cmd/demo-issue-board-web on :8899) can take its place via VITE_PROXY_TARGET.
const gatewayTarget = process.env['VITE_PROXY_TARGET'] ?? 'http://localhost:8081'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/auth': gatewayTarget,
      '/api': gatewayTarget,
      '/healthz': gatewayTarget,
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{ts,tsx}'],
      // Generated code, the browser entry point and test scaffolding are not
      // units under test; everything else counts toward the thresholds.
      exclude: ['src/api/**', 'src/main.tsx', 'src/test/**', 'src/**/*.test.{ts,tsx}'],
      thresholds: { lines: 80, functions: 80, branches: 80, statements: 80 },
    },
  },
})
