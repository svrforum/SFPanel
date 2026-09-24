import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Circle, Eraser, Unplug, Maximize2, Minimize2 } from 'lucide-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'
import { api } from '@/lib/api'
import { attachXtermTouchScroll } from '@/lib/xtermTouchScroll'
import { Button } from '@/components/ui/button'

interface ContainerShellProps {
  containerId: string
}

export default function ContainerShell({ containerId }: ContainerShellProps) {
  const { t } = useTranslation()
  const terminalRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const [attempt, setAttempt] = useState(0)
  const [expanded, setExpanded] = useState(false)
  const [ctrl, setCtrl] = useState(false)
  const ctrlRef = useRef(false)
  useEffect(() => { ctrlRef.current = ctrl }, [ctrl])
  const sendKey = (data: string) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) wsRef.current.send(data)
    termRef.current?.focus()
  }
  const [connected, setConnected] = useState(false)

  const handleClear = () => {
    termRef.current?.clear()
  }

  const handleDisconnect = () => {
    wsRef.current?.close()
  }

  useEffect(() => {
    if (!terminalRef.current) return

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 12,
      fontFamily: '"SF Mono", Menlo, Monaco, "Courier New", monospace',
      lineHeight: 1.4,
      theme: {
        background: '#0a0a0a',
        foreground: '#e5e5e5',
        cursor: '#3182f6',
        cursorAccent: '#0a0a0a',
        selectionBackground: '#3182f644',
        selectionForeground: '#ffffff',
      },
      convertEol: true,
      scrollback: 5000,
    })

    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.loadAddon(new WebLinksAddon())
    term.open(terminalRef.current)
    fitAddon.fit()
    termRef.current = term
    const detachTouch = attachXtermTouchScroll(terminalRef.current, term)

    const token = api.getToken()
    if (!token) {
      term.writeln(`\x1b[31m${t('terminal.notAuthenticated')}\x1b[0m`)
      return () => { detachTouch(); term.dispose() }
    }

    // Async because buildWsUrl mints a ws-ticket so the JWT stays out of
    // the URL; defer the actual WebSocket() construction until the URL
    // resolves and unmount-safety holds.
    let disposed = false
    let wsCleanup: (() => void) | null = null

    void (async () => {
      const wsUrl = await api.buildWsUrl(`/ws/docker/containers/${containerId}/exec`)
      if (disposed) return
      const ws = new WebSocket(wsUrl)
      // Default binaryType is 'blob' which xterm.js handles, but we
      // explicitly pick 'arraybuffer' to avoid the async Blob.text()
      // path and to ensure non-UTF8 bytes from the PTY (binary tools,
      // ANSI escape sequences with high bytes) reach xterm intact.
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws

      ws.onopen = () => {
        setConnected(true)
        term.focus()
      }

      ws.onmessage = (event) => {
        if (disposed) return
        term.write(event.data)
      }

      ws.onerror = () => {
        term.writeln(`\r\n\x1b[31m${t('terminal.wsError')}\x1b[0m`)
      }

      ws.onclose = () => {
        if (disposed) return
        setConnected(false)
        term.writeln(`\r\n\x1b[2m${t('terminal.disconnected')}\x1b[0m`)
      }

      const onDataDisposable = term.onData((data) => {
        if (ws.readyState === WebSocket.OPEN) {
          if (ctrlRef.current && data.length === 1 && /[a-z@[\]\\^_?]/i.test(data)) {
            ws.send(data === '?' ? '\x7f' : String.fromCharCode(data.toUpperCase().charCodeAt(0) & 31))
            ctrlRef.current = false; setCtrl(false)
          } else ws.send(data)
        }
      })

      const onResizeDisposable = term.onResize(({ cols, rows }) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'resize', cols, rows }))
        }
      })

      wsCleanup = () => {
        onDataDisposable.dispose()
        onResizeDisposable.dispose()
        ws.close()
      }
    })().catch(() => {
      if (!disposed) { setConnected(false); term.writeln(`\r\n${t('terminal.wsError')}`) }
    })

    const handleResize = () => {
      fitAddon.fit()
    }
    const observer = new ResizeObserver(handleResize)
    observer.observe(terminalRef.current)
    window.visualViewport?.addEventListener('resize', handleResize)
    window.addEventListener('resize', handleResize)

    return () => {
      disposed = true
      observer.disconnect()
      window.visualViewport?.removeEventListener('resize', handleResize)
      window.removeEventListener('resize', handleResize)
      detachTouch()
      wsCleanup?.()
      termRef.current = null
      term.dispose()
    }
  }, [containerId, t, attempt])

  return (
    <div data-shell-expanded={expanded} className={`bg-[#0a0a0a] rounded-2xl overflow-hidden card-shadow flex flex-col ${expanded ? 'fixed inset-2 z-[80]' : ''}`} style={expanded ? { height: 'calc(100dvh - 1rem)' } : undefined}>
      {/* Toolbar */}
      <div className="flex items-center justify-between px-3 py-2 bg-[#111111] border-b border-white/[0.06]">
        <div className="flex items-center gap-2">
          <div className="flex items-center gap-1.5">
            <Circle className={`h-2 w-2 fill-current ${connected ? 'text-success' : 'text-destructive'}`} />
            <span className="text-[11px] text-white/40 font-medium">
              {connected ? t('terminal.connected') : t('terminal.disconnected')}
            </span>
          </div>
        </div>
        <div className="flex items-center gap-0.5">
          {!connected && <Button variant="ghost" className="text-white" onClick={() => setAttempt(n => n + 1)}>{t('docker.improvements.reconnect')}</Button>}
          <Button variant="ghost" size="icon" className="text-white" aria-label={t(expanded ? 'docker.improvements.exitFullscreen' : 'docker.improvements.fullscreen')} onClick={() => setExpanded(value => !value)}>{expanded ? <Minimize2 /> : <Maximize2 />}</Button>
          <Button
            variant="ghost"
            size="icon"
            className="text-white/40 hover:text-white hover:bg-white/10"
            title={t('terminal.clear')}
            aria-label={t('terminal.clear')}
            onClick={handleClear}
          >
            <Eraser className="h-3.5 w-3.5" />
          </Button>
          {connected && (
            <Button
              variant="ghost"
              size="icon"
              className="text-white/40 hover:text-destructive hover:bg-white/10"
              title={t('terminal.disconnect')}
              aria-label={t('terminal.disconnect')}
              onClick={handleDisconnect}
            >
              <Unplug className="h-3.5 w-3.5" />
            </Button>
          )}
        </div>
      </div>

      {/* Terminal */}
      <div
        ref={terminalRef}
        className={`${expanded ? 'flex-1 min-h-0' : 'h-[min(55dvh,420px)]'} w-full px-1 pt-1 touch-none`}
        onClick={() => termRef.current?.focus()}
      />
      <div className="flex flex-wrap gap-1 border-t border-white/10 p-1 text-white" aria-label={t('terminal.title')}>
        <Button variant="ghost" aria-pressed={ctrl} disabled={!connected} onClick={() => { setCtrl(value => !value); termRef.current?.focus() }}>Ctrl</Button>
        {[['Esc', '\x1b'], ['Tab', '\t'], ['↑', '\x1b[A'], ['↓', '\x1b[B'], ['←', '\x1b[D'], ['→', '\x1b[C'], ['Ctrl+C', '\x03']].map(([label, data]) => <Button key={label} variant="ghost" disabled={!connected} onPointerDown={e => e.preventDefault()} onClick={() => sendKey(data)}>{label}</Button>)}
      </div>
    </div>
  )
}
