import { loader } from '@monaco-editor/react'
// Types come from the package root (erased at build — no runtime barrel import).
import type * as Monaco from 'monaco-editor'
// Slim Monaco: the editor core API only — NOT the `monaco-editor` barrel, which
// statically pulls in the TypeScript language service and its ~6.9 MB ts.worker
// (embedded in the binary via go:embed even though our getWorker override below
// never spawns it). We add back exactly the languages the app edits.
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api'

// Full language services (validation + formatting + their web workers). The CSS
// service also covers scss/less; the HTML service covers html. JSON for configs.
import 'monaco-editor/esm/vs/language/json/monaco.contribution'
import 'monaco-editor/esm/vs/language/css/monaco.contribution'
import 'monaco-editor/esm/vs/language/html/monaco.contribution'

// Syntax-highlighting only (tiny Monarch grammars, no worker) for every other
// file type the Files editor maps (see getLanguageFromFilename). Deliberately
// NO typescript LANGUAGE SERVICE — only its grammar — so .ts/.js still highlight
// while the heavy ts.worker is dropped (TS IntelliSense was already disabled).
import 'monaco-editor/esm/vs/basic-languages/javascript/javascript.contribution'
import 'monaco-editor/esm/vs/basic-languages/typescript/typescript.contribution'
import 'monaco-editor/esm/vs/basic-languages/python/python.contribution'
import 'monaco-editor/esm/vs/basic-languages/ruby/ruby.contribution'
import 'monaco-editor/esm/vs/basic-languages/go/go.contribution'
import 'monaco-editor/esm/vs/basic-languages/rust/rust.contribution'
import 'monaco-editor/esm/vs/basic-languages/java/java.contribution'
import 'monaco-editor/esm/vs/basic-languages/cpp/cpp.contribution' // registers c + cpp
import 'monaco-editor/esm/vs/basic-languages/csharp/csharp.contribution'
import 'monaco-editor/esm/vs/basic-languages/php/php.contribution'
import 'monaco-editor/esm/vs/basic-languages/xml/xml.contribution'
import 'monaco-editor/esm/vs/basic-languages/yaml/yaml.contribution'
import 'monaco-editor/esm/vs/basic-languages/ini/ini.contribution' // ini + cfg
import 'monaco-editor/esm/vs/basic-languages/markdown/markdown.contribution'
import 'monaco-editor/esm/vs/basic-languages/sql/sql.contribution'
import 'monaco-editor/esm/vs/basic-languages/shell/shell.contribution'
import 'monaco-editor/esm/vs/basic-languages/dockerfile/dockerfile.contribution'
import 'monaco-editor/esm/vs/basic-languages/lua/lua.contribution'
import 'monaco-editor/esm/vs/basic-languages/r/r.contribution'
import 'monaco-editor/esm/vs/basic-languages/swift/swift.contribution'
import 'monaco-editor/esm/vs/basic-languages/kotlin/kotlin.contribution'

// Worker factory — only json/css/html have language workers; everything else
// uses the base editor worker (so no ts.worker is ever requested).
;(globalThis as { MonacoEnvironment?: Monaco.Environment }).MonacoEnvironment = {
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
        return new Worker(new URL('monaco-editor/esm/vs/editor/editor.worker.js', import.meta.url), { type: 'module' })
    }
  },
}

// Use the locally bundled Monaco instead of the CDN (required for Tauri desktop,
// where CDN script loading is blocked).
loader.config({ monaco })
