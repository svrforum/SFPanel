import { describe, expect, it } from 'vitest'
import { lineAt, rangeBetween, spanEnd, wordAt, type CellBuffer } from './terminalWord'

// Rows of cells as xterm keeps them. A Hangul syllable takes two cells, the
// second of width 0; cells past the text are unwritten (chars '').
type Row = { text: string; wrapped?: boolean }
const wide = (ch: string) => /[ᄀ-ᇿ㄰-㆏가-힣]/.test(ch)
function buffer(cols: number, rows: Row[]): CellBuffer {
  const lines = rows.map((row) => {
    const cells: { ch: string; w: number }[] = []
    for (const ch of row.text) {
      if (wide(ch)) cells.push({ ch, w: 2 }, { ch: '', w: 0 })
      else cells.push({ ch, w: 1 })
    }
    while (cells.length < cols) cells.push({ ch: '', w: 1 })
    return {
      isWrapped: row.wrapped === true,
      getCell: (x: number) => {
        const c = cells[x]
        return c && { getChars: () => c.ch, getWidth: () => c.w }
      },
    }
  })
  return { length: lines.length, getLine: (y: number) => lines[y] } as unknown as CellBuffer
}

describe('wordAt', () => {
  const b = buffer(40, [{ text: 'error: open /opt/stacks/a.yml' }])

  it('takes the run of non-blank characters under the finger', () => {
    expect(wordAt(b, 40, 0, 16)?.text).toBe('/opt/stacks/a.yml')
  })

  it('leaves off the punctuation that follows a word in prose', () => {
    expect(wordAt(b, 40, 0, 2)?.text).toBe('error')
  })

  it('leaves off the $ or # a shell prompt ends in', () => {
    const p = buffer(60, [{ text: 'user@host:/opt/stacks/SFPanel$ ls' }])
    expect(wordAt(p, 60, 0, 20)?.text).toBe('user@host:/opt/stacks/SFPanel')
  })

  it('returns nothing on a blank cell or past the end of the text', () => {
    expect(wordAt(b, 40, 0, 6)).toBeNull()
    expect(wordAt(b, 40, 0, 35)).toBeNull()
  })

  it('strips quotes and brackets around a URL, but keeps what was pressed', () => {
    const q = buffer(40, [{ text: 'see ("https://x.y/z"), then )' }])
    expect(wordAt(q, 40, 0, 10)?.text).toBe('https://x.y/z')
    expect(wordAt(q, 40, 0, 28)?.text).toBe(')')
  })

  it('follows a path the screen wrapped across rows, and spans it for select()', () => {
    const w = buffer(10, [{ text: 'cat /opt/s' }, { text: 'tacks/a.ym', wrapped: true }, { text: 'l', wrapped: true }, { text: '$' }])
    const word = wordAt(w, 10, 1, 3)
    expect(word).toEqual({ text: '/opt/stacks/a.yml', row: 0, col: 4, length: 17 })
    expect(wordAt(w, 10, 3, 0)?.text).toBe('$')
  })

  // At 9 columns '다' does not fit after 'cd /가나' (eight cells): the ninth
  // cell stays unwritten and must not split the word.
  it('adds no break where a double-width character wrapped early', () => {
    const k = buffer(9, [{ text: 'cd /가나' }, { text: '다라마바', wrapped: true }, { text: '사', wrapped: true }])
    const word = wordAt(k, 9, 1, 2)
    expect(word?.text).toBe('/가나다라마바사')
    // From the '/' (row 0, col 3) to the end of '사' (row 2, cells 0-1).
    expect(word).toMatchObject({ row: 0, col: 3, length: 2 * 9 + 2 - 3 })
  })

  it('finds the word from the second half of a double-width character', () => {
    const k = buffer(20, [{ text: '오류 발생: 권한' }])
    expect(wordAt(k, 20, 0, 1)?.text).toBe('오류')
  })
})

describe('lineAt', () => {
  it('joins the rows a line wrapped over and drops the blanks around it', () => {
    const w = buffer(10, [{ text: '  cat /opt' }, { text: '/stacks/a', wrapped: true }, { text: 'next' }])
    expect(lineAt(w, 10, 1)).toEqual({ text: 'cat /opt/stacks/a', row: 0, col: 2, length: 17 })
    expect(lineAt(w, 10, 2)?.text).toBe('next')
  })

  it('returns nothing for an empty line', () => {
    expect(lineAt(buffer(10, [{ text: '   ' }]), 10, 0)).toBeNull()
  })
})

describe('rangeBetween', () => {
  it('spans two cells in either order, both inclusive', () => {
    const b = buffer(10, [{ text: 'hello world' }])
    expect(rangeBetween(b, 10, { row: 0, col: 6 }, { row: 0, col: 2 })).toEqual({ row: 0, col: 2, length: 5 })
  })

  it('crosses rows', () => {
    const b = buffer(10, [{ text: 'abcdefghij' }, { text: 'klm', wrapped: true }])
    expect(rangeBetween(b, 10, { row: 0, col: 8 }, { row: 1, col: 1 })).toEqual({ row: 0, col: 8, length: 4 })
  })

  it('never splits a double-width character at either end', () => {
    const k = buffer(20, [{ text: 'ab가나다' }])
    // End on '나' (cells 4-5), start on the second half of '가' (cell 3).
    expect(rangeBetween(k, 20, { row: 0, col: 3 }, { row: 0, col: 4 })).toEqual({ row: 0, col: 2, length: 4 })
  })
})

describe('spanEnd', () => {
  it('is the last cell of a span, on the row it wrapped to', () => {
    expect(spanEnd({ text: '', row: 0, col: 4, length: 17 }, 10)).toEqual({ row: 2, col: 0 })
  })
})
