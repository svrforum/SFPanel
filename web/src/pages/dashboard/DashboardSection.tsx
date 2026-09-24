import { useId, useState, type ReactNode } from 'react'
import { ChevronDown } from 'lucide-react'
import { cn } from '@/lib/utils'

/** Keep the desktop overview visible; let phone users expand the heavier panels. */
export default function DashboardSection({ id, title, summary, action, children, className }: {
  id: string; title: string; summary?: ReactNode; action?: ReactNode; children: ReactNode; className?: string
}) {
  const [expanded, setExpanded] = useState(false)
  const contentId = useId()
  return (
    <section id={id} aria-label={title} className={cn('min-w-0 rounded-2xl bg-card p-4 card-shadow md:p-5', className)}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="hidden text-sm font-semibold md:block">{title}</h2>
        <h2 className="min-w-0 flex-1 md:hidden">
          <button type="button" aria-expanded={expanded} aria-controls={contentId} onClick={() => setExpanded(!expanded)}
            className="flex min-h-11 w-full items-center gap-2 rounded-md text-left text-sm font-semibold focus-visible:outline-2 focus-visible:outline-ring">
            {title}<ChevronDown aria-hidden="true" className={cn('size-4 shrink-0 transition-transform', expanded && 'rotate-180')} />
          </button>
        </h2>
        {action}
      </div>
      {summary && <div className="mt-1 text-xs text-muted-foreground">{summary}</div>}
      <div id={contentId} className={cn('mt-3 min-w-0 md:block', !expanded && 'hidden')}>{children}</div>
    </section>
  )
}
