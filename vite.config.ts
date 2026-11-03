import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  server: { port: Number(process.env.CONN_DEV_PORT ?? 5173), proxy: { '/api': process.env.CONN_DEV_API ?? 'http://127.0.0.1:7777' } },
  build: { outDir: 'dist', target: 'es2022' },
})
