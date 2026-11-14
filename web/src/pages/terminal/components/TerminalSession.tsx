import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { SearchAddon } from '@xterm/addon-search'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import '@xterm/xterm/css/xterm.css'
import { api } from '@/lib/api'
import { attachXtermTouchScroll, scrollTerminalLines, type TmuxTouch } from '@/lib/xtermTouchScroll'
import { openHistoryView, type HistoryView } from '@/lib/terminalHistory'
import { attachLongPress } from '@/lib/longPress'
import { openTerminalLink } from '@/lib/terminalLinks'
import { SELECT_TEXT_EVENT } from '@/lib/terminalText'
import { DEFAULT_FONT_SIZE, FONT_SIZE_EVENT, GRID_EVENT, attachPinch } from '@/lib/terminalFont'
import { focusesUnasked } from '@/lib/terminalLayout'
import { SEARCH_RESULTS_EVENT, searchOptions, type SearchDirection, type SearchOutcome } from '@/lib/terminalSearch'
import { decodeOsc52 } from '@/lib/osc52'
import { cellBefore, lineAt, rangeBetween, spanEnd, wordAt, type CellPos, type TextSpan } from '@/lib/terminalWord'
import { TerminalSelection, type HandleEnd, type Point } from './TerminalSelection'
import { TerminalContextMenu, type TerminalMenuAt } from './TerminalContextMenu'
import { cn, copyText } from '@/lib/utils'
import { toast } from 'sonner'
import { ArrowDown } from 'lucide-react'
import { IS_MAC, MODIFIERS_CONSUMED_EVENT, MODIFIERS_EVENT, NO_MODIFIERS, terminalKey, type TerminalModifiers } from '@/lib/terminalKeys'

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
  /** What the Android app's own search dialog calls (MainActivity.find): the
   *  addon itself for a temporary shell, the session's search for a tmux one. */
  __searchAddon?: Pick<SearchAddon, 'findNext' | 'findPrevious'>
  __fitAddon?: FitAddon
  __termRef?: RefObject<XTerm | null>
  __wsRef?: RefObject<WebSocket | null>
  /** back to the latest output: out of the history view and of tmux's copy mode */
  __latest?: () => void
  /** finds the query in the session's history — a tmux session's through the
   *  history view, opened over the live screen — or on its screen alone */
  __search?: (query: string, direction: SearchDirection) => Promise<SearchOutcome>
  /** the search is over: its marks go, and the view it opened */
  __searchEnd?: () => void
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

