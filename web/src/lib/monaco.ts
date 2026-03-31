import { loader } from '@monaco-editor/react'
import * as monaco from 'monaco-editor'

// Use locally bundled Monaco instead of CDN
// Required for Tauri desktop where CDN script loading is blocked
loader.config({ monaco })
