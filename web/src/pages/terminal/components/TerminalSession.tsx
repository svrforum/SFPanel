import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { SearchAddon } from '@xterm/addon-search'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import '@xterm/xterm/css/xterm.css'
import { api } from '@/lib/api'
import { attachXtermTouchScroll } from '@/lib/xtermTouchScroll'
import { attachLongPress } from '@/lib/longPress'
import { SELECT_TEXT_EVENT } from '@/lib/terminalText'
import { cellBefore, lineAt, rangeBetween, spanEnd, wordAt, type CellPos, type TextSpan } from '@/lib/terminalWord'
import { TerminalSelection, type HandleEnd, type Point } from './TerminalSelection'
import { cn, copyText } from '@/lib/utils'
import { toast } from 'sonner'
import { MODIFIERS_CONSUMED_EVENT, MODIFIERS_EVENT, NO_MODIFIERS, terminalKey, type TerminalModifiers } from '@/lib/terminalKeys'

// xterm paints into its own canvas, so it cannot inherit the app's CSS tokens
// the way the rest of the UI does — the palette has to be handed over as an
// object and swapped by hand when the theme flips. Dark is Tokyo Night (what
// this terminal has always used); light is Tokyo Night Day, its official
// counterpart, so the two read as one family rather than two products.
const DARK_THEME = {
  background: '#1a1b26',
  foreground: '#c0caf5',
  cursor: '#c0caf5',
  cursorAccent: '#1a1b26',
  selectionBackground: '#33467c',
  selectionForeground: '#c0caf5',
  black: '#15161e',
  red: '#f7768e',
  green: '#9ece6a',
  yellow: '#e0af68',
  blue: '#7aa2f7',
  magenta: '#bb9af7',
  cyan: '#7dcfff',
  white: '#a9b1d6',
  brightBlack: '#414868',
  brightRed: '#f7768e',
  brightGreen: '#9ece6a',
  brightYellow: '#e0af68',
  brightBlue: '#7aa2f7',
  brightMagenta: '#bb9af7',
  brightCyan: '#7dcfff',
  brightWhite: '#c0caf5',
}

const LIGHT_THEME = {
  background: '#e1e2e7',
  foreground: '#3760bf',
  cursor: '#3760bf',
  cursorAccent: '#e1e2e7',
  selectionBackground: '#b6bfe2',
  selectionForeground: '#3760bf',
  black: '#e9e9ed',
  red: '#f52a65',
  green: '#587539',
  yellow: '#8c6c3e',
  blue: '#2e7de9',
  magenta: '#9854f1',
  cyan: '#007197',
  white: '#6172b0',
  brightBlack: '#a1a6c5',
  brightRed: '#f52a65',
  brightGreen: '#587539',
  brightYellow: '#8c6c3e',
  brightBlue: '#2e7de9',
  brightMagenta: '#9854f1',
  brightCyan: '#007197',
  brightWhite: '#3760bf',
}

// Matches the convention in MetricsChart: read the `dark` class that
// lib/theme.ts owns, and re-read it on the `sfpanel:themechange` event it
// dispatches. There is no theme context to subscribe to.
function currentTermTheme() {
  return document.documentElement.classList.contains('dark') ? DARK_THEME : LIGHT_THEME
}

// Each TerminalSession imperatively attaches its xterm instance, websocket
// ref, and search addon to its DOM container so the parent (which renders
// many sessions and reaches into the active one for search/clear/key
// forwarding) can find them by querying the DOM. This sidesteps lifting a
// dynamic list of refs to the parent.
export interface TerminalSessionElement extends HTMLElement {
  __searchAddon?: SearchAddon
  __fitAddon?: FitAddon
  __termRef?: RefObject<XTerm | null>
  __wsRef?: RefObject<WebSocket | null>
}

// The terminal's text grid on screen: where it starts and one cell's size.
interface Grid { left: number; top: number; cellW: number; cellH: number }
function gridOf(term: XTerm, container: HTMLElement): Grid | null {
  const screen = container.querySelector('.xterm-screen')
  if (!screen || !term.cols || !term.rows) return null
  const r = screen.getBoundingClientRect()
  return { left: r.left, top: r.top, cellW: r.width / term.cols, cellH: r.height / term.rows }
}

