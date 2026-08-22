import { useEffect, useRef, type RefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { SearchAddon } from '@xterm/addon-search'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import '@xterm/xterm/css/xterm.css'
import { api } from '@/lib/api'
import { attachXtermTouchScroll } from '@/lib/xtermTouchScroll'
import { cn, copyText } from '@/lib/utils'
import { toast } from 'sonner'

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
  __termRef?: RefObject<XTerm | null>
  __wsRef?: RefObject<WebSocket | null>
}

export function TerminalSession({ sessionId, active, fontSize }: { sessionId: string; active: boolean; fontSize: number }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XTerm | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  const initialized = useRef(false)
  const { t } = useTranslation()

  useEffect(() => {
    if (!containerRef.current || initialized.current) return
    initialized.current = true

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
            fitAddon.fit()
            term.scrollToBottom()
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
      return
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

    const onDataDisposable = term.onData((data) => {
      const sock = wsRef.current
      if (sock && sock.readyState === WebSocket.OPEN) {
        sock.send(new TextEncoder().encode(data))
      }
    })
    const onResizeDisposable = term.onResize(({ cols, rows }) => {
      const sock = wsRef.current
      if (sock && sock.readyState === WebSocket.OPEN) {
        sock.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    })

    const connect = async () => {
      const wsUrl = await api.buildWsUrl('/ws/terminal', { session_id: sessionId })
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
      container?.removeEventListener('focusin', onFocusIn)
      wsCleanup?.()
      term.dispose()
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
    el.__wsRef = wsRef
    el.__termRef = termRef
  }, [])

  // data-terminal-session is a stable hook for the parent's active-session
  // lookup (search/clear/key forwarding). Querying by Tailwind class substrings
  // broke silently when a className was reordered during the UI-polish churn;
  // this attribute is decoupled from styling.
  return (
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
  )
}
