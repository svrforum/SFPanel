import { useEffect, useState } from 'react'
import Editor, { type OnMount } from '@monaco-editor/react'
import type * as Monaco from 'monaco-editor'
import '@/lib/monaco' // the bundled Monaco, not the CDN; this file is loaded lazily
import type { TaskProblem } from '@/types/api'

interface TaskYamlEditorProps {
  value: string
  onChange: (value: string) => void
  /** The server's problems with the document, marked on their lines. */
  problems: TaskProblem[]
  /** A line to put the cursor on; a new object each time it is asked for. */
  jump: { line: number } | null
  /** The editor's accessible name. */
  label: string
  height?: string
}

const MARKER_OWNER = 'sfpanel-task'

interface Mounted {
  editor: Monaco.editor.IStandaloneCodeEditor
  monaco: typeof Monaco
}

/** A problem's line as the editor has it: 0 (the whole document) is line 1. */
const lineOf = (model: Monaco.editor.ITextModel, line: number) => Math.min(Math.max(line, 1), model.getLineCount())

// A task's YAML in Monaco. Monaco here only highlights YAML: the server is
// what judges the document, and the problems it sent are drawn on their lines
// (setModelMarkers) as soon as the editor is there and whenever they change.
export default function TaskYamlEditor({ value, onChange, problems, jump, label, height = '480px' }: TaskYamlEditorProps) {
  const [mounted, setMounted] = useState<Mounted | null>(null)

  const onMount: OnMount = (editor, monaco) => setMounted({ editor, monaco })

  useEffect(() => {
    if (!mounted) return
    const { editor, monaco } = mounted
    const model = editor.getModel()
    if (!model) return
    monaco.editor.setModelMarkers(
      model,
      MARKER_OWNER,
      problems.map((p) => {
        const line = lineOf(model, p.line)
        return {
          severity: monaco.MarkerSeverity.Error,
          message: p.message,
          startLineNumber: line,
          startColumn: model.getLineFirstNonWhitespaceColumn(line) || 1,
          endLineNumber: line,
          endColumn: Math.max(model.getLineMaxColumn(line), 2),
        }
      }),
    )
    // Not on value: a marker moves with the text typed around it, and
    // setting the list again would put it back where the server saw it.
  }, [mounted, problems])

  useEffect(() => {
    if (!mounted || !jump) return
    const { editor } = mounted
    const model = editor.getModel()
    if (!model) return
    const line = lineOf(model, jump.line)
    editor.revealLineInCenter(line)
    editor.setPosition({ lineNumber: line, column: 1 })
    editor.focus()
  }, [mounted, jump])

  return (
    <div className="overflow-hidden rounded-xl border border-border">
      <Editor
        height={height}
        language="yaml"
        theme="vs-dark"
        value={value}
        onChange={(v) => onChange(v ?? '')}
        onMount={onMount}
        options={{
          ariaLabel: label,
          minimap: { enabled: false },
          fontSize: 13,
          lineNumbers: 'on',
          scrollBeyondLastLine: false,
          wordWrap: 'on',
          tabSize: 2,
          insertSpaces: true,
          automaticLayout: true,
          fixedOverflowWidgets: true,
        }}
      />
    </div>
  )
}