// A copied text as a toast names it.
const clip = (text: string) => (text.length > 40 ? `${text.slice(0, 39)}…` : text)

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
  tmux = false,
  touchScroll = false,
  touchHistory = false,
  attachLatest = false,
}: {
  sessionId: string
  active: boolean
  fontSize: number
  /** The AI page attaches through /ws/ai/attach; the terminal keeps its default. */
  wsPath?: string
  wsParams?: Record<string, string>
  /** A tmux session: its copies arrive as OSC 52 (see the handler below). */
  tmux?: boolean
  /** The panel binds touch scrolling for tmux (GET /ai/tools touch_scroll): a
   *  finger's line is then one line of history, or one wheel report to the
   *  program in the pane. */
  touchScroll?: boolean
  /** The panel serves the history view's copy (GET /ai/tools touch_history). */
  touchHistory?: boolean
  /** The attach takes the latest frame (GET /ai/tools attach_latest): out of
   *  tmux's copy mode. A panel without it would type the frame into the session. */
  attachLatest?: boolean
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XTerm | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  // Read by the touch scrolling at each line: the tools bundle that says so can
  // arrive after the session has connected.
  const touchScrollRef = useRef(touchScroll)
  useEffect(() => { touchScrollRef.current = touchScroll }, [touchScroll])
  const touchHistoryRef = useRef(touchHistory)
  useEffect(() => { touchHistoryRef.current = touchHistory }, [touchHistory])
  const attachLatestRef = useRef(attachLatest)
  useEffect(() => { attachLatestRef.current = attachLatest }, [attachLatest])
  // Only the session on screen says what its grid is (GRID_EVENT).
  const activeRef = useRef(active)
  useEffect(() => { activeRef.current = active }, [active])
  // Two fingers are changing the font size (lib/terminalFont): what they have
  // reached, shown until they lift.
  const [pinch, setPinch] = useState<{ size: number; cols: number } | null>(null)
  // The history view is open (lib/terminalHistory): the ↓ button shows.
  const [historyOpen, setHistoryOpen] = useState(false)
  // The pane was in tmux's copy mode when last asked (the scrollback's
  // answer): scrolled back where xterm has nothing to scroll, so the ↓ button
  // shows for it too.
  const [paneCopyMode, setPaneCopyMode] = useState(false)
  // xterm's own buffer is scrolled back (a temporary shell's history is there).
  const [scrolledUp, setScrolledUp] = useState(false)
  const latestRef = useRef<() => void>(() => {})
  const fitAddonRef = useRef<FitAddon | null>(null)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  // The long-press selection (null: none), shown with xterm's own highlight.
  const [sel, setSel] = useState<Selection | null>(null)
  // The right-click menu, kept mounted once opened so it closes as a menu does.
  const [menu, setMenu] = useState<TerminalMenuAt | null>(null)
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
    if (!active) {
      setSel(null)
      setMenu(null)
    }
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
      // With tmux's mouse on, a drag is tmux's, and its copy arrives as OSC 52.
      // Shift+drag (xterm's default elsewhere) and Option+drag on a Mac select
      // in the browser instead, to copy with Ctrl+Shift+C / ⌘C or the menu.
      macOptionClickForcesSelection: true,
    })

    const fitAddon = new FitAddon()
    const searchAddon = new SearchAddon()
    const unicode11Addon = new Unicode11Addon()
    term.loadAddon(fitAddon)
    term.loadAddon(new WebLinksAddon(openTerminalLink))
    term.loadAddon(searchAddon)
    term.loadAddon(unicode11Addon)
    term.unicode.activeVersion = '11'
    term.open(containerRef.current)
    fitAddon.fit() // immediate baseline
    const announceGrid = () => {
      if (activeRef.current) window.dispatchEvent(new CustomEvent(GRID_EVENT, { detail: { cols: term.cols, rows: term.rows } }))
    }
    announceGrid()
    const announceResults = (index: number, count: number) => {
      if (activeRef.current) window.dispatchEvent(new CustomEvent(SEARCH_RESULTS_EVENT, { detail: { index, count } }))
    }
    const liveScroll = term.onScroll(() => {
      const b = term.buffer.active
      setScrolledUp(b.type === 'normal' && b.viewportY < b.baseY)
    })
    const liveResults = searchAddon.onDidChangeResults(({ resultIndex, resultCount }) => announceResults(resultIndex, resultCount))

    // Time-debounced fit. The mobile soft keyboard fires a BURST of viewport
    // resizes across its open/close animation; fitting on each one churns the
    // PTY row count and leaves piles of blank rows (the "공백" after toggling the
    // keyboard). Instead, fit once ~140ms after the size settles, skip it when
    // the dimensions didn't actually change, and anchor to the bottom so no gap
    // shows above the prompt.
    // xterm's own scrollLines. term.scrollLines is replaced further down to
    // hear a move to the latest output from outside this component; the fit's
    // anchoring is not one, and must not take a pane out of tmux's copy mode
    // every time the keyboard opens.
    const scrollTermLines = term.scrollLines.bind(term)
    let fitTimer = 0
    const safeFit = () => {
      clearTimeout(fitTimer)
      fitTimer = window.setTimeout(() => {
        try {
          const dims = fitAddon.proposeDimensions()
          if (dims && (dims.rows !== term.rows || dims.cols !== term.cols)) {
            const wasAtBottom = term.buffer.active.viewportY >= term.buffer.active.baseY
            fitAddon.fit()
            if (wasAtBottom) requestAnimationFrame(() => { if (termRef.current === term) scrollTermLines(term.buffer.active.length) })
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
    // usually served from. Ctrl+Shift+V is the same: the browser pastes on it
    // (paste as plain text) and xterm takes that paste like any other. It is
    // bound only so xterm does not also send the key as ^V; reading the
    // clipboard here as well sent the text a second time, raw.
    //
    // Copy does need a binding: xterm draws its selection into a canvas, so no
    // DOM selection exists for the browser to copy. Plain Ctrl+C must stay
    // SIGINT, hence the Shift variant every terminal emulator uses.
    term.attachCustomKeyEventHandler((e) => {
      const key = e.key.toLowerCase()
      // The page's search (Terminal.tsx hears it on the window): Ctrl+Shift+F
      // as in a terminal emulator, ⌘F on a Mac. xterm must not take the key,
      // or it never gets there. Plain Ctrl+F stays the program's — a page
      // down in vim and less — as it always reached it from here.
      if (key === 'f' && (e.metaKey || (e.ctrlKey && e.shiftKey))) return false
      if (e.type !== 'keydown' || !e.ctrlKey || !e.shiftKey) return true
      if (key === 'c') {
        const selection = term.getSelection()
        if (!selection) return true
        void copyText(selection).then((ok) => {
          if (ok) term.clearSelection()
          else toast.error(t('terminal.copyFailed', { defaultValue: 'Could not copy to clipboard' }))
        })
        return false
      }
      if (key === 'v') return false
      return true
    })

    // tmux's own copy — a mouse drag, a double or triple click — reaches the
    // browser as OSC 52, and tmux drops its highlight on release, so this is
    // the only place the text can land. Only a tmux session listens: there
    // set-clipboard is external (tmuxOptions), so no program in the pane can
    // send one. A temporary shell has nothing in front of it, and any output —
    // a `cat` of a hostile file — could set the operator's clipboard. Without
    // a click to stand on the browser may refuse the copy (Safari, or a
    // plain-HTTP panel past the click's grace); the toast then carries one.
    if (tmux) {
      term.parser.registerOscHandler(52, (data) => {
        const text = decodeOsc52(data)
        if (!text) return true
        void copyText(text).then((ok) => {
          if (ok) {
            toast.success(t('terminal.copyBar.copied', { text: clip(text) }))
            return
          }
          toast.error(t('terminal.copyBlocked'), {
            action: {
              label: t('terminal.menu.copy'),
              onClick: () => {
                void copyText(text).then((again) => {
                  if (again) toast.success(t('terminal.copyBar.copied', { text: clip(text) }))
                  else toast.error(t('terminal.copyFailed', { defaultValue: 'Could not copy to clipboard' }))
                })
              },
            },
          })
        })
        return true
      })
    }

    // History view (lib/terminalHistory): a phone scrolls a tmux session's
    // history in the page, a pixel at a time, over a copy of it — through tmux
    // every line was a round trip and a redraw. A drag back into the history
    // opens it, once the panel serves the copy and the pane is as the page
    // shows it: not in the alternate screen (a program there keeps its history
    // to itself), not scrolled back in copy mode, and the page's size. Anything
    // else keeps scrolling line by line through tmux. Back at the bottom, a
    // tap, any input, a resize, a lost connection or the ↓ button returns to
    // the live screen.
    let view: HistoryView | null = null
    // Where the current gesture goes: the view, lines through tmux, or held
    // while the copy loads — both its pixels and the lines the touch helper
    // counts from them, so whichever it turns out to be gets the gesture
    // exactly: the view the finger's pixels, tmux its lines. A gesture that
    // starts while a copy is on its way joins it.
    let gesture: 'idle' | 'view' | 'lines' | 'forward' | 'loading' = 'idle'
    let heldPx = 0
    let heldLines = 0
    let load = 0
    let loading = false
    // What the last copy said, so a pane that scrolls through tmux does not
    // wait a round trip at every gesture: 'tmux' sends the lines at once and
    // asks again behind them.
    let pane: 'history' | 'tmux' | null = null
    // The search in progress, for a resize to restore: it closes the view the
    // copy was laid out in — the keyboard a phone raises for the search box
    // does, right after the first match showed. research is the search
    // itself, defined further down with the rest of it.
    let searching: string | null = null
    let researchTimer = 0
    let research: (query: string) => void = () => {}
    // The view is a search's: it stays open at the bottom of the history,
    // where a gesture's would close, until the search ends.
    let searchPinned = false
    const closeView = () => {
      // A copy still on its way would open over whatever came after.
      load++
      loading = false
      heldPx = 0
      heldLines = 0
      if (gesture === 'loading') gesture = 'idle'
      searchPinned = false
      if (!view) return
      view.close()
      view = null
      setHistoryOpen(false)
    }
    // The socket's own send, for this component's senders. What everyone else
    // sends through __wsRef — the key bar, the Android app's keys — goes through
    // the socket's replaced send, which leaves the history view first: input is
    // for the live screen. onData stays out of it, as it carries the terminal's
    // answers to tmux's queries; the keyboard, a paste and touch scrolling
    // close or keep the view themselves.
    const sendDirect = new WeakMap<WebSocket, WebSocket['send']>()
    const send = (sock: WebSocket, data: Parameters<WebSocket['send']>[0]) => (sendDirect.get(sock) ?? sock.send.bind(sock))(data)

    let mobileModifiers: TerminalModifiers = NO_MODIFIERS
    const onModifiers = (event: Event) => {
      mobileModifiers = (event as CustomEvent<TerminalModifiers>).detail
    }
    window.addEventListener(MODIFIERS_EVENT, onModifiers)
    const onDataDisposable = term.onData((data) => {
      const sock = wsRef.current
      if (sock && sock.readyState === WebSocket.OPEN) {
        send(sock, new TextEncoder().encode(terminalKey(data, mobileModifiers)))
        mobileModifiers = NO_MODIFIERS
        window.dispatchEvent(new Event(MODIFIERS_CONSUMED_EVENT))
      }
    })
    const onResizeDisposable = term.onResize(({ cols, rows }) => {
      announceGrid()
      // The copy was laid out for the old size. A search's view comes back
      // once the size has settled; one the operator had already left (a tap,
      // to type — which is what raised the keyboard) does not.
      const restore = searchPinned || researchTimer !== 0
      closeView()
      clearTimeout(researchTimer)
      researchTimer = 0
      if (restore && searching !== null) {
        researchTimer = window.setTimeout(() => {
          researchTimer = 0
          if (searching !== null) research(searching)
        }, 250)
      }
      const sock = wsRef.current
      if (sock && sock.readyState === WebSocket.OPEN) {
        send(sock, JSON.stringify({ type: 'resize', cols, rows }))
      }
    })

    const connect = async () => {
      const wsUrl = await api.buildWsUrl(wsPath, wsParams ?? { session_id: sessionId })
      if (disposed) return
      const ws = new WebSocket(wsUrl)
      wsRef.current = ws
      ws.binaryType = 'arraybuffer'
      const direct = ws.send.bind(ws)
      sendDirect.set(ws, direct)
      ws.send = (data) => {
        closeView()
        direct(data)
      }

      ws.onopen = () => {
        if (focusesUnasked()) term.focus()
        const { cols, rows } = term
        send(ws, JSON.stringify({ type: 'resize', cols, rows }))
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
        // The notice goes to the live screen, which the view would cover.
        closeView()
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
          // Not over the history view: the press would select from the live
          // screen underneath, and an armed press takes the rest of the
          // finger's drag from the scrolling.
          enabled: () => !view,
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
    // xterm v6's viewport isn't natively touch-scrollable; the shared helper
    // scrolls a line per line of finger travel and carries a flick on (see
    // lib/xtermTouchScroll), behind the long press, which stops a fired
    // press's drag first. A tmux session's history goes to the history view
    // (pixels, below); what it does not take goes to tmux as lines — the
    // panel's touch wheel pairs once it says it binds them. The pairs never pass
    // through xterm, so nothing else clears a selection they move the text from
    // under.
    const tmuxTouch: TmuxTouch | undefined = tmux
      ? {
          bound: () => touchScrollRef.current,
          send: (data) => {
            const sock = wsRef.current
            if (sock && sock.readyState === WebSocket.OPEN) send(sock, new TextEncoder().encode(data))
          },
        }
      : undefined
    const scrollLines = (lines: number, at?: Point) => {
      if (lines) dismissSelection()
      scrollTerminalLines(term, container?.querySelector('.xterm-screen') ?? null, lines, at, tmuxTouch)
    }
    const routeLines = (lines: number, at?: Point) => {
      if (gesture === 'loading') heldLines += lines
      else scrollLines(lines, at)
    }
    const openView = (text: string) => {
      if (!container) return
      dismissSelection()
      view = openHistoryView(container, term, text, () => {
        closeView()
        term.focus()
      }, announceResults)
      setHistoryOpen(true)
    }
    // Asks for the copy. What it answers decides the gesture still loading,
    // and the pane's kind for the gestures after it.
    const fetchCopy = () => {
      const mine = ++load
      loading = true
      const asked = { cols: term.cols, rows: term.rows }
      const asLines = () => {
        const lines = heldLines
        heldPx = 0
        heldLines = 0
        if (gesture === 'loading') gesture = 'lines'
        scrollLines(lines)
      }
      api.getAISessionScrollback(sessionId).then((copy) => {
        if (disposed || mine !== load) return
        loading = false
        setPaneCopyMode(copy.copy_mode)
        const usable = !copy.alternate && !copy.copy_mode && copy.cols === asked.cols && copy.rows === asked.rows
          && term.cols === asked.cols && term.rows === asked.rows
        pane = usable ? 'history' : 'tmux'
        if (!usable) {
          asLines()
          return
        }
        if (heldPx >= 0) {
          // Nothing back to show: the gesture went to tmux, or turned back.
          heldPx = 0
          heldLines = 0
          return
        }
        const still = gesture === 'loading'
        openView(copy.text ?? '')
        view?.move(heldPx)
        heldPx = 0
        heldLines = 0
        if (still) gesture = 'view'
        else if (view?.offset() === 0 && !searchPinned) closeView()
      }).catch(() => {
        if (disposed || mine !== load) return
        loading = false
        pane = 'tmux'
        // No answer, so no telling: the lines that follow may put the pane in
        // copy mode, and the ↓ button costs nothing where they did not.
        setPaneCopyMode(true)
        asLines()
      })
    }
    const pixels = (px: number, _at: Point, flinging: boolean) => {
      if (gesture === 'view') {
        // A fling that brings the view back to the bottom is back on the live
        // screen at once; the rest of it has nowhere to go.
        if (view && view.move(px) === 0 && flinging && px > 0 && !searchPinned) closeView()
        return true
      }
      if (gesture === 'loading') {
        heldPx += px
        return false
      }
      if (gesture === 'lines') return false
      if (view) {
        gesture = 'view'
        view.move(px)
        return true
      }
      if (!tmux || !touchScrollRef.current || !touchHistoryRef.current) {
        gesture = 'lines'
        return false
      }
      // Towards newer output on the live screen there is nothing to show, but
      // a program in the alternate screen may be scrolled back: lines, until
      // the finger turns back into the history.
      if (px >= 0) {
        gesture = 'forward'
        return false
      }
      if (pane === 'tmux') {
        gesture = 'lines'
        // Asked again behind the lines, for the next gesture; this one is
        // decided, and holds nothing for the answer.
        if (!loading) fetchCopy()
        return false
      }
      gesture = 'loading'
      heldPx += px
      if (!loading) fetchCopy()
      return false
    }
    const startGesture = () => {
      gesture = loading && pane !== 'tmux' ? 'loading' : 'idle'
    }
    // Back at the bottom when the finger lifts, or a fling comes to rest, is
    // back on the live screen.
    const settleGesture = () => {
      if (gesture !== 'loading' && view?.offset() === 0 && !searchPinned) closeView()
      gesture = 'idle'
    }
    const detachTouch = container
      ? attachXtermTouchScroll(container, term, {
          scroll: routeLines,
          pixels: (px, at, flinging) => {
            // A forward start that turns back into the history decides again.
            if (gesture === 'forward' && px < 0) gesture = 'idle'
            return pixels(px, at, flinging)
          },
          onStart: startGesture,
          onSettle: settleGesture,
        })
      : () => {}
    // A pinch previews its size by scaling what is drawn, and the size is
    // applied when the fingers lift — once: each size is a resize, which tmux
    // and the program in it redraw for.
    const detachPinch = container
      ? attachPinch(container, {
          size: () => term.options.fontSize ?? DEFAULT_FONT_SIZE,
          onPreview: (size) => {
            const from = term.options.fontSize ?? DEFAULT_FONT_SIZE
            if (term.element) {
              term.element.style.transformOrigin = '0 0'
              term.element.style.transform = size === from ? '' : `scale(${size / from})`
            }
            dismissSelection()
            setPinch({ size, cols: Math.max(1, Math.floor(term.cols * from / size)) })
          },
          onEnd: (size) => {
            if (term.element) term.element.style.transform = ''
            setPinch(null)
            if (size !== null) window.dispatchEvent(new CustomEvent(FONT_SIZE_EVENT, { detail: size }))
          },
        })
      : () => {}
    // Search. xterm holds one screen of a tmux session — tmux draws into the
    // alternate buffer — so its own search found only what was showing. The
    // history is searched in the history view's copy instead, opened over the
    // live screen for it. Where there is no copy (a program in the alternate
    // screen keeps its history to itself; a panel without the route) the
    // screen is all there is, and the outcome says so.
    let askedOnScreen = ''
    const findOnScreen = (query: string, direction: SearchDirection) => {
      // A new query starts on the newest match (see HistoryView.search).
      if (query !== askedOnScreen) {
        askedOnScreen = query
        term.clearSelection()
        direction = 'older'
      }
      return direction === 'newer' ? searchAddon.findNext(query, searchOptions()) : searchAddon.findPrevious(query, searchOptions())
    }
    const copyForSearch = async (): Promise<void> => {
      const mine = ++load // a gesture's copy still on its way is dropped
      loading = false
      heldPx = 0
      heldLines = 0
      if (gesture === 'loading') gesture = 'idle'
      const later = (ms: number) => new Promise((done) => setTimeout(done, ms))
      for (let attempt = 0; attempt < 3; attempt++) {
        let copy: Awaited<ReturnType<typeof api.getAISessionScrollback>>
        try { copy = await api.getAISessionScrollback(sessionId) } catch { return }
        if (disposed || mine !== load) return
        const sock = wsRef.current
        setPaneCopyMode(copy.copy_mode)
        const fits = copy.cols === term.cols && copy.rows === term.rows
        if (!copy.alternate && !copy.copy_mode && fits) {
          pane = 'history'
          openView(copy.text ?? '')
          return
        }
        // Scrolled back through tmux: the search takes the view to its
        // matches, so the pane's own place is given up for the copy.
        if (copy.copy_mode && attachLatestRef.current && sock && sock.readyState === WebSocket.OPEN) send(sock, JSON.stringify({ type: 'latest' }))
        // The alternate screen does not pass; a pane still taking the page's
        // new size, or leaving copy mode, does in a moment.
        else if (copy.alternate || fits) break
        if (attempt === 2) break
        await later(200)
        if (disposed || mine !== load) return
      }
      pane = 'tmux'
    }
    let copying: Promise<void> | null = null
    const search = async (query: string, direction: SearchDirection): Promise<SearchOutcome> => {
      searching = query
      // A temporary shell's history is xterm's own, until a program takes the alternate screen.
      if (!tmux) return { found: findOnScreen(query, direction), scope: term.buffer.active.type === 'alternate' ? 'screen' : 'history' }
      if (!view && touchHistoryRef.current) {
        copying ??= copyForSearch().finally(() => { copying = null })
        await copying
      }
      if (disposed) return { found: false, scope: 'screen' }
      if (!view) return { found: findOnScreen(query, direction), scope: 'screen' }
      searchPinned = true
      searchAddon.clearDecorations()
      return { found: await view.search(query, direction), scope: 'history' }
    }
    research = (query) => { void search(query, 'older') }
    // The Android app's search dialog calls the search addon on this element
    // and reads true or false at once. For a tmux session that addon sees one
    // screen, so the app gets the session's search under the same two names:
    // true at once, and a miss — known only when the copy has come — said here.
    const fromApp = (direction: SearchDirection) => (query: string) => {
      void search(query, direction).then((outcome) => { if (!outcome.found && !disposed) toast.info(t('terminal.searchNone')) })
      return true
    }
    const searchEnd = () => {
      askedOnScreen = ''
      searching = null
      clearTimeout(researchTimer)
      researchTimer = 0
      searchAddon.clearDecorations()
      term.clearSelection()
      if (searchPinned) closeView()
    }
    if (container) {
      (container as TerminalSessionElement).__search = search
      if (tmux) (container as TerminalSessionElement).__searchAddon = { findNext: fromApp('newer'), findPrevious: fromApp('older') }
      ;(container as TerminalSessionElement).__searchEnd = searchEnd
    }
    // Typing is for the live screen: the textarea's own events, because xterm's
    // onData also carries the terminal's answers to tmux's queries. In the
    // capture phase, as xterm listens: it stops a key it handles there, and the
    // textarea's bubble-phase listeners then never hear it.
    const textarea = term.textarea
    const onType = () => closeView()
    const typing = ['keydown', 'beforeinput', 'paste', 'compositionstart'] as const
    typing.forEach((type) => textarea?.addEventListener(type, onType, true))
    // Back to the latest output: out of the history view, out of tmux's copy
    // mode — where a pane scrolled back through tmux is, and xterm has nothing
    // to move — and to the end of xterm's own buffer for a temporary shell.
    // The frame goes only to a panel that takes it (GET /ai/tools
    // attach_latest); an older one would type it into the session.
    const toLatest = () => {
      closeView()
      const sock = wsRef.current
      if (tmux && attachLatestRef.current && sock && sock.readyState === WebSocket.OPEN) send(sock, JSON.stringify({ type: 'latest' }))
      setPaneCopyMode(false)
      scrollTermLines(term.buffer.active.length)
    }
    latestRef.current = toLatest
    if (container) (container as TerminalSessionElement).__latest = toLatest
    // The Android app's own "맨 아래로" calls scrollLines on this terminal with
    // the whole buffer's length; any move towards the latest output leaves the
    // history view.
    term.scrollLines = (amount: number) => {
      if (amount >= term.buffer.active.length) {
        toLatest()
        return
      }
      if (amount > 0) closeView()
      scrollTermLines(amount)
    }
    // A paste — the menu's, the Android app's composed prompt — reaches the
    // session through term.paste with no event on the textarea.
    const pasteDirect = term.paste.bind(term)
    term.paste = (data: string) => {
      closeView()
      pasteDirect(data)
    }

    // A right click opens the terminal's own menu. The button itself is kept
    // from xterm: with tmux's mouse on it would be reported, and tmux answers a
    // right click with its pane menu — Kill and Respawn one stray click away —
    // under the browser's. Shift+right click keeps the browser's menu, the way
    // to paste from a menu on a plain-HTTP panel. A touch's contextmenu is the
    // long press's (lib/longPress).
    const keepRightButton = (e: MouseEvent) => {
      if (e.button === 2 || e.buttons === 2) e.stopPropagation()
    }
    const onContextMenu = (e: MouseEvent) => {
      const pointer = (e as PointerEvent).pointerType
      if ((pointer && pointer !== 'mouse') || e.shiftKey) return
      e.preventDefault()
      setMenu({ x: e.clientX, y: e.clientY, canCopy: term.hasSelection(), open: true })
    }
    const rightButtonEvents = ['mousedown', 'mouseup', 'mousemove'] as const
    rightButtonEvents.forEach((type) => container?.addEventListener(type, keepRightButton, true))
    container?.addEventListener('contextmenu', onContextMenu)

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
      typing.forEach((type) => textarea?.removeEventListener(type, onType, true))
      closeView()
      if (container) {
        delete (container as TerminalSessionElement).__latest
        delete (container as TerminalSessionElement).__search
        delete (container as TerminalSessionElement).__searchAddon
        delete (container as TerminalSessionElement).__searchEnd
      }
      detachLongPress()
      detachPinch()
      clearTimeout(researchTimer)
      liveResults.dispose()
      liveScroll.dispose()
      rightButtonEvents.forEach((type) => container?.removeEventListener(type, keepRightButton, true))
      container?.removeEventListener('contextmenu', onContextMenu)
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
        const term = termRef.current
        if (focusesUnasked()) term?.focus()
        if (term) window.dispatchEvent(new CustomEvent(GRID_EVENT, { detail: { cols: term.cols, rows: term.rows } }))
      }, 50)
    }
  }, [active])

  // Expose search addon and ws for parent access
  useEffect(() => {
    const el = containerRef.current as TerminalSessionElement | null
    if (!el) return
    // A tmux session's is its own search, set where that is defined.
    if (!tmux && searchAddonRef.current) el.__searchAddon = searchAddonRef.current
    if (fitAddonRef.current) el.__fitAddon = fitAddonRef.current
    el.__wsRef = wsRef
    el.__termRef = termRef
  }, [tmux])

  // data-terminal-session is a stable hook for the parent's active-session
  // lookup (search/clear/key forwarding). Querying by Tailwind class substrings
  // broke silently when a className was reordered during the UI-polish churn;
  // this attribute is decoupled from styling.
  const copyAndDismiss = async (text: string) => {
    const ok = text !== '' && await copyText(text)
    dismissSelection()
    if (ok) toast.success(t('terminal.copyBar.copied', { text: clip(text) }))
    else toast.error(t('terminal.copyFailed', { defaultValue: 'Could not copy to clipboard' }))
  }
  const copyFromMenu = () => {
    const term = termRef.current
    const text = term?.getSelection() ?? ''
    if (!term || !text) return
    void copyText(text).then((ok) => {
      if (!ok) {
        toast.error(t('terminal.copyFailed', { defaultValue: 'Could not copy to clipboard' }))
        return
      }
      term.clearSelection()
      toast.success(t('terminal.copyBar.copied', { text: clip(text) }))
    })
  }
  // A menu click is not a paste, so the menu reads the clipboard itself: that
  // takes HTTPS and the browser's leave. term.paste is what Ctrl+V goes
  // through — bracketed when the program asked, line breaks as Enter.
  const pasteFromMenu = () => {
    const term = termRef.current
    if (!term) return
    const blocked = () => toast.info(t('terminal.menu.pasteBlocked', { key: IS_MAC ? '⌘V' : 'Ctrl+V' }))
    if (!window.isSecureContext || !navigator.clipboard?.readText) {
      blocked()
      return
    }
    navigator.clipboard.readText().then((text) => { if (text) term.paste(text) }).catch(blocked)
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
          'relative w-full h-full touch-none',
          active ? 'block' : 'hidden'
        )}
      />
      {(historyOpen || paneCopyMode || scrolledUp) && (
        <button
          type="button"
          aria-label={t('terminal.mobile.latest')}
          title={t('terminal.mobile.latest')}
          data-history-latest
          onPointerDown={(e) => e.preventDefault()}
          onClick={() => latestRef.current()}
          className="absolute bottom-3 right-3 z-10 flex h-10 w-10 items-center justify-center rounded-full bg-secondary text-foreground shadow-md focus-visible:outline-2 focus-visible:outline-ring"
        >
          <ArrowDown className="h-5 w-5" aria-hidden="true" />
        </button>
      )}
      {pinch && (
        <div data-font-preview className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center">
          <span className="rounded-xl bg-foreground/85 px-3 py-1.5 text-sm font-medium tabular-nums text-background shadow-md">
            {t('terminal.fontPreview', { size: pinch.size, cols: pinch.cols })}
          </span>
        </div>
      )}
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
      {menu && (
        <TerminalContextMenu
          at={menu}
          onOpenChange={(open) => setMenu((m) => m && { ...m, open })}
          onCopy={copyFromMenu}
          onPaste={pasteFromMenu}
          onSelectText={() => window.dispatchEvent(new Event(SELECT_TEXT_EVENT))}
          onClosed={() => termRef.current?.focus()}
        />
      )}
    </>
  )
}
