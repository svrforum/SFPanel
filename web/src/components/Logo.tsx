import { cn } from '@/lib/utils'

/**
 * SFPanel mark: a hexagon with a squared "S" knocked out of it.
 *
 * The S's upper-left — the stem plus the top and middle bars — is itself an
 * "F", so the monogram carries both letters as one shape instead of stacking
 * two glyphs. Overlapping the letters is what made the previous mark
 * unreadable much below 32px.
 *
 * The geometry is constructed on a 64-unit grid rather than drawn by eye:
 * hexagon at R=27 with a 4-unit corner radius, monogram 24x30 on a uniform
 * 6-unit stroke, and counters exactly as wide as the stroke. Keeping the
 * counters at stroke width is what stops the S filling in when it is
 * rasterised down to a 16px favicon.
 *
 * Path is `fill-rule="evenodd"`: the S is a true hole, not a lighter shape
 * painted on top, so the mark sits correctly on any background.
 */
const MARK_PATH =
  'M30.00 6.15A4.00 4.00 0 0 1 34.00 6.15L53.38 17.35A4.00 4.00 0 0 1 55.38 20.81' +
  'L55.38 43.19A4.00 4.00 0 0 1 53.38 46.65L34.00 57.85A4.00 4.00 0 0 1 30.00 57.85' +
  'L10.62 46.65A4.00 4.00 0 0 1 8.62 43.19L8.62 20.81A4.00 4.00 0 0 1 10.62 17.35Z' +
  'M20.00 17.00L44.00 17.00L44.00 23.00L26.00 23.00L26.00 29.00L44.00 29.00' +
  'L44.00 47.00L20.00 47.00L20.00 41.00L38.00 41.00L38.00 35.00L20.00 35.00Z'

/**
 * The mark alone. Inherits colour, so `text-primary` themes it.
 *
 * Always decorative: every caller sits inside something that already carries
 * the accessible name (a link with aria-label, or adjacent heading text).
 * A caller that needs the mark to stand alone must name it itself.
 */
export function LogoMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 64 64" className={cn('text-primary', className)} aria-hidden="true">
      <path fill="currentColor" fillRule="evenodd" d={MARK_PATH} />
    </svg>
  )
}

/**
 * Mark + wordmark lockup. The wordmark is live text in the app's own
 * typeface, not a bitmap, so it stays sharp at any zoom and inherits the
 * theme. The old banner baked "SERVER MANAGEMENT PANEL" into the image at a
 * size that was unreadable in a 240px sidebar; the name alone carries it.
 */
export default function Logo({ className }: { className?: string }) {
  return (
    <span className={cn('flex items-center gap-2 min-w-0', className)}>
      <LogoMark className="h-7 w-7 shrink-0" />
      <span className="text-[17px] font-bold tracking-tight text-foreground truncate">
        SF<span className="font-medium text-muted-foreground">Panel</span>
      </span>
    </span>
  )
}
