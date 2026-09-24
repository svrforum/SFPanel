import { Link } from 'react-router-dom'
import { ArrowUpRight } from 'lucide-react'

interface MetricsCardProps {
  title: string
  value: string
  percent?: number
  icon: React.ReactNode
  to: string
  description?: string
  subLabel?: string
  subValue?: string
}

function getValueColor(percent: number): string {
  if (percent > 80) return 'text-destructive'
  if (percent >= 60) return 'text-warning'
  return ''
}

export default function MetricsCard({ title, value, percent, icon, to, description, subLabel, subValue }: MetricsCardProps) {
  const clamped = percent == null ? undefined : Math.min(100, Math.max(0, percent))
  return (
    <Link to={to} className="group flex min-w-0 flex-col rounded-2xl bg-card p-3.5 card-shadow transition-colors hover:bg-secondary/60 focus-visible:outline-2 focus-visible:outline-ring md:p-5">
      <div className="mb-2 flex min-w-0 items-center gap-2 text-muted-foreground">
        <span className="shrink-0 text-primary" aria-hidden="true">{icon}</span>
        <h3 className="min-w-0 flex-1 break-words text-xs font-medium md:text-sm">{title}</h3>
        <ArrowUpRight className="size-4 shrink-0" aria-hidden="true" />
      </div>
      <p className={`whitespace-pre-line break-words text-lg font-bold tabular-nums md:text-xl ${clamped == null ? '' : getValueColor(clamped)}`}>{value}</p>
      {description && <p className="mt-1 break-words text-xs text-muted-foreground">{description}</p>}
      {clamped != null && <div role="progressbar" aria-label={title} aria-valuenow={clamped} aria-valuemin={0} aria-valuemax={100} className="mt-3 h-1.5 overflow-hidden rounded-full bg-secondary">
        <div className={`h-full rounded-full ${clamped > 80 ? 'bg-destructive' : clamped >= 60 ? 'bg-warning' : 'bg-primary'}`} style={{ width: `${clamped}%` }} />
      </div>}
      {subLabel && subValue && <p className="mt-2 break-words text-xs text-muted-foreground"><span>{subLabel}</span> {subValue}</p>}
    </Link>
  )
}
