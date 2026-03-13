import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'
import { api } from '@/lib/api'
import i18n from '@/i18n'

interface ContainerShellProps {
  containerId: string
}

export default function ContainerShell({ containerId }: ContainerShellProps) {
  const terminalRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const wsRef = useRef<WebSocket | null>(null)

  useEffect(() => {
    if (!terminalRef.current) return

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      theme: {
        background: '#1e1e1e',
        foreground: '#d4d4d4',
        cursor: '#d4d4d4',
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

    term.writeln(i18n.t('terminal.connectingShell'))

    const token = api.getToken()
    if (!token) {
      term.writeln(`\r\n${i18n.t('terminal.notAuthenticated')}`)
      return
    }

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/ws/docker/containers/${containerId}/exec`
    const ws = new WebSocket(wsUrl, api.getWebSocketProtocols())
    wsRef.current = ws

    ws.onopen = () => {
      term.writeln(`${i18n.t('terminal.connected')}\r\n`)
      term.focus()
    }

    ws.onmessage = (event) => {
      term.write(event.data)
    }

    ws.onerror = () => {
      term.writeln(`\r\n${i18n.t('terminal.wsError')}`)
    }

    ws.onclose = () => {
      term.writeln(`\r\n${i18n.t('terminal.connectionClosed')}`)
    }

    // Send terminal input to WebSocket
    const onDataDisposable = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(data)
      }
    })

    // Send resize events
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
  }, [containerId])

  return (
    <div
      ref={terminalRef}
      className="h-[400px] w-full rounded-md overflow-hidden"
    />
  )
}
