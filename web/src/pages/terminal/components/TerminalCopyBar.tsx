import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'

// Where the selected text sits on screen: the finger's x, and the top and
// bottom of the rows the selection covers. The bar goes above it when there is
// room, below it otherwise, so it never covers what it is about to copy.
export interface CopyBarAnchor {
  x: number
  top: number
  bottom: number
}

// The bar a long press on the terminal raises over the word it selected.
// Rendered into <body>, outside the terminal, so its taps never reach the
// terminal's own touch handling. Any tap outside it, Escape or a resize
// dismisses it; the owner dismisses it on scroll and on typing.
export function TerminalCopyBar({ anchor, canCopyWord, canCopyLine, onCopyWord, onCopyLine, onViewAll, onDismiss }: {
  anchor: CopyBarAnchor
  canCopyWord: boolean
  canCopyLine: boolean
  onCopyWord: () => void
  onCopyLine: () => void
  onViewAll: () => void
  onDismiss: () => void
}) {
  const { t } = useTranslation()
  const barRef = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)

  useLayoutEffect(() => {
    const bar = barRef.current
    if (!bar) return
    const margin = 8
    const { offsetWidth: w, offsetHeight: h } = bar
    const top = anchor.top - h - margin >= margin ? anchor.top - h - margin : anchor.bottom + margin
    const left = Math.max(margin, Math.min(anchor.x - w / 2, window.innerWidth - w - margin))
    setPos({ left, top })
  }, [anchor])

  useEffect(() => {
    const onPointerDown = (e: PointerEvent) => {
      if (!barRef.current?.contains(e.target as Node)) onDismiss()
    }
    const onKeyDown = (e: KeyboardEvent) => { if (e.key === 'Escape') onDismiss() }
    window.addEventListener('pointerdown', onPointerDown, true)
    window.addEventListener('keydown', onKeyDown, true)
    window.addEventListener('resize', onDismiss)
    return () => {
      window.removeEventListener('pointerdown', onPointerDown, true)
      window.removeEventListener('keydown', onKeyDown, true)
      window.removeEventListener('resize', onDismiss)
    }
  }, [onDismiss])

  const button = 'h-10 rounded-lg px-3 text-[13px]'
  return createPortal(
    <div
      ref={barRef}
      role="toolbar"
      aria-label={t('terminal.copyBar.label')}
      data-terminal-copy-bar
      style={{ position: 'fixed', left: pos?.left ?? 0, top: pos?.top ?? 0, visibility: pos ? 'visible' : 'hidden' }}
      className="z-50 flex items-center gap-1 rounded-xl border bg-popover p-1 text-popover-foreground shadow-lg"
    >
      {canCopyWord && <Button type="button" className={button} onClick={onCopyWord}>{t('terminal.copyBar.copy')}</Button>}
      {canCopyLine && <Button type="button" variant="ghost" className={button} onClick={onCopyLine}>{t('terminal.copyBar.copyLine')}</Button>}
      <Button type="button" variant="ghost" className={button} onClick={onViewAll}>{t('terminal.copyBar.viewAll')}</Button>
    </div>,
    document.body,
  )
}
