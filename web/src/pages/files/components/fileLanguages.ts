// Extension → Monaco language id. Single source for "is this a text file"
// checks too (row icon, editor) — previously duplicated as a hand-kept regex.
const languageMap: Record<string, string> = {
  js: 'javascript',
  jsx: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  py: 'python',
  rb: 'ruby',
  go: 'go',
  rs: 'rust',
  java: 'java',
  c: 'c',
  cpp: 'cpp',
  h: 'c',
  hpp: 'cpp',
  cs: 'csharp',
  php: 'php',
  html: 'html',
  htm: 'html',
  css: 'css',
  scss: 'scss',
  less: 'less',
  json: 'json',
  xml: 'xml',
  yaml: 'yaml',
  yml: 'yaml',
  toml: 'toml',
  ini: 'ini',
  conf: 'plaintext',
  cfg: 'ini',
  md: 'markdown',
  sql: 'sql',
  sh: 'shell',
  bash: 'shell',
  zsh: 'shell',
  dockerfile: 'dockerfile',
  makefile: 'plaintext',
  lua: 'lua',
  r: 'r',
  swift: 'swift',
  kt: 'kotlin',
  vue: 'html',
  svelte: 'html',
}

// Text extensions with no Monaco language of their own.
const extraTextExtensions = new Set(['txt', 'log'])

function extOf(filename: string): string {
  return filename.split('.').pop()?.toLowerCase() || ''
}

export function getLanguageFromFilename(filename: string): string {
  return languageMap[extOf(filename)] || 'plaintext'
}

export function isTextFile(filename: string): boolean {
  const ext = extOf(filename)
  return ext in languageMap || extraTextExtensions.has(ext)
}
