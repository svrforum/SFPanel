import type { IBuffer } from '@xterm/xterm'

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

// The cells of the logical line containing `row`: the rows a long line was
// wrapped across, in order. A row's unwritten tail is left out where the line
// continues on the next row — a double-width character that did not fit in the
// last column leaves one such cell, and it is not a space in the text.
function logicalCells(buffer: CellBuffer, cols: number, row: number): Cell[] {
  let first = row
  while (first > 0 && buffer.getLine(first)?.isWrapped) first--
  let last = row
  while (last + 1 < buffer.length && buffer.getLine(last + 1)?.isWrapped) last++
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
