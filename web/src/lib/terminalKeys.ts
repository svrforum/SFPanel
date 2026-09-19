export interface TerminalModifiers { shift: boolean; ctrl: boolean; alt: boolean }
export const NO_MODIFIERS: TerminalModifiers = { shift: false, ctrl: false, alt: false }
export const MODIFIERS_EVENT = 'sfpanel:terminal-modifiers'
export const MODIFIERS_CONSUMED_EVENT = 'sfpanel:terminal-modifiers-consumed'

/** xterm modifier parameters: Shift=1, Alt=2, Ctrl=4, plus the base value 1. */
export function terminalKey(data: string, { shift, ctrl, alt }: TerminalModifiers): string {
  const modifier = 1 + Number(shift) + 2 * Number(alt) + 4 * Number(ctrl)
  if (modifier === 1) return data
  if (data.startsWith('\x1b[') && /^[ABCDHF]$/.test(data.slice(2))) return `\x1b[1;${modifier}${data.at(-1)}`
  if (data.startsWith('\x1b[') && /^[356]~$/.test(data.slice(2))) return `\x1b[${data[2]};${modifier}~`
  if (data === '\t' && shift && !ctrl && !alt) return '\x1b[Z'
  // Shift+Enter is the "new line, don't send" key of an AI CLI's composer,
  // and the CSI-u form did the opposite: tmux collapses \x1b[13;2u to a
  // plain carriage return before the pane sees it — in every extended-keys
  // mode, measured — so the key submitted the message it was meant to
  // break. A newline byte reaches the pane untouched, and it is the first
  // key Codex lists for inserting one.
  if (data === '\r' && shift && !ctrl && !alt) return '\n'
  // A multi-character IME composition or bracketed paste must remain intact.
  if (data.length !== 1) return data
  let value = data
  if (ctrl) {
    const code = data.toUpperCase().charCodeAt(0)
    if (code >= 64 && code <= 95) value = String.fromCharCode(code - 64)
    else if (data === ' ') value = '\0'
  } else if (shift) {
    const plain = '`1234567890-=[]\\;\',./'
    const shifted = '~!@#$%^&*()_+{}|:"<>?'
    const index = plain.indexOf(data)
    value = index >= 0 ? shifted[index] : data.toUpperCase()
  }
  return alt ? '\x1b' + value : value
}
