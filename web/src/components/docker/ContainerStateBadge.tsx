import { containerNeedsAttention } from '@/lib/containerState'
import { useTranslation } from 'react-i18next'

export function ContainerStateBadge({ state, status = '' }: { state: string; status?: string }) {
  const { t } = useTranslation()
  const unhealthy = /unhealthy/i.test(status)
  const starting = /health:\s*starting/i.test(status)
  const attention = containerNeedsAttention(state, status)
  const label = unhealthy ? 'unhealthy' : starting ? 'starting' : state.toLowerCase()
  const color = attention ? 'bg-destructive/10 text-destructive' : starting || state === 'paused' ? 'bg-warning/10 text-warning' : state === 'running' ? 'bg-success/10 text-success' : 'bg-secondary text-muted-foreground'
  return <span title={status} className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${color}`}>{t(`docker.improvements.state.${label}`, label)}</span>
}
