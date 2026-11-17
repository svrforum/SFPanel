import { AlertTriangle, XCircle } from 'lucide-react'
import { cn, nodeStatusColor } from '@/lib/utils'

// A node's state as a mark that does not lean on colour alone: a dot while it
// answers or is joining, a warning sign when it is slow to respond, a cross
// when it is offline. Decorative: the word goes in the control's name.
export function NodeStatusMark({ status, dotClassName = 'h-2 w-2' }: { status: string; dotClassName?: string }) {
  if (status === 'offline') return <XCircle aria-hidden="true" className="h-3 w-3 shrink-0 text-destructive" />
  if (status === 'suspect') return <AlertTriangle aria-hidden="true" className="h-3 w-3 shrink-0 text-warning" />
  return <span aria-hidden="true" className={cn('rounded-full shrink-0', dotClassName, nodeStatusColor(status))} />
}
