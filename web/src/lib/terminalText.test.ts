import { describe, expect, it } from 'vitest'
import { bufferText, type TextBuffer } from './terminalText'

// A buffer of rows as xterm keeps them: `text` is what was written to the row's
// cells (a printed space included), `blank` the cells nothing was written to.
// Like xterm, trimRight drops only the blank cells. `wrapped` marks a row that
// continues the one above it.
type Row = { text: string; blank?: number; wrapped?: boolean }
function buffer(rows: Row[]): TextBuffer {
  return {
    length: rows.length,
    getLine: (y: number) => {
      const row = rows[y]
      if (!row) return undefined
      return {
        isWrapped: row.wrapped === true,
        translateToString: (trimRight?: boolean) => (trimRight ? row.text : row.text + ' '.repeat(row.blank ?? 0)),
      } as unknown as ReturnType<TextBuffer['getLine']>
    },
  }
}

describe('bufferText', () => {
  it('joins a line the terminal wrapped back into one line', () => {
    const b = buffer([
      { text: '$ cat /opt' },
      { text: '/stacks/ap', wrapped: true },
      { text: 'p.yml', blank: 5, wrapped: true },
      { text: 'done', blank: 6 },
    ])
    expect(bufferText(b)).toBe('$ cat /opt/stacks/app.yml\ndone')
  })

  it('keeps a printed space that falls on the wrap point', () => {
    const b = buffer([{ text: 'echo  ' }, { text: 'hi', blank: 4, wrapped: true }])
    expect(bufferText(b)).toBe('echo  hi')
  })

  // At 9 columns '마' (two cells) does not fit after '가나다라' (eight), so the
  // last cell stays unwritten and '마' starts the next row.
  it('adds no space where a double-width character wrapped early', () => {
    const b = buffer([
      { text: 'cd /가나다라', blank: 1 },
      { text: '마바사아', blank: 1, wrapped: true },
      { text: '자차', blank: 5, wrapped: true },
    ])
    expect(bufferText(b)).toBe('cd /가나다라마바사아자차')
  })

  it('drops the empty screen below the output and blank cells at line ends', () => {
    const b = buffer([{ text: 'a', blank: 7 }, { text: '', blank: 8 }, { text: 'b', blank: 7 }, { text: '', blank: 8 }, { text: '   ', blank: 5 }])
    expect(bufferText(b)).toBe('a\n\nb')
  })

  it('reads only the last maxLines rows', () => {
    const b = buffer([{ text: 'one' }, { text: 'two' }, { text: 'tri' }])
    expect(bufferText(b, 2)).toBe('two\ntri')
  })

  it('starts a window that opens mid-wrap on its own line', () => {
    const b = buffer([{ text: 'abcd' }, { text: 'ef', wrapped: true }, { text: 'gh' }])
    expect(bufferText(b, 2)).toBe('ef\ngh')
  })

  it('returns an empty string for an empty terminal', () => {
    expect(bufferText(buffer([{ text: '', blank: 4 }, { text: '', blank: 4 }]))).toBe('')
  })
})
