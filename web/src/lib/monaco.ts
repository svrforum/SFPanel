import { loader } from '@monaco-editor/react'
import * as monaco from 'monaco-editor'

// Override worker factory to skip TypeScript worker (saves ~7MB)
// We only need basic syntax highlighting, not full intellisense
;(globalThis as any).MonacoEnvironment = {
  getWorker(_moduleId: string, label: string) {
    switch (label) {
      case 'json':
        return new Worker(new URL('monaco-editor/esm/vs/language/json/json.worker.js', import.meta.url), { type: 'module' })
      case 'css':
      case 'scss':
      case 'less':
        return new Worker(new URL('monaco-editor/esm/vs/language/css/css.worker.js', import.meta.url), { type: 'module' })
      case 'html':
      case 'handlebars':
      case 'razor':
        return new Worker(new URL('monaco-editor/esm/vs/language/html/html.worker.js', import.meta.url), { type: 'module' })
      default:
        // Use base editor worker for everything else (no TS worker = ~7MB saved)
        return new Worker(new URL('monaco-editor/esm/vs/editor/editor.worker.js', import.meta.url), { type: 'module' })
    }
  },
}

// Use locally bundled Monaco instead of CDN
// Required for Tauri desktop where CDN script loading is blocked
loader.config({ monaco })
