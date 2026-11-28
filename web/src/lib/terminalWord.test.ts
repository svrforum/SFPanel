import { describe, expect, it } from 'vitest'
import { lineAt, rangeBetween, selectedText, spanEnd, urlRunsOn, urlsAt, wordAt, type CellBuffer } from './terminalWord'

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

// A URL a program broke into rows of its own: Claude Code lays out its text
// and ends each row with a real line break, and tmux redraws every row on its
// own, so the terminal never marks the rows as wrapped. Every row but the last
// fills the screen to its last column.
const LOGIN = 'https://example.com/oauth/authorize?code=true&client_id=00000000-0000-4000-8000-000000000000&response_type=code&redirect_uri=https%3A%2F%2Fexample.com%2Fcallback&scope=a+b+c&state=abc123'
function broken(text: string, cols: number): Row[] {
  const rows: Row[] = []
  for (let i = 0; i < text.length; i += cols) rows.push({ text: text.slice(i, i + cols) })
  return rows
}

describe('a URL broken into rows by the program', () => {
  const cols = 47
  const rows = [{ text: '  Browser did not open? Use the url below' }, ...broken(LOGIN, cols), { text: '  Hold Shift while selecting' }]
  const b = buffer(cols, rows)
  const urlRows = Math.ceil(LOGIN.length / cols)

  it('is one word from any of its rows, spanned for select()', () => {
    expect(wordAt(b, cols, 1, 0)).toEqual({ text: LOGIN, row: 1, col: 0, length: LOGIN.length })
    expect(wordAt(b, cols, 3, 20)?.text).toBe(LOGIN)
    expect(wordAt(b, cols, urlRows, 2)?.text).toBe(LOGIN)
  })

  it('is one line for the line copy', () => {
    expect(lineAt(b, cols, 2)?.text).toBe(LOGIN)
    expect(lineAt(b, cols, 0)?.text).toBe('Browser did not open? Use the url below')
    expect(lineAt(b, cols, urlRows + 1)?.text).toBe('Hold Shift while selecting')
  })

  it('is one link on every row it covers, from its first cell to its last', () => {
    const last = { row: urlRows, col: (LOGIN.length - 1) % cols }
    for (const row of [1, 4, urlRows]) {
      expect(urlsAt(b, cols, row)).toEqual([{ text: LOGIN, start: { row: 1, col: 0 }, end: last }])
    }
    expect(urlsAt(b, cols, 0)).toEqual([])
  })

  it('is joined in what a selection across it copies, and nothing else is', () => {
    const selection = ['Browser did not open? Use the url below', ...broken(LOGIN, cols).map((r) => r.text), '  Hold Shift'].join('\n')
    expect(selectedText(b, cols, 0, urlRows + 1, selection)).toBe(['Browser did not open? Use the url below', LOGIN, '  Hold Shift'].join('\n'))
  })

  it('leaves alone a selection text it cannot line up with the rows', () => {
    expect(selectedText(b, cols, 1, 3, 'a column selection')).toBe('a column selection')
  })

  it('keeps a scheme that was itself broken across two rows', () => {
    const split = 'see the page at https:/'
    const w = buffer(split.length, [{ text: split }, { text: '/example.com/a' }])
    expect(urlRunsOn(w, split.length, 0)).toBe(true)
    expect(wordAt(w, split.length, 1, 3)?.text).toBe('https://example.com/a')
  })
})

describe('urlRunsOn leaves rows apart', () => {
  it('when the text that fills a row is not a URL', () => {
    const b = buffer(10, [{ text: 'abcdefghij' }, { text: 'klm' }])
    expect(urlRunsOn(b, 10, 0)).toBe(false)
    expect(wordAt(b, 10, 0, 0)?.text).toBe('abcdefghij')
  })

  it('when the URL stops short of the last column', () => {
    const b = buffer(20, [{ text: 'https://a.example/b' }, { text: 'next' }])
    expect(urlRunsOn(b, 20, 0)).toBe(false)
    expect(urlsAt(b, 20, 0).map((u) => u.text)).toEqual(['https://a.example/b'])
  })

  it('when the next row starts with a blank', () => {
    const b = buffer(19, [{ text: 'https://a.example/b' }, { text: ' and more' }])
    expect(urlRunsOn(b, 19, 0)).toBe(false)
  })

  it('when the next row starts with a box border', () => {
    const b = buffer(19, [{ text: 'https://a.example/b' }, { text: '│ text' }])
    expect(urlRunsOn(b, 19, 0)).toBe(false)
    expect(urlsAt(b, 19, 0).map((u) => u.text)).toEqual(['https://a.example/b'])
  })

  it('when the run that fills a row began on it without a scheme, even under a URL', () => {
    const b = buffer(19, [{ text: 'https://a.example/b' }, { text: ' see abcdefghijklmn' }, { text: 'pq' }])
    expect(urlRunsOn(b, 19, 0)).toBe(false)
    expect(urlRunsOn(b, 19, 1)).toBe(false)
    expect(wordAt(b, 19, 2, 0)?.text).toBe('pq')
  })

  it('when the last row is the end of the buffer', () => {
    const b = buffer(19, [{ text: 'https://a.example/b' }])
    expect(urlRunsOn(b, 19, 0)).toBe(false)
  })
})

describe('urlsAt', () => {
  it('gives a row only the URLs that cover it', () => {
    const b = buffer(20, [{ text: 'https://a.example/x ' }, { text: 'https://b.example/y', wrapped: true }])
    expect(urlsAt(b, 20, 1).map((u) => u.text)).toEqual(['https://b.example/y'])
  })

  it('skips what looks like a URL but does not parse as one', () => {
    expect(urlsAt(buffer(20, [{ text: 'go to http://:80 now' }]), 20, 0)).toEqual([])
  })
})
