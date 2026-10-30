import { useTranslation } from 'react-i18next'
import { Crown } from 'lucide-react'
import { StatusPill, type StatusPillTone } from '@/components/StatusPill'
import type { NodeRoleKind } from '@/lib/clusterNodes'

const TONE: Record<NodeRoleKind, StatusPillTone> = {
  leader: 'primary',
  manager: 'secondary',
  nonvoter: 'muted',
  agent: 'muted',
}

// What a node is in the cluster, read through nodeRoleKind so an agent's empty
// role never shows as a blank. The node table, the overview and the
// conversion and migration dialogs all draw it the same way.
export function KindBadge({ kind }: { kind: NodeRoleKind }) {
  const { t } = useTranslation()
  const label = {
    leader: t('cluster.kind.leader'),
    manager: t('cluster.kind.manager'),
    nonvoter: t('cluster.kind.nonvoter'),
    agent: t('cluster.kind.agent'),
  }[kind]
  return (
    <StatusPill tone={TONE[kind]} className={kind === 'agent' ? 'gap-1 bg-transparent border border-border' : 'gap-1'}>
      {kind === 'leader' && <Crown className="h-3 w-3" aria-hidden />}
      {label}
    </StatusPill>
  )
}
