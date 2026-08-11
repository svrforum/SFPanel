import type { ComponentType } from 'react'
import { AlertTriangle, X, Play, Square, Check, Zap, RotateCw, Skull } from 'lucide-react'
import type { ContainerEvent } from '@/types/api'

interface Props {
  event: ContainerEvent
}

// Static map of event-type → icon component. Defined at module scope so the
// react-hooks/static-components rule sees the components as stable, not
// "created during render".
const iconMap = {
  oom: AlertTriangle,
  die: X,
  start: Play,
  restart: RotateCw,
  stop: Square,
  healthy: Check,
  unhealthy: Zap,
  kill: Skull,
} as const satisfies Record<ContainerEvent['event_type'], ComponentType<{ className?: string }>>

export function EventTimelineRow({ event }: Props) {
  const date = new Date(event.ts)
  const time = date.toLocaleTimeString()
  const IconComponent = iconMap[event.event_type]
  const color = colorFor(event.event_type)
  const summary = summarize(event)

  return (
    <div className="flex items-start gap-2 py-1.5">
      <IconComponent className={`h-3.5 w-3.5 mt-0.5 ${color}`} />
      <div className="flex-1 text-[12px]">
        <div className="flex items-center gap-2">
          <span className="text-muted-foreground tabular-nums">{time}</span>
          <span className="font-medium">{event.event_type}</span>
          {event.exit_code !== null && (
            <span className="text-muted-foreground">exit {event.exit_code}</span>
          )}
        </div>
        {summary && <div className="text-muted-foreground mt-0.5">{summary}</div>}
      </div>
    </div>
  )
}

function colorFor(t: ContainerEvent['event_type']): string {
  switch (t) {
    case 'oom':
    case 'die':
    case 'unhealthy':
    case 'kill':
      return 'text-destructive'
    case 'healthy':
    case 'start':
      return 'text-emerald-600'
    case 'restart':
      return 'text-amber-600'
    default:
      return 'text-muted-foreground'
  }
}

function summarize(ev: ContainerEvent): string | null {
  if (!ev.detail) return null
  try {
    const obj = JSON.parse(ev.detail)
    if (typeof obj === 'object' && obj !== null && 'output' in obj) {
      return String((obj as { output: unknown }).output).slice(0, 100)
    }
  } catch {
    return ev.detail.slice(0, 100)
  }
  return null
}
