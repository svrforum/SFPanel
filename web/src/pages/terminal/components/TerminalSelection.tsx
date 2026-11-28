import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { KNOB_TARGET, knobLeft } from './selectionKnob'

// A point on screen, in viewport pixels.
export interface Point {
  x: number
  y: number
}

// Where the selection sits: the finger's x (or the selection's middle), and
// the top and bottom of the rows it covers. The bar goes above it when there is
// room, below it otherwise, so it never covers what it is about to copy.
export interface BarAnchor {
  x: number
  top: number
  bottom: number
}

export type HandleEnd = 'start' | 'end'

// Where a handle hangs — under the edge of the selection it holds — and `aim`,
// the middle of the cell at that end: what a drag of it moves.
export interface HandleAt extends Point {
  aim: Point
}

// The long-press selection's controls: two handles that hang below the ends of
// the selection and drag it wider or narrower — onHandleDrag is given the
// point the dragged end has moved to, not the finger, and a bar to copy it, copy its
// line, or open the whole output. Rendered into <body>, outside the terminal,
// so their touches never reach the terminal's own touch handling. A tap
// anywhere else, Escape or a resize dismisses them; the owner dismisses them
// on scroll and on typing. The bar steps aside while a handle is dragged.
export interface SelectionLayout {
  handles: Record<HandleEnd, HandleAt | null>
  bar: BarAnchor
}

// `measure` reads where things go from the terminal's layout; it changes
// identity whenever the selection does, and is read again then.
export function TerminalSelection({ measure, canCopyLine, onHandleDrag, onCopy, onCopyLine, onViewAll, onDismiss }: {
  measure: () => SelectionLayout | null
  canCopyLine: boolean
  onHandleDrag: (end: HandleEnd, point: Point) => void
  onCopy: () => void
  onCopyLine: () => void
  onViewAll: () => void
  onDismiss: () => void
}) {
  const { t } = useTranslation()
  const rootRef = useRef<HTMLDivElement>(null)
  const barRef = useRef<HTMLDivElement>(null)
  // The handle being dragged, and how far the end's cell lay from the finger
  // when it took hold, kept for the whole drag: the end moves as far as the
  // finger does, and taking hold does not move it — wherever on the knob the
  // finger landed, and though a knob kept clear of the edge sits away from the
  // end it holds.
  const [dragging, setDragging] = useState<{ end: HandleEnd; grab: Point } | null>(null)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)

  // Positions come from the terminal's layout, outside React; read them as
  // this renders, which it does whenever the selection or a drag changes.
  const layout = measure()
  const handles = layout?.handles ?? { start: null, end: null }
  const bar = layout?.bar

  const barX = bar?.x
  const barTop = bar?.top
  const barBottom = bar?.bottom
  useLayoutEffect(() => {
    const el = barRef.current
    if (!el || barX === undefined || barTop === undefined || barBottom === undefined) return
    const margin = 8
    const { offsetWidth: w, offsetHeight: h } = el
    // Below the selection the handles hang in the way; leave them room.
    const top = barTop - h - margin >= margin ? barTop - h - margin : barBottom + 28 + margin
    const left = Math.max(margin, Math.min(barX - w / 2, window.innerWidth - w - margin))
    setPos({ left, top })
  }, [barX, barTop, barBottom, dragging])

  useEffect(() => {
    const onPointerDown = (e: PointerEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) onDismiss()
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

  const handle = (end: HandleEnd) => {
    const at = handles[end]
    if (!at) return null
    // While dragged, the knob stays under the finger.
    const grab = dragging?.end === end ? dragging.grab : null
    const left = grab ? at.aim.x - grab.x - KNOB_TARGET / 2 : knobLeft(at.x, window.innerWidth)
    const top = grab ? at.aim.y - grab.y - KNOB_TARGET / 2 : at.y - 4
    return (
      <div
        key={end}
        role="button"
        aria-label={t(end === 'start' ? 'terminal.copyBar.startHandle' : 'terminal.copyBar.endHandle')}
        data-selection-handle={end}
        // A 40px target around a 16px knob that hangs just below the text,
        // kept clear of the screen's edges (knobPlacement).
        style={{ position: 'fixed', left, top, width: KNOB_TARGET, height: KNOB_TARGET, touchAction: 'none' }}
        className="z-50 flex justify-center"
        onPointerDown={(e) => {
          e.preventDefault()
          e.stopPropagation()
          e.currentTarget.setPointerCapture(e.pointerId)
          setDragging({ end, grab: { x: at.aim.x - e.clientX, y: at.aim.y - e.clientY } })
        }}
        onPointerMove={(e) => { if (grab) onHandleDrag(end, { x: e.clientX + grab.x, y: e.clientY + grab.y }) }}
        onPointerUp={() => setDragging(null)}
        onPointerCancel={() => setDragging(null)}
      >
        <span className="mt-1 block h-4 w-4 rounded-full bg-primary shadow-md ring-2 ring-background" aria-hidden="true" />
      </div>
    )
  }

  const button = 'h-10 rounded-lg px-3 text-[13px]'
  return createPortal(
    <div ref={rootRef} data-terminal-selection>
      {handle('start')}
      {handle('end')}
      <div
        ref={barRef}
        role="toolbar"
        aria-label={t('terminal.copyBar.label')}
        style={{ position: 'fixed', left: pos?.left ?? 0, top: pos?.top ?? 0, visibility: pos && !dragging ? 'visible' : 'hidden' }}
        className="z-50 flex items-center gap-1 rounded-xl border bg-popover p-1 text-popover-foreground shadow-lg"
      >
        <Button type="button" className={button} onClick={onCopy}>{t('terminal.copyBar.copy')}</Button>
        {canCopyLine && <Button type="button" variant="ghost" className={button} onClick={onCopyLine}>{t('terminal.copyBar.copyLine')}</Button>}
        <Button type="button" variant="ghost" className={button} onClick={onViewAll}>{t('terminal.copyBar.viewAll')}</Button>
      </div>
    </div>,
    document.body,
  )
}
