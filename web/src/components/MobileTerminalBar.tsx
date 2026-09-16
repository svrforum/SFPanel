import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { MODIFIERS_CONSUMED_EVENT, MODIFIERS_EVENT, NO_MODIFIERS, terminalKey, type TerminalModifiers } from '@/lib/terminalKeys'
import type { TerminalSessionElement } from '@/pages/terminal/components/TerminalSession'

// Pointer presses preserve xterm focus so the Android IME stays open.
export default function MobileTerminalBar({ onSendKey }: { onSendKey: (data: string) => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [modifiers, setModifiers] = useState<TerminalModifiers>(NO_MODIFIERS)
  useEffect(() => {
    const consumed = () => {
      setModifiers(NO_MODIFIERS)
      window.dispatchEvent(new CustomEvent(MODIFIERS_EVENT, { detail: NO_MODIFIERS }))
    }
    window.addEventListener(MODIFIERS_CONSUMED_EVENT, consumed)
    return () => {
      window.removeEventListener(MODIFIERS_CONSUMED_EVENT, consumed)
      window.dispatchEvent(new CustomEvent(MODIFIERS_EVENT, { detail: NO_MODIFIERS }))
    }
  }, [])
  const change = (next: TerminalModifiers) => {
    setModifiers(next)
    window.dispatchEvent(new CustomEvent(MODIFIERS_EVENT, { detail: next }))
  }
  const send = (data: string) => { onSendKey(terminalKey(data, modifiers)); change(NO_MODIFIERS) }
  const scroll = (direction: 'up' | 'down' | 'bottom') => {
    document.querySelectorAll<TerminalSessionElement>('[data-terminal-session="active"]').forEach(el => {
      const term = el.__termRef?.current
      if (direction === 'bottom') {
        // Finish a pending keyboard resize first, so the target is the latest
        // line in the new-sized terminal rather than the old viewport's end.
        el.__fitAddon?.fit()
        // xterm 6 can leave viewport pixels and buffer rows out of sync after
        // resize. A full-buffer delta clamps to the true end; scrollToBottom's
        // relative delta can stop one resize-height short in that state.
        requestAnimationFrame(() => term?.scrollLines(term.buffer.active.length))
      }
      else term?.scrollPages(direction === 'up' ? -1 : 1)
    })
  }
  const keyClass = 'shrink-0 min-w-12 min-h-12 px-3 rounded-lg text-sm font-medium bg-secondary text-foreground active:bg-accent focus-visible:outline-2 focus-visible:outline-ring'
  const keys = [
    { label: 'Esc', data: '\x1b', name: 'escape' },
    { label: 'Tab', data: '\t', name: 'tab' },
    { label: 'Enter', data: '\r', name: 'enter' },
    { label: '↑', data: '\x1b[A', name: 'up' },
    { label: '↓', data: '\x1b[B', name: 'down' },
    { label: '←', data: '\x1b[D', name: 'left' },
    { label: '→', data: '\x1b[C', name: 'right' },
    { label: 'PgUp', data: '\x1b[5~', name: 'pageUp' },
    { label: 'PgDn', data: '\x1b[6~', name: 'pageDown' },
    { label: 'Home', data: '\x1b[H', name: 'home' },
    { label: 'End', data: '\x1b[F', name: 'end' },
    ...['c', 'd', 'z', '|', '/', '~', '-'].map(data => ({ label: data, data, name: '' })),
  ]
  return (
    <div data-mobile-terminal-bar className="md:hidden shrink-0 bg-card border-t border-border">
      <div role="group" aria-label={t('terminal.mobile.keys')} className="flex items-center gap-1 px-1 py-1 overflow-x-auto">
        {(['shift', 'ctrl', 'alt'] as const).map(key => (
          <button key={key} aria-pressed={modifiers[key]} aria-label={t('terminal.mobile.modifier', { key })}
            className={cn(keyClass, modifiers[key] && 'bg-primary text-primary-foreground')}
            onPointerDown={e => e.preventDefault()} onClick={() => change({ ...modifiers, [key]: !modifiers[key] })}>
            {key === 'shift' ? 'Shift' : key === 'ctrl' ? 'Ctrl' : 'Alt'}
          </button>
        ))}
        {keys.map(key => (
          <button key={key.label} className={keyClass} aria-label={key.name ? t(`terminal.mobile.${key.name}`) : key.label}
            onPointerDown={e => e.preventDefault()} onClick={() => send(key.data)}>{key.label}</button>
        ))}
      </div>
      <div role="group" aria-label={t('terminal.mobile.history')} className="flex gap-1 px-1 py-1 overflow-x-auto border-t border-border pb-safe">
        <button className={keyClass} onPointerDown={e => e.preventDefault()} onClick={() => scroll('up')}>{t('terminal.mobile.scrollUp')}</button>
        <button className={keyClass} onPointerDown={e => e.preventDefault()} onClick={() => scroll('down')}>{t('terminal.mobile.scrollDown')}</button>
        <button className={keyClass} onPointerDown={e => e.preventDefault()} onClick={() => scroll('bottom')}>{t('terminal.mobile.latest')}</button>
        <button className={cn(keyClass, 'text-destructive')} onPointerDown={e => e.preventDefault()} onClick={() => { onSendKey('\x03'); change(NO_MODIFIERS) }}>Ctrl+C</button>
        <button className={keyClass} onClick={() => navigate('/ai')}>{t('ai.title')}</button>
        <button className={keyClass} onClick={() => navigate('/dashboard')}>{t('layout.mobileNav.dashboard')}</button>
      </div>
    </div>
  )
}
