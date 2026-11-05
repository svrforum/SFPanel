import { loader } from '@monaco-editor/react'
// Types come from the package root (erased at build — no runtime barrel import).
import type * as Monaco from 'monaco-editor'
// Slim Monaco: the editor core API only — NOT the `monaco-editor` barrel, which
// statically pulls in the TypeScript language service and its ~6.9 MB ts.worker
// (embedded in the binary via go:embed even though our getWorker override below
// never spawns it). We add back exactly the languages the app edits, through
// the entry points monaco-editor 0.56 made the supported ones: its exports map
// no longer reaches the old esm/vs/... paths.
import * as monaco from 'monaco-editor/editor'
// Every editor feature — find, folding, clipboard, comments, the context menu
// and the rest. The JSON service's contribution used to import them all
// statically; its register entry point now loads them with the service, so a
// YAML or shell editor would open without them.
import 'monaco-editor/features/register.all'

// Full language services (validation + formatting + their web workers). The CSS
// service also covers scss/less; the HTML service covers html. JSON for configs.
import 'monaco-editor/languages/features/json/register'
import 'monaco-editor/languages/features/css/register'
import 'monaco-editor/languages/features/html/register'

// Syntax-highlighting only (tiny Monarch grammars, no worker) for every other
// file type the Files editor maps (see getLanguageFromFilename). Deliberately
// NO typescript LANGUAGE SERVICE — only its grammar — so .ts/.js still highlight
// while the heavy ts.worker is dropped (TS IntelliSense was already disabled).
import 'monaco-editor/languages/definitions/javascript/register'
import 'monaco-editor/languages/definitions/typescript/register'
import 'monaco-editor/languages/definitions/python/register'
import 'monaco-editor/languages/definitions/ruby/register'
import 'monaco-editor/languages/definitions/go/register'
import 'monaco-editor/languages/definitions/rust/register'
import 'monaco-editor/languages/definitions/java/register'
import 'monaco-editor/languages/definitions/cpp/register' // registers c + cpp
import 'monaco-editor/languages/definitions/csharp/register'
import 'monaco-editor/languages/definitions/php/register'
import 'monaco-editor/languages/definitions/xml/register'
import 'monaco-editor/languages/definitions/yaml/register'
import 'monaco-editor/languages/definitions/ini/register' // ini + cfg
import 'monaco-editor/languages/definitions/markdown/register'
import 'monaco-editor/languages/definitions/sql/register'
import 'monaco-editor/languages/definitions/shell/register'
import 'monaco-editor/languages/definitions/dockerfile/register'
import 'monaco-editor/languages/definitions/lua/register'
import 'monaco-editor/languages/definitions/r/register'
import 'monaco-editor/languages/definitions/swift/register'
import 'monaco-editor/languages/definitions/kotlin/register'

// Worker factory — only json/css/html have language workers; everything else
// uses the base editor worker (so no ts.worker is ever requested).
;(globalThis as { MonacoEnvironment?: Monaco.Environment }).MonacoEnvironment = {
  getWorker(_moduleId: string, label: string) {
    switch (label) {
      case 'json':
        return new Worker(new URL('monaco-editor/languages/features/json/json.worker.js', import.meta.url), { type: 'module' })
      case 'css':
      case 'scss':
      case 'less':
        return new Worker(new URL('monaco-editor/languages/features/css/css.worker.js', import.meta.url), { type: 'module' })
      case 'html':
      case 'handlebars':
      case 'razor':
        return new Worker(new URL('monaco-editor/languages/features/html/html.worker.js', import.meta.url), { type: 'module' })
      default:
        return new Worker(new URL('monaco-editor/editor/editor.worker.js', import.meta.url), { type: 'module' })
    }
  },
}

// Use the locally bundled Monaco instead of the CDN (required for Tauri desktop,
// where CDN script loading is blocked).
loader.config({ monaco })
