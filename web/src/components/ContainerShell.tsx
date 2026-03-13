import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Circle, Eraser, Unplug } from 'lucide-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'

interface ContainerShellProps {
  containerId: string
}

export default function ContainerShell({ containerId }: ContainerShellProps) {
  const { t } = useTranslation()
  const terminalRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
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

    const token = api.getToken()
    if (!token) {
      term.writeln(`\x1b[31m${t('terminal.notAuthenticated')}\x1b[0m`)
      return () => { term.dispose() }
    }

    const wsUrl = api.buildWsUrl(`/ws/docker/containers/${containerId}/exec`)
    const ws = new WebSocket(wsUrl)
    wsRef.current = ws

    ws.onopen = () => {
      setConnected(true)
      term.focus()
    }

    ws.onmessage = (event) => {
      term.write(event.data)
    }

    ws.onerror = () => {
      term.writeln(`\r\n\x1b[31m${t('terminal.wsError')}\x1b[0m`)
    }

    ws.onclose = () => {
      setConnected(false)
      term.writeln(`\r\n\x1b[2m${t('terminal.disconnected')}\x1b[0m`)
    }

    const onDataDisposable = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(data)
      }
    })

    const onResizeDisposable = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    })

    const handleResize = () => {
      fitAddon.fit()
    }
    window.addEventListener('resize', handleResize)

    return () => {
      window.removeEventListener('resize', handleResize)
      onDataDisposable.dispose()
      onResizeDisposable.dispose()
      ws.close()
      term.dispose()
    }
  }, [containerId, t])

  return (
    <div className="bg-[#0a0a0a] rounded-2xl overflow-hidden card-shadow">
      {/* Toolbar */}
      <div className="flex items-center justify-between px-3 py-2 bg-[#111111] border-b border-white/[0.06]">
        <div className="flex items-center gap-2">
          <div className="flex items-center gap-1.5">
            <Circle className={`h-2 w-2 fill-current ${connected ? 'text-[#00c471]' : 'text-[#f04452]'}`} />
            <span className="text-[11px] text-white/40 font-medium">
              {connected ? t('terminal.connected') : t('terminal.disconnected')}
            </span>
          </div>
        </div>
        <div className="flex items-center gap-0.5">
          <Button
            variant="ghost"
            size="icon-xs"
            className="text-white/40 hover:text-white hover:bg-white/10"
            title={t('terminal.clear')}
            onClick={handleClear}
          >
            <Eraser className="h-3.5 w-3.5" />
          </Button>
          {connected && (
            <Button
              variant="ghost"
              size="icon-xs"
              className="text-white/40 hover:text-[#f04452] hover:bg-white/10"
              title={t('terminal.disconnect')}
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
        className="h-[420px] w-full px-1 pt-1"
        onClick={() => termRef.current?.focus()}
      />
    </div>
  )
}
