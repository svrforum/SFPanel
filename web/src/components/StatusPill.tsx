import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export type StatusPillTone = 'success' | 'destructive' | 'warning' | 'primary' | 'muted' | 'secondary'

const TONE_CLASSES: Record<StatusPillTone, string> = {
  success: 'bg-success/10 text-success',
  destructive: 'bg-destructive/10 text-destructive',
  warning: 'bg-warning/10 text-warning',
  primary: 'bg-primary/10 text-primary',
  muted: 'bg-muted text-muted-foreground',
  secondary: 'bg-secondary text-muted-foreground',
}

// Single source for the status-pill class string that used to be copy-pasted
// as a literal across Services/Processes/Packages/CronJobs. Tones map to the
// semantic color tokens so a dark-mode or palette change lands in one place.
export function StatusPill({
  tone,
  className,
  children,
}: {
  tone: StatusPillTone
  className?: string
  children: ReactNode
}) {
  return (
    <span
      className={cn(
        'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium',
        TONE_CLASSES[tone],
        className
      )}
    >
      {children}
    </span>
  )
}
