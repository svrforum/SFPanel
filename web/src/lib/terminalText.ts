import type { IBuffer } from '@xterm/xterm'

// Raised on window to open the terminal page's text selection for the active
// session: by a long press on the terminal, and by the Android app's Tools menu,
// which checks for [data-select-text] on the page before relying on it.
export const SELECT_TEXT_EVENT = 'sfpanel:select-text'

// The subset of xterm's buffer the text export reads, so tests can hand it a
// plain object instead of a live terminal.
export type TextBuffer = Pick<IBuffer, 'length' | 'getLine'>

// bufferText returns the last maxLines rows of a terminal buffer as plain text,
// the way the output was written rather than the way it was drawn: a row that
// only exists because a long line wrapped at the terminal's width is joined
// back onto the row before it, so a copied command or path has no line break
// in the middle. The empty rows below the last output (the unused part of the
// screen) are dropped.
//
// Every row is read with trimRight, which in xterm drops only cells nothing
// was ever written to — a space that was printed stays. That matters at the
// wrap point: a double-width character that did not fit in the last column
// leaves that cell unwritten, and reading it untrimmed put a space into the
// middle of a Korean path. xterm's own selection joins rows the same way.
export function bufferText(buffer: TextBuffer, maxLines = 5000): string {
  const start = Math.max(0, buffer.length - maxLines)
  const lines: string[] = []
  for (let y = start; y < buffer.length; y++) {
    const line = buffer.getLine(y)
    if (!line) continue
    const text = line.translateToString(true)
    if (line.isWrapped && lines.length > 0) lines[lines.length - 1] += text
    else lines.push(text)
  }
  while (lines.length > 0 && lines[lines.length - 1].trim() === '') lines.pop()
  return lines.join('\n')
}
