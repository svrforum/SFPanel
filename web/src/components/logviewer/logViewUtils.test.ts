import { describe, expect, it } from 'vitest'
import {
  appendLogLines,
  getLogLevel,
  LOG_LEVEL_COLORS,
  LOG_LEVEL_TEXT_COLORS,
  type LogLevel,
} from './logViewUtils'

// logViewUtils.tsx also exports highlightText, which returns JSX — it is left
// untested here so these stay DOM-free (node environment, no jsdom).

describe('appendLogLines', () => {
  const line = (n: number) => `line-${n}`
  const buffer = (n: number) => Array.from({ length: n }, (_, i) => line(i))

  it('concatenates while under the cap', () => {
    expect(appendLogLines(['a', 'b'], ['c'])).toEqual(['a', 'b', 'c'])
  })

  it('returns the previous lines unchanged for an empty batch', () => {
    expect(appendLogLines(['a', 'b'], [])).toEqual(['a', 'b'])
  })

  // The slack window: trimming only kicks in past 5500 so the slice amortises
  // over many batches instead of running on every flush during a live tail.
  it('does not trim at the slack boundary', () => {
    expect(appendLogLines(buffer(5500), []).length).toBe(5500)
  })

  it('trims to 5000 once the slack window is exceeded', () => {
    expect(appendLogLines(buffer(5500), ['overflow']).length).toBe(5000)
  })

  it('keeps the newest lines when trimming', () => {
    const result = appendLogLines(buffer(5501), [])
    expect(result[result.length - 1]).toBe(line(5500))
    expect(result[0]).toBe(line(501))
  })

  it('does not mutate the previous buffer', () => {
    const prev = ['a', 'b']
    appendLogLines(prev, ['c'])
    expect(prev).toEqual(['a', 'b'])
  })
})

describe('getLogLevel', () => {
  const cases: [name: string, line: string, want: LogLevel | null][] = [
    ['ERROR', 'time=... level=ERROR msg="boom"', 'error'],
    ['FATAL', 'FATAL could not bind', 'error'],
    ['CRITICAL', 'CRITICAL disk full', 'error'],
    ['PANIC', 'PANIC runtime failure', 'error'],
    ['EMERG', 'EMERG kernel', 'error'],
    ['WARN', 'level=WARN retrying', 'warn'],
    ['WARNING', 'WARNING deprecated flag', 'warn'],
    ['INFO', 'level=INFO started', 'info'],
    ['NOTICE', 'NOTICE reloaded', 'info'],
    ['DEBUG', 'level=DEBUG cache hit', 'debug'],
    ['TRACE', 'TRACE span', 'debug'],
    // Detection is case-insensitive: the line is upper-cased before matching.
    ['lowercase level', 'level=warn retrying', 'warn'],
    // Word-boundary anchored, so a level name embedded in a longer word does
    // not trigger a false positive.
    ['level name inside a word', 'TERRORS everywhere', null],
    ['no level at all', 'plain log line', null],
    ['empty line', '', null],
    // Severity order is fixed: error wins over anything else on the same line.
    ['error outranks info on the same line', 'level=INFO msg="ERROR handled"', 'error'],
    ['warn outranks info on the same line', 'level=INFO msg="WARN handled"', 'warn'],
    // slog emits an empty error field on success paths; it is stripped first so
    // those lines are not misread as errors.
    ['empty slog error field is ignored', '{"level":"INFO","error":""}', 'info'],
    ['empty slog error field on an otherwise plain line', '{"msg":"ok","error":""}', null],
  ]

  it.each(cases)('%s', (_name, line, want) => {
    expect(getLogLevel(line)).toBe(want)
  })
})

describe('log level color maps', () => {
  it('covers every level getLogLevel can return', () => {
    const levels: LogLevel[] = ['error', 'warn', 'info', 'debug']
    for (const level of levels) {
      expect(LOG_LEVEL_COLORS[level]).toBeTruthy()
      expect(LOG_LEVEL_TEXT_COLORS[level]).toBeTruthy()
    }
  })
})
