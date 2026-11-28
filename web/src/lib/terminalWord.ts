import type { IBuffer, Terminal } from '@xterm/xterm'

// The subset of xterm's buffer these helpers read, so tests can hand them a
// plain object instead of a live terminal.
export type CellBuffer = Pick<IBuffer, 'length' | 'getLine'>

// A run of text in the buffer, with what term.select() needs to highlight it:
// the first cell and the number of cells, counted across wrapped rows.
export interface TextSpan {
  text: string
  row: number
  col: number
  length: number
}

interface Cell { y: number; x: number; ch: string; w: number }

// A character a URL can carry: RFC 3986's unreserved and reserved ones, and %.
const URL_CHAR = /^[A-Za-z0-9\-._~:/?#[\]@!$&'()*+,;=%]$/
const SCHEME = /https?:\/\//i
// How many rows up a broken URL's start is looked for — far past any real one.
const URL_ROWS = 64

// urlRunsOn: row `row + 1` carries on a URL that row `row` runs into its last
// column. A program that lays out its own text — Claude Code, any Ink-based
// CLI — breaks a long URL with real line ends, and tmux redraws every row on
// its own, so the terminal never marks those rows as wrapped; read row by row,
// the URL stops at the edge of the screen. The rows are taken as one where the
// characters running into the last column are a URL's (a scheme among them,
// here or on the rows above that the same rule joins) and the next row starts
// with a URL character in its first column. A URL that really does end in the
// last column, with an unindented line under it that starts with a letter, is
// joined to that line too: the terminal holds nothing that tells the two apart.
export function urlRunsOn(buffer: CellBuffer, cols: number, row: number): boolean {
  const next = buffer.getLine(row + 1)
  if (!next || next.isWrapped || !URL_CHAR.test(next.getCell(0)?.getChars() ?? '')) return false
  // What the next row adds, for a scheme the break itself split ("https:/" "/…").
  let ahead = ''
  for (let x = 0; x < cols; x++) {
    const ch = next.getCell(x)?.getChars() ?? ''
    if (!URL_CHAR.test(ch)) break
    ahead += ch
  }
  let run = ''
  for (let y = row; y >= 0 && row - y < URL_ROWS; y--) {
    const line = buffer.getLine(y)
    if (!line) return false
    let x = cols - 1
    let part = ''
    for (; x >= 0; x--) {
      const ch = line.getCell(x)?.getChars() ?? ''
      if (!URL_CHAR.test(ch)) break
      part = ch + part
    }
    if (part === '') return false
    run = part + run
    if (SCHEME.test(run + ahead)) return true
    // The run began on this row, and no scheme began it.
    if (x >= 0) return false
  }
  return false
}

// One row runs on into the next: the screen wrapped it, or a URL does.
const runsOn = (buffer: CellBuffer, cols: number, row: number) =>
  buffer.getLine(row + 1)?.isWrapped === true || urlRunsOn(buffer, cols, row)

// The cells of the logical line containing `row`: the rows a long line was
// wrapped across, or a URL broken over, in order. A row's unwritten tail is
// left out where the line continues on the next row — a double-width character
// that did not fit in the last column leaves one such cell, and it is not a
// space in the text.
function logicalCells(buffer: CellBuffer, cols: number, row: number): Cell[] {
  let first = row
  while (first > 0 && runsOn(buffer, cols, first - 1)) first--
  let last = row
  while (last + 1 < buffer.length && runsOn(buffer, cols, last)) last++
  const cells: Cell[] = []
  for (let y = first; y <= last; y++) {
    const line = buffer.getLine(y)
    if (!line) continue
    const rowCells: Cell[] = []
    for (let x = 0; x < cols; x++) {
      const cell = line.getCell(x)
      if (!cell) break
      const w = cell.getWidth()
      if (w === 0) continue // the second half of a double-width character
      rowCells.push({ y, x, ch: cell.getChars(), w })
    }
    if (y < last) {
      while (rowCells.length > 0 && rowCells[rowCells.length - 1].ch === '') rowCells.pop()
    }
    cells.push(...rowCells)
  }
  return cells
}

const isBlank = (ch: string) => ch === '' || /^\s$/.test(ch)
const OPENERS = '"\'`([{<'
// A shell prompt ends in $ or # (user@host:/path$), which is not part of the
// path it follows.
const CLOSERS = '"\'`)]}>,;:.!?$#'

function span(cells: Cell[], from: number, to: number, cols: number): TextSpan {
  const a = cells[from]
  const b = cells[to]
  return {
    text: cells.slice(from, to + 1).map((c) => c.ch || ' ').join(''),
    row: a.y,
    col: a.x,
    length: b.y * cols + b.x + b.w - (a.y * cols + a.x),
  }
}

// wordAt is the word under the cell at (row, col): the run of non-blank
// characters around it, whole even where the screen wrapped it, without the
// quotes, brackets and trailing punctuation that usually surround a path or a
// URL in output ("open /opt/a.yml:" gives /opt/a.yml). Null on a blank cell.
export function wordAt(buffer: CellBuffer, cols: number, row: number, col: number): TextSpan | null {
  const cells = logicalCells(buffer, cols, row)
  const at = cells.findIndex((c) => c.y === row && col >= c.x && col < c.x + c.w)
  if (at < 0 || isBlank(cells[at].ch)) return null
  let from = at
  while (from > 0 && !isBlank(cells[from - 1].ch)) from--
  let to = at
  while (to + 1 < cells.length && !isBlank(cells[to + 1].ch)) to++
  let a = from
  let b = to
  while (a < b && OPENERS.includes(cells[a].ch)) a++
  while (b > a && CLOSERS.includes(cells[b].ch)) b--
  // Trimming must not walk off the character that was pressed.
  if (at < a || at > b) {
    a = from
    b = to
  }
  return span(cells, a, b, cols)
}

// lineAt is the logical line containing `row`, joined across the rows it was
// wrapped over and without its trailing blanks. Null for an empty line.
export function lineAt(buffer: CellBuffer, cols: number, row: number): TextSpan | null {
  const cells = logicalCells(buffer, cols, row)
  let from = 0
  while (from < cells.length && isBlank(cells[from].ch)) from++
  let to = cells.length - 1
  while (to >= from && isBlank(cells[to].ch)) to--
  if (to < from) return null
  return span(cells, from, to, cols)
}

// A cell in the buffer: an absolute row (scrollback included) and a column.
export interface CellPos {
  row: number
  col: number
}

// cellBefore: a comes before b, or is b.
export const cellBefore = (a: CellPos, b: CellPos) => a.row < b.row || (a.row === b.row && a.col <= b.col)

// spanEnd is the last cell a span covers.
export function spanEnd(span: TextSpan, cols: number): CellPos {
  const last = span.col + span.length - 1
  return { row: span.row + Math.floor(last / cols), col: last % cols }
}

// rangeBetween is the selection from cell a to cell b, in either order, both
// ends inclusive, in the form term.select() takes. Neither end splits a
// double-width character: an end on its second half takes the whole of it.
export function rangeBetween(buffer: CellBuffer, cols: number, a: CellPos, b: CellPos): { row: number; col: number; length: number } {
  const [first, last] = cellBefore(a, b) ? [a, b] : [b, a]
  const widthAt = (p: CellPos) => buffer.getLine(p.row)?.getCell(p.col)?.getWidth() ?? 1
  const startCol = first.col > 0 && widthAt(first) === 0 ? first.col - 1 : first.col
  const endCol = last.col + (widthAt(last) === 2 ? 2 : 1)
  return { row: first.row, col: startCol, length: last.row * cols + endCol - (first.row * cols + startCol) }
}

// xterm's web-links rule for where a URL in output starts and ends.
const URL_MATCH = /(https?|HTTPS?):[/]{2}[^\s"'!*(){}|\\^<>`]*[^\s"':,.!?{}|\\^~[\]`()<>]/g

// A match is a URL only if it parses as one and begins with its own scheme and
// host, as xterm's web-links addon checks.
function isUrl(text: string): boolean {
  try {
    const url = new URL(text)
    const user = url.username ? `${url.username}${url.password ? `:${url.password}` : ''}@` : ''
    return text.toLowerCase().startsWith(`${url.protocol}//${user}${url.host}`.toLowerCase())
  } catch {
    return false
  }
}

// A URL in the buffer, from its first cell to its last, both inclusive.
export interface UrlAt {
  text: string
  start: CellPos
  end: CellPos
}

// urlsAt is every URL that covers `row`, whole across the rows the screen
// wrapped it over or the program broke it into.
export function urlsAt(buffer: CellBuffer, cols: number, row: number): UrlAt[] {
  const cells = logicalCells(buffer, cols, row)
  let text = ''
  const owner: number[] = []
  cells.forEach((cell, i) => {
    const s = cell.ch || ' '
    text += s
    for (let k = 0; k < s.length; k++) owner.push(i)
  })
  const found: UrlAt[] = []
  for (const m of text.matchAll(URL_MATCH)) {
    if (!isUrl(m[0])) continue
    const a = cells[owner[m.index]]
    const b = cells[owner[m.index + m[0].length - 1]]
    if (row < a.y || row > b.y) continue
    found.push({ text: m[0], start: { row: a.y, col: a.x }, end: { row: b.y, col: b.x + b.w - 1 } })
  }
  return found
}

// selectedText is what a selection from row `startRow` to row `endRow` copies.
// `text` is xterm's own reading of it, which ends each row with a line break
// unless the next row is one the screen wrapped; a URL a program broke into
// rows comes out with a break at every row. Those breaks are taken out, and
// every other one stays where it was.
export function selectedText(buffer: CellBuffer, cols: number, startRow: number, endRow: number, text: string): string {
  const sep = text.includes('\r\n') ? '\r\n' : '\n'
  const parts = text.split(sep)
  const breaks: number[] = []
  for (let y = startRow + 1; y <= endRow; y++) if (!buffer.getLine(y)?.isWrapped) breaks.push(y)
  // Not xterm's usual reading (a column selection, say): leave it as it is.
  if (breaks.length !== parts.length - 1) return text
  let out = parts[0]
  breaks.forEach((y, i) => { out += (urlRunsOn(buffer, cols, y - 1) ? '' : sep) + parts[i + 1] })
  return out
}

// copyableSelection is what copying the terminal's selection puts on the
// clipboard: xterm's text, with a URL the program broke into rows made whole.
export function copyableSelection(term: Pick<Terminal, 'getSelection' | 'getSelectionPosition' | 'buffer' | 'cols'>): string {
  const text = term.getSelection()
  const at = term.getSelectionPosition()
  return text && at ? selectedText(term.buffer.active, term.cols, at.start.y, at.end.y, text) : text
}
