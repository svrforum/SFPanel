import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { VitePWA } from 'vite-plugin-pwa'
import path from 'path'

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    VitePWA({
      // autoUpdate (with skipWaiting/clientsClaim below) so a new service
      // worker takes over on the next load instead of waiting for a prompt the
      // app never surfaces — that wait is what left upgraded panels serving a
      // stale precached shell pointing at chunk hashes that no longer exist.
      registerType: 'autoUpdate',
      // The hand-written public/manifest.json (correct name/icons/theme, linked
      // from index.html) is the source of truth. Disable the plugin's generated
      // manifest.webmanifest, which otherwise ships a bogus second manifest
      // (name "web", Vue-green theme, no icons).
      manifest: false,
      workbox: {
        maximumFileSizeToCacheInBytes: 5 * 1024 * 1024,
        globPatterns: ['**/*.{css,html,ico,png,svg,woff2}', 'assets/*.js'],
        globIgnores: [
          // rolldown ≥1.1 (vite 8.1+) names the monaco chunk editor.api2-*.js
          '**/monaco-*.js', '**/editor.api2-*.js', '**/ts.worker-*.js', '**/css.worker-*.js',
          '**/html.worker-*.js', '**/json.worker-*.js', '**/editor.worker-*.js',
          '**/xterm-*.js', '**/lspLanguageFeatures-*.js', '**/tsMode-*.js',
          '**/cssMode-*.js', '**/htmlMode-*.js', '**/jsonMode-*.js',
          '**/abap-*.js', '**/apex-*.js', '**/azcli-*.js', '**/bat-*.js',
          '**/bicep-*.js', '**/cameligo-*.js', '**/clojure-*.js', '**/coffee-*.js',
          '**/csp-*.js', '**/cypher-*.js', '**/dart-*.js', '**/ecl-*.js',
          '**/elixir-*.js', '**/flow9-*.js', '**/freemarker2-*.js', '**/fsharp-*.js',
          '**/graphql-*.js', '**/handlebars-*.js', '**/hcl-*.js', '**/julia-*.js',
          '**/kotlin-*.js', '**/lexon-*.js', '**/liquid-*.js', '**/m3-*.js',
          '**/mdx-*.js', '**/mips-*.js', '**/msdax-*.js', '**/mysql-*.js',
          '**/objective-c-*.js', '**/pascal-*.js', '**/pascaligo-*.js',
          '**/perl-*.js', '**/pgsql-*.js', '**/pla-*.js', '**/postiats-*.js',
          '**/powerquery-*.js', '**/powershell-*.js', '**/protobuf-*.js',
          '**/pug-*.js', '**/qsharp-*.js', '**/razor-*.js', '**/redis-*.js',
          '**/redshift-*.js', '**/restructuredtext-*.js', '**/sb-*.js',
          '**/scala-*.js', '**/scheme-*.js', '**/solidity-*.js', '**/sophia-*.js',
          '**/sparql-*.js', '**/st-*.js', '**/swift-*.js', '**/systemverilog-*.js',
          '**/tcl-*.js', '**/twig-*.js', '**/typespec-*.js', '**/vb-*.js',
          '**/wgsl-*.js',
        ],
        navigateFallback: '/index.html',
        runtimeCaching: [],
        skipWaiting: true,
        clientsClaim: true,
      },
    }),
  ],
  build: {
    chunkSizeWarningLimit: 1000,
    rollupOptions: {
      output: {
        // Function form (rather than object form) is required by vite 8's
        // narrower TypeScript types and works identically in vite 7 — keeping
        // the same chunk groupings as before.
        manualChunks(id: string) {
          if (!id.includes('node_modules')) return undefined
          if (
            id.includes('node_modules/react/') ||
            id.includes('node_modules/react-dom/') ||
            id.includes('node_modules/react-router-dom/') ||
            id.includes('node_modules/react-router/')
          ) {
            return 'react-vendor'
          }
          if (
            id.includes('node_modules/class-variance-authority/') ||
            id.includes('node_modules/clsx/') ||
            id.includes('node_modules/tailwind-merge/')
          ) {
            return 'ui-vendor'
          }
          if (id.includes('node_modules/@xterm/')) return 'xterm'
          if (
            id.includes('node_modules/i18next/') ||
            id.includes('node_modules/react-i18next/') ||
            id.includes('node_modules/i18next-browser-languagedetector/')
          ) {
            return 'i18n'
          }
          if (id.includes('node_modules/uplot/')) return 'uplot'
          // monaco-editor / @monaco-editor are intentionally NOT manual-chunked:
          // forcing them into a named 'monaco' chunk made rolldown co-locate a
          // shared symbol there that the eager entry needed, statically pulling
          // the 3.6 MB editor into first paint. Left to default chunking they
          // ride the lazy Files/DockerStacks chunks that actually import them.
          return undefined
        },
      },
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:3628',
        changeOrigin: true,
      },
      '/ws': {
        target: 'ws://localhost:3628',
        ws: true,
      },
    },
  },
})
