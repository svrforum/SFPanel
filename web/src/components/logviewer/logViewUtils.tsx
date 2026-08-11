// Shared presentation utilities for the log viewer (Logs page + FirewallLogs
// tab). These used to be verbatim copies in both pages and had already
// drifted (regex pre-compilation only landed on one side).

export type LineCount = 100 | 500 | 1000 | 5000

export const LINE_COUNT_OPTIONS: LineCount[] = [100, 500, 1000, 5000]

export const ROW_HEIGHT = 20

// Cap the in-memory line buffer during live tail. Slack window: only slice
// when we're well past the cap so the allocation amortises across many
// batches instead of running on every flush during sustained high log rates.
export function appendLogLines(prev: string[], batch: string[]): string[] {
  const next = prev.concat(batch)
  return next.length > 5500 ? next.slice(-5000) : next
}

export function highlightText(text: string, query: string) {
  if (!query) return text
  const parts: Array<{ text: string; match: boolean }> = []
  const lower = text.toLowerCase()
  const qLower = query.toLowerCase()
  let lastIndex = 0
  let idx = lower.indexOf(qLower)
  while (idx !== -1) {
    if (idx > lastIndex) {
      parts.push({ text: text.slice(lastIndex, idx), match: false })
    }
    parts.push({ text: text.slice(idx, idx + query.length), match: true })
    lastIndex = idx + query.length
    idx = lower.indexOf(qLower, lastIndex)
  }
  if (lastIndex < text.length) {
    parts.push({ text: text.slice(lastIndex), match: false })
  }
  return (
    <>
      {parts.map((part, i) =>
        part.match ? (
          <mark key={i} className="bg-yellow-400/80 text-black rounded-sm px-0.5">{part.text}</mark>
        ) : (
          <span key={i}>{part.text}</span>
        )
      )}
    </>
  )
}

// Pre-compiled regexes for log level detection (avoid re-creating per call)
const RE_ERROR = /\b(ERROR|FATAL|CRITICAL|PANIC|EMERG)\b/
const RE_WARN = /\b(WARN|WARNING)\b/
const RE_INFO = /\b(INFO|NOTICE)\b/
const RE_DEBUG = /\b(DEBUG|TRACE)\b/
const RE_EMPTY_ERROR = /"error":""/g

export type LogLevel = 'error' | 'warn' | 'info' | 'debug'

export function getLogLevel(line: string): LogLevel | null {
  const cleaned = line.replace(RE_EMPTY_ERROR, '')
  const upper = cleaned.toUpperCase()
  if (RE_ERROR.test(upper)) return 'error'
  if (RE_WARN.test(upper)) return 'warn'
  if (RE_INFO.test(upper)) return 'info'
  if (RE_DEBUG.test(upper)) return 'debug'
  return null
}

export const LOG_LEVEL_COLORS: Record<string, string> = {
  error: 'border-l-2 border-l-red-500/70',
  warn: 'border-l-2 border-l-yellow-500/70',
  info: 'border-l-2 border-l-blue-500/50',
  debug: 'border-l-2 border-l-gray-500/40',
}

export const LOG_LEVEL_TEXT_COLORS: Record<string, string> = {
  error: 'text-red-400',
  warn: 'text-yellow-400',
  info: 'text-gray-200',
  debug: 'text-gray-500',
}
