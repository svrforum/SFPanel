import Editor from '@monaco-editor/react'

interface ComposeEditorProps {
  value: string
  onChange: (value: string) => void
  language?: string
}

export default function ComposeEditor({ value, onChange, language = 'yaml' }: ComposeEditorProps) {
  return (
    <Editor
      height="400px"
      language={language}
      theme="vs-dark"
      value={value}
      onChange={(val) => onChange(val || '')}
      options={{
        minimap: { enabled: false },
        fontSize: 14,
        lineNumbers: 'on',
        scrollBeyondLastLine: false,
        wordWrap: 'on',
        tabSize: 2,
        insertSpaces: true,
        automaticLayout: true,
      }}
    />
  )
}
