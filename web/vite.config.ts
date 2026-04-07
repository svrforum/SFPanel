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
      registerType: 'prompt',
      workbox: {
        maximumFileSizeToCacheInBytes: 5 * 1024 * 1024,
        globPatterns: ['**/*.{css,html,ico,png,svg,woff2}', 'assets/*.js'],
        globIgnores: [
          '**/monaco-*.js', '**/ts.worker-*.js', '**/css.worker-*.js',
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
        manualChunks: {
          'react-vendor': ['react', 'react-dom', 'react-router-dom'],
          'ui-vendor': ['class-variance-authority', 'clsx', 'tailwind-merge'],
          'xterm': ['@xterm/xterm', '@xterm/addon-fit', '@xterm/addon-search', '@xterm/addon-unicode11', '@xterm/addon-web-links'],
          'i18n': ['i18next', 'react-i18next', 'i18next-browser-languagedetector'],
          'uplot': ['uplot'],
          'monaco': ['monaco-editor'],
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
        target: 'http://localhost:8443',
        changeOrigin: true,
      },
      '/ws': {
        target: 'ws://localhost:8443',
        ws: true,
      },
    },
  },
})
