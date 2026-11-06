import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  server: {
    port: Number(process.env.CONN_DEV_PORT ?? 5173),
    // changeOrigin false: the API sees the Host the page used, so the page's Origin is the server's own origin, the only
    // one the API's gate accepts (the string shorthand would rewrite the Host to the API's and every write would be refused).
    // ws: a hosted session's terminal is a WebSocket under /api, whose handshake goes through the same gate.
    proxy: { '/api': { target: process.env.CONN_DEV_API ?? 'http://127.0.0.1:7777', changeOrigin: false, ws: true } },
  },
  build: { outDir: 'dist', target: 'es2022' },
})