// The buffer cell under a point on screen. With clamp, a point past the edge
// takes the nearest visible cell, so a handle dragged off the grid stays on it.
function cellAt(term: XTerm, grid: Grid, x: number, y: number, clamp = false): CellPos | null {
  let col = Math.floor((x - grid.left) / grid.cellW)
  let viewRow = Math.floor((y - grid.top) / grid.cellH)
  if (clamp) {
    col = Math.max(0, Math.min(term.cols - 1, col))
    viewRow = Math.max(0, Math.min(term.rows - 1, viewRow))
  } else if (col < 0 || col >= term.cols || viewRow < 0 || viewRow >= term.rows) {
    return null
  }
  return { row: term.buffer.active.viewportY + viewRow, col }
}

// A long-press selection: its two ends in either order (a handle dragged past
// the other simply swaps sides), the word the press chose — holding on and
// dragging extends from it — and the line it is on.
interface Selection { a: CellPos; b: CellPos; word: { start: CellPos; end: CellPos }; line: TextSpan | null }

export function TerminalSession({
  sessionId,
  active,
  fontSize,
  wsPath = '/ws/terminal',
  wsParams,
}: {
  sessionId: string
  active: boolean
  fontSize: number
  /** The AI page attaches through /ws/ai/attach; the terminal keeps its default. */
  wsPath?: string
  wsParams?: Record<string, string>
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XTerm | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  // The long-press selection (null: none), shown with xterm's own highlight.
  const [sel, setSel] = useState<Selection | null>(null)
  const dismissSelection = useCallback(() => {
    termRef.current?.clearSelection()
    setSel(null)
  }, [])
  useEffect(() => {
    const term = termRef.current
    if (!sel || !term) return
    const r = rangeBetween(term.buffer.active, term.cols, sel.a, sel.b)
    term.select(r.col, r.row, r.length)
  }, [sel])
  // Scrolling moves the text out from under the handles, and typing means the
  // operator has moved on.
  useEffect(() => {
    const term = termRef.current
    if (!sel || !term) return
    const subs = [term.onScroll(dismissSelection), term.onData(dismissSelection)]
    return () => subs.forEach((s) => s.dispose())
  }, [sel, dismissSelection])
  // A session that goes to the background takes its selection with it — the
  // handles are rendered into <body> and would stay over the next session. The
  // state follows the prop during render; the highlight is xterm's, cleared in
  // an effect.
  const [wasActive, setWasActive] = useState(active)
  if (wasActive !== active) {
    setWasActive(active)
    if (!active) setSel(null)
  }
  useEffect(() => { if (!active) termRef.current?.clearSelection() }, [active])
  // A handle's knob hangs below the text, so it aims at the row above the finger.
  const dragHandle = useCallback((end: HandleEnd, point: Point) => {
    const term = termRef.current
    const grid = term && containerRef.current && gridOf(term, containerRef.current)
    if (!term || !grid) return
    const p = cellAt(term, grid, point.x, point.y - grid.cellH, true)
    if (p) setSel((prev) => prev && (end === 'start' ? { ...prev, a: p } : { ...prev, b: p }))
  }, [])
  // Where the handles and the bar go, read from the terminal's layout.
  const measureSelection = useCallback(() => {
    const term = termRef.current
    const grid = term && containerRef.current && gridOf(term, containerRef.current)
    if (!sel || !term || !grid) return null
    const buffer = term.buffer.active
    const r = rangeBetween(buffer, term.cols, sel.a, sel.b)
    const lastCell = r.col + r.length - 1
    const last = { row: r.row + Math.floor(lastCell / term.cols), col: lastCell % term.cols }
    const onScreen = (row: number) => row >= buffer.viewportY && row < buffer.viewportY + term.rows
    const below = (row: number) => grid.top + (row - buffer.viewportY + 1) * grid.cellH
    const left = onScreen(r.row) ? { x: grid.left + r.col * grid.cellW, y: below(r.row) } : null
    const right = onScreen(last.row) ? { x: grid.left + (last.col + 1) * grid.cellW, y: below(last.row) } : null
    // Each handle keeps the end it holds: dragged past the other, it takes
    // that side rather than jumping away from the finger.
    const aFirst = cellBefore(sel.a, sel.b)
    return {
      handles: { start: aFirst ? left : right, end: aFirst ? right : left },
      bar: {
        x: left && right && r.row === last.row ? (left.x + right.x) / 2 : grid.left + (term.cols * grid.cellW) / 2,
        top: grid.top + (r.row - buffer.viewportY) * grid.cellH,
        bottom: below(last.row),
      },
    }
  }, [sel])
  const { t } = useTranslation()

  useEffect(() => {
    // React StrictMode replays setup → cleanup → setup in development. The
    // effect owns this instance; a sticky initialized ref would skip the second
    // setup and leave both Terminal and AI with a disposed, empty terminal.
    if (!containerRef.current) return

    const term = new XTerm({
      cursorBlink: true,
      fontSize,
      fontFamily: '"JetBrains Mono", "Fira Code", Menlo, Monaco, "Courier New", monospace',
      theme: currentTermTheme(),
      scrollback: 10000,
      allowProposedApi: true,
    })

    const fitAddon = new FitAddon()
    const searchAddon = new SearchAddon()
    const unicode11Addon = new Unicode11Addon()
    term.loadAddon(fitAddon)
    term.loadAddon(new WebLinksAddon())
    term.loadAddon(searchAddon)
    term.loadAddon(unicode11Addon)
    term.unicode.activeVersion = '11'
    term.open(containerRef.current)
    fitAddon.fit() // immediate baseline

    // Time-debounced fit. The mobile soft keyboard fires a BURST of viewport
    // resizes across its open/close animation; fitting on each one churns the
    // PTY row count and leaves piles of blank rows (the "공백" after toggling the
    // keyboard). Instead, fit once ~140ms after the size settles, skip it when
    // the dimensions didn't actually change, and anchor to the bottom so no gap
    // shows above the prompt.
    let fitTimer = 0
    const safeFit = () => {
      clearTimeout(fitTimer)
      fitTimer = window.setTimeout(() => {
        try {
          const dims = fitAddon.proposeDimensions()
          if (dims && (dims.rows !== term.rows || dims.cols !== term.cols)) {
            const wasAtBottom = term.buffer.active.viewportY >= term.buffer.active.baseY
            fitAddon.fit()
            if (wasAtBottom) requestAnimationFrame(() => { if (termRef.current === term) term.scrollLines(term.buffer.active.length) })
          }
        } catch { /* container not laid out yet */ }
      }, 140)
    }
    // Re-fit once the monospace webfont is ready: its cell metrics differ from
    // the fallback, and fitting with fallback metrics yields a wrong row count.
    document.fonts?.ready.then(() => safeFit()).catch(() => {})
    termRef.current = term
    fitAddonRef.current = fitAddon
    searchAddonRef.current = searchAddon

    const token = api.getToken()
    if (!token) {
      term.writeln('\r\n\x1b[31m' + t('terminal.notAuthenticated') + '\x1b[0m')
      return () => { clearTimeout(fitTimer); term.dispose(); termRef.current = null }
    }

    // WS setup is async (ticket mint). connect() is re-invocable so a dropped
    // socket transparently reconnects to the SAME session_id: the server keeps
    // the PTY alive (stable session key, scrollback replay, 5-min idle grace),
    // so a transient drop (Wi-Fi blip, sleep, reverse-proxy idle timeout)
    // resumes the live session instead of leaving a dead terminal. The
    // term-level input/resize listeners are registered once and always target
    // the current socket via wsRef.
    let disposed = false
    let wsCleanup: (() => void) | null = null
    let reconnectTimer = 0
    let stableTimer = 0
    let attempts = 0
    const maxReconnectAttempts = 6

    // Copy/paste.
    //
    // Paste needs nothing: xterm registers a native `paste` listener on both
    // its textarea and its element, so the browser's own Ctrl+V already
    // delivers the clipboard through a ClipboardEvent — which, unlike
    // navigator.clipboard, works on the plain-HTTP origins this panel is
    // usually served from. Ctrl+Shift+V is bound only as the muscle-memory
    // alias, and it DOES need the async API, so it degrades to a hint.
    //
    // Copy does need a binding: xterm draws its selection into a canvas, so no
    // DOM selection exists for the browser to copy. Plain Ctrl+C must stay
    // SIGINT, hence the Shift variant every terminal emulator uses.
    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== 'keydown' || !e.ctrlKey || !e.shiftKey) return true
      const key = e.key.toLowerCase()
      if (key === 'c') {
        const selection = term.getSelection()
        if (!selection) return true
        void copyText(selection).then((ok) => {
          if (ok) term.clearSelection()
          else toast.error(t('terminal.copyFailed', { defaultValue: 'Could not copy to clipboard' }))
        })
        return false
      }
      if (key === 'v') {
        if (!window.isSecureContext || !navigator.clipboard?.readText) {
          toast.info(t('terminal.pasteUseCtrlV', { defaultValue: 'Use Ctrl+V to paste (Ctrl+Shift+V needs HTTPS)' }))
          return false
        }
        void navigator.clipboard.readText().then((text) => {
          const sock = wsRef.current
          if (text && sock && sock.readyState === WebSocket.OPEN) {
            sock.send(new TextEncoder().encode(text))
          }
        }).catch(() => {
          toast.info(t('terminal.pasteUseCtrlV', { defaultValue: 'Use Ctrl+V to paste (Ctrl+Shift+V needs HTTPS)' }))
        })
        return false
      }
      return true
    })

    let mobileModifiers: TerminalModifiers = NO_MODIFIERS
    const onModifiers = (event: Event) => {
      mobileModifiers = (event as CustomEvent<TerminalModifiers>).detail
    }
    window.addEventListener(MODIFIERS_EVENT, onModifiers)
    const onDataDisposable = term.onData((data) => {
      const sock = wsRef.current
      if (sock && sock.readyState === WebSocket.OPEN) {
        sock.send(new TextEncoder().encode(terminalKey(data, mobileModifiers)))
        mobileModifiers = NO_MODIFIERS
        window.dispatchEvent(new Event(MODIFIERS_CONSUMED_EVENT))
      }
    })
    const onResizeDisposable = term.onResize(({ cols, rows }) => {
      const sock = wsRef.current
      if (sock && sock.readyState === WebSocket.OPEN) {
        sock.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    })

    const connect = async () => {
      const wsUrl = await api.buildWsUrl(wsPath, wsParams ?? { session_id: sessionId })
      if (disposed) return
      const ws = new WebSocket(wsUrl)
      wsRef.current = ws
      ws.binaryType = 'arraybuffer'

      ws.onopen = () => {
        term.focus()
        const { cols, rows } = term
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
        // Only clear the backoff once the socket has proven stable, so a server
        // that accepts then instantly drops can't spin in a tight reconnect loop.
        stableTimer = window.setTimeout(() => { attempts = 0 }, 3000)
      }

      ws.onmessage = (event) => {
        if (event.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(event.data))
        } else {
          term.write(event.data)
        }
      }

      ws.onerror = () => {
        // Swallow: onclose fires next and drives the reconnect/disconnect notice.
      }

      ws.onclose = () => {
        clearTimeout(stableTimer)
        if (disposed) return
        if (attempts < maxReconnectAttempts) {
          const delay = Math.min(1000 * 2 ** attempts, 10000)
          attempts += 1
          term.writeln('\r\n\x1b[33m' + t('terminal.reconnecting') + '\x1b[0m')
          reconnectTimer = window.setTimeout(() => { void connect() }, delay)
        } else {
          term.writeln('\r\n\x1b[31m' + t('terminal.disconnected') + '\x1b[0m')
        }
      }
    }

    void connect()

    wsCleanup = () => {
      clearTimeout(reconnectTimer)
      clearTimeout(stableTimer)
      onDataDisposable.dispose()
      window.removeEventListener(MODIFIERS_EVENT, onModifiers)
      onResizeDisposable.dispose()
      const sock = wsRef.current
      if (sock) {
        sock.onclose = null // intentional teardown — must not schedule a reconnect
        sock.close()
      }
    }

    // ResizeObserver fires AFTER the container's box actually changes (keyboard
    // open/close via --app-h, orientation, tab switch), so the fit measures the
    // real post-reflow height — unlike a visualViewport 'resize' that can fire
    // before the CSS height reflows. window/visualViewport stay as a fallback
    // for browsers that miss some container resizes.
    const container = containerRef.current
    const ro = new ResizeObserver(() => safeFit())
    if (container) ro.observe(container)
    const handleResize = () => safeFit()
    window.addEventListener('resize', handleResize)
    window.visualViewport?.addEventListener('resize', handleResize)

    // xterm v6's viewport isn't natively touch-scrollable; the shared helper
    // translates a vertical touch-drag into term.scrollLines so mobile can
    // reach the scrollback (see lib/xtermTouchScroll).
    const detachTouch = container ? attachXtermTouchScroll(container, term) : () => {}
    // A long press is how a phone selects text, and xterm draws into a canvas
    // with nothing to select. So a long press selects the word under the
    // finger in place, with xterm's own highlight, and raises a bar to copy it
    // or its line, or to open the whole output as text; pressed on an empty
    // row it opens that text view straight away.
    const detachLongPress = container
      ? attachLongPress(container, (x, y) => {
          const grid = gridOf(term, container)
          const at = grid && cellAt(term, grid, x, y)
          const buffer = term.buffer.active
          const word = at ? wordAt(buffer, term.cols, at.row, at.col) : null
          const line = at ? lineAt(buffer, term.cols, at.row) : null
          const span = word ?? line
          if (!span) {
            window.dispatchEvent(new Event(SELECT_TEXT_EVENT))
            return
          }
          const start = { row: span.row, col: span.col }
          const end = spanEnd(span, term.cols)
          setSel({ a: start, b: end, word: { start, end }, line })
        }, {
          // Holding on and dragging extends the selection from the word the
          // press chose, as a phone's own text selection does.
          onDrag: (x, y) => {
            const grid = gridOf(term, container)
            const p = grid && cellAt(term, grid, x, y, true)
            if (!p) return
            setSel((prev) => {
              if (!prev) return prev
              const { start, end } = prev.word
              if (!cellBefore(start, p)) return { ...prev, a: p, b: end }
              return { ...prev, a: start, b: cellBefore(p, end) ? end : p }
            })
          },
        })
      : () => {}

    // Re-fit when the terminal gains focus (user tapped to type). This
    // self-corrects the size when the page loaded with the keyboard already up
    // and the viewport-change events that normally drive the fit never fired —
    // the second fit lands after the keyboard's open animation settles.
    const onFocusIn = () => { safeFit(); window.setTimeout(safeFit, 400) }
    container?.addEventListener('focusin', onFocusIn)

    return () => {
      disposed = true
      clearTimeout(fitTimer)
      ro.disconnect()
      window.removeEventListener('resize', handleResize)
      window.visualViewport?.removeEventListener('resize', handleResize)
      detachTouch()
      detachLongPress()
      container?.removeEventListener('focusin', onFocusIn)
      wsCleanup?.()
      term.dispose()
      termRef.current = null
      wsRef.current = null
    }
  }, [sessionId]) // eslint-disable-line react-hooks/exhaustive-deps

  // Update font size dynamically
  useEffect(() => {
    if (termRef.current) {
      termRef.current.options.fontSize = fontSize
      fitAddonRef.current?.fit()
    }
  }, [fontSize])

  // Follow the app theme. 'sfpanel:themechange' covers an explicit toggle; the
  // matchMedia listener covers an OS flip while the preference is 'system',
  // which lib/theme.ts also re-dispatches — subscribing to both is harmless
  // and keeps this working if that ever changes.
  useEffect(() => {
    const applyTheme = () => {
      if (termRef.current) termRef.current.options.theme = currentTermTheme()
    }
    applyTheme()
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    window.addEventListener('sfpanel:themechange', applyTheme)
    media.addEventListener('change', applyTheme)
    return () => {
      window.removeEventListener('sfpanel:themechange', applyTheme)
      media.removeEventListener('change', applyTheme)
    }
  }, [])

  // Re-fit and focus when tab becomes active
  useEffect(() => {
    if (active && fitAddonRef.current && termRef.current) {
      setTimeout(() => {
        fitAddonRef.current?.fit()
        termRef.current?.focus()
      }, 50)
    }
  }, [active])

  // Expose search addon and ws for parent access
  useEffect(() => {
    const el = containerRef.current as TerminalSessionElement | null
    if (!el) return
    if (searchAddonRef.current) el.__searchAddon = searchAddonRef.current
    if (fitAddonRef.current) el.__fitAddon = fitAddonRef.current
    el.__wsRef = wsRef
    el.__termRef = termRef
  }, [])

  // data-terminal-session is a stable hook for the parent's active-session
  // lookup (search/clear/key forwarding). Querying by Tailwind class substrings
  // broke silently when a className was reordered during the UI-polish churn;
  // this attribute is decoupled from styling.
  const copyAndDismiss = async (text: string) => {
    const ok = text !== '' && await copyText(text)
    dismissSelection()
    if (ok) toast.success(t('terminal.copyBar.copied', { text: text.length > 40 ? `${text.slice(0, 39)}…` : text }))
    else toast.error(t('terminal.copyFailed', { defaultValue: 'Could not copy to clipboard' }))
  }

  return (
    <>
      <div
        ref={containerRef}
        data-terminal-session={active ? 'active' : 'inactive'}
        className={cn(
          // touch-none: xterm v6's viewport isn't natively touch-scrollable, so we
          // drive scrollback from a touch-drag handler (see the effect above) —
          // disable the browser's own touch gestures here so they can't preempt it.
          'w-full h-full touch-none',
          active ? 'block' : 'hidden'
        )}
      />
      {sel && (
        <TerminalSelection
          measure={measureSelection}
          canCopyLine={sel.line !== null}
          onHandleDrag={dragHandle}
          onCopy={() => { void copyAndDismiss(termRef.current?.getSelection() ?? '') }}
          onCopyLine={() => { if (sel.line) void copyAndDismiss(sel.line.text) }}
          onViewAll={() => { dismissSelection(); window.dispatchEvent(new Event(SELECT_TEXT_EVENT)) }}
          onDismiss={dismissSelection}
        />
      )}
    </>
  )
}
