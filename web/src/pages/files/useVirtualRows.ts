import { useEffect, useRef, useState } from 'react'

/**
 * Render only the rows near the viewport.
 *
 * A directory listing is capped at 10 000 entries server-side and every one of
 * them was rendered — /usr/bin alone is around two thousand. Each row carries a
 * checkbox, a button, a context menu and a dropdown, so the DOM node count runs
 * into six figures and a phone spends several seconds laying it out before the
 * first paint, then drops frames on every scroll.
 *
 * Deliberately hand-rolled rather than pulled in as a dependency. The
 * requirement here is one fixed-height list; a virtualiser library brings
 * variable sizing, horizontal windowing and a measurement cache that this page
 * has no use for, and the whole of it is the forty lines below.
 */
export function useVirtualRows({ count, rowHeight, overscan = 8, enabled = true }: {
  count: number
  rowHeight: number
  /** Extra rows above and below, so a fast scroll does not show blank space. */
  overscan?: number
  enabled?: boolean
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  // Only the viewport's own numbers are state, and they change only when the
  // browser tells us they did. The visible range is derived during render:
  // computing it in an effect would set state synchronously on every scroll
  // and cascade a second render behind each one.
  const [viewport, setViewport] = useState({ scrollTop: 0, height: 0 })

  useEffect(() => {
    const el = scrollRef.current
    if (!el || !enabled) return

    const read = () => setViewport({ scrollTop: el.scrollTop, height: el.clientHeight })
    read()

    // Passive: this listener never calls preventDefault, and saying so lets the
    // browser scroll without waiting to find out.
    el.addEventListener('scroll', read, { passive: true })
    const observer = new ResizeObserver(read)
    observer.observe(el)
    return () => {
      el.removeEventListener('scroll', read)
      observer.disconnect()
    }
  }, [enabled])

  if (!enabled) {
    return { scrollRef, start: 0, end: count, padTop: 0, padBottom: 0 }
  }

  // Before the first measurement the height is 0, which would render nothing.
  // Assume a screenful so the first paint has content rather than a blank box.
  const height = viewport.height || 600
  const start = Math.max(0, Math.floor(viewport.scrollTop / rowHeight) - overscan)
  const end = Math.min(count, start + Math.ceil(height / rowHeight) + overscan * 2)

  return {
    scrollRef,
    start,
    end,
    // Spacers rather than absolute positioning: a table keeps its own layout,
    // and two empty rows are simpler to reason about than transformed ones.
    padTop: start * rowHeight,
    padBottom: Math.max(0, (count - end) * rowHeight),
  }
}

/**
 * Below this many rows the list renders whole.
 *
 * Windowing costs a scroll container and two spacer rows, and it breaks
 * find-in-page for anything not currently rendered. An ordinary directory is
 * tens of entries, so paying that everywhere to help the rare huge one would be
 * the wrong trade.
 */
export const VIRTUALIZE_THRESHOLD = 200
