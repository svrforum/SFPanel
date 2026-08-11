import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { cn } from '@/lib/utils'

// On-screen key bar shown below the terminal on mobile (md:hidden): a special-
// keys row (Esc/Tab/Ctrl/Alt/arrows/symbols) with sticky Ctrl/Alt modifiers and
// a quick-nav row. Extracted from Terminal.tsx to keep the page focused.
export default function MobileTerminalBar({ onSendKey }: { onSendKey: (data: string) => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [ctrlActive, setCtrlActive] = useState(false)
  const [altActive, setAltActive] = useState(false)

  const sendKey = (key: string) => {
    let data = key
    if (ctrlActive) {
      // Convert to ctrl sequence: Ctrl+C = \x03, Ctrl+D = \x04, etc.
      if (key.length === 1) {
        const code = key.toUpperCase().charCodeAt(0) - 64
        if (code > 0 && code < 32) data = String.fromCharCode(code)
      }
      setCtrlActive(false)
    }
    if (altActive) {
      data = '\x1b' + key
      setAltActive(false)
    }
    onSendKey(data)
  }

  const keys = [
    { label: 'Esc', data: '\x1b' },
    { label: 'Tab', data: '\t' },
    { label: 'Ctrl', toggle: 'ctrl' as const },
    { label: 'Alt', toggle: 'alt' as const },
    { label: '↑', data: '\x1b[A' },
    { label: '↓', data: '\x1b[B' },
    { label: '←', data: '\x1b[D' },
    { label: '→', data: '\x1b[C' },
    { label: '|', data: '|' },
    { label: '/', data: '/' },
    { label: '~', data: '~' },
    { label: '-', data: '-' },
  ]

  return (
    <div className="md:hidden shrink-0 bg-card border-t border-border">
      {/* Special keys row */}
      <div className="flex items-center gap-0.5 px-1 py-1 overflow-x-auto no-scrollbar">
        {keys.map((k) => (
          <button
            key={k.label}
            className={cn(
              'shrink-0 px-2.5 py-1.5 rounded text-[11px] font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
              (k.toggle === 'ctrl' && ctrlActive) || (k.toggle === 'alt' && altActive)
                ? 'bg-primary text-primary-foreground'
                : 'bg-secondary text-foreground active:bg-accent'
            )}
            aria-pressed={
              k.toggle ? (k.toggle === 'ctrl' ? ctrlActive : altActive) : undefined
            }
            onClick={() => {
              if (k.toggle === 'ctrl') { setCtrlActive(!ctrlActive); setAltActive(false) }
              else if (k.toggle === 'alt') { setAltActive(!altActive); setCtrlActive(false) }
              else if (k.data) sendKey(k.data)
            }}
          >
            {k.label}
          </button>
        ))}
      </div>
      {/* Navigation row */}
      <div className="flex items-center justify-around h-10 pb-safe border-t border-border">
        <button
          onClick={() => navigate('/dashboard')}
          className="flex flex-col items-center justify-center flex-1 h-full text-muted-foreground active:text-foreground outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
        >
          <span className="text-[10px] font-medium">← {t('layout.mobileNav.dashboard')}</span>
        </button>
        <button
          onClick={() => onSendKey('\x03')}
          className="flex flex-col items-center justify-center flex-1 h-full text-destructive active:opacity-70 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
        >
          <span className="text-[10px] font-semibold">Ctrl+C</span>
        </button>
        <button
          onClick={() => onSendKey('\x04')}
          className="flex flex-col items-center justify-center flex-1 h-full text-warning active:opacity-70 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
        >
          <span className="text-[10px] font-semibold">Ctrl+D</span>
        </button>
        <button
          onClick={() => onSendKey('\x1a')}
          className="flex flex-col items-center justify-center flex-1 h-full text-muted-foreground active:text-foreground outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
        >
          <span className="text-[10px] font-semibold">Ctrl+Z</span>
        </button>
      </div>
    </div>
  )
}
