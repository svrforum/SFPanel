import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { StatusPill, type StatusPillTone } from '@/components/StatusPill'
import type { ClusterNode } from '@/types/api'
import { isOutdated, nodeGroup, type NodeRoleKind } from '@/lib/clusterNodes'
import { KindBadge } from './KindBadge'

const STATUS_TONE: Record<ClusterNode['status'], StatusPillTone> = {
  online: 'success',
  suspect: 'warning',
  offline: 'destructive',
  joining: 'muted',
}

export function NodeStatus({ status }: { status: ClusterNode['status'] }) {
  const { t } = useTranslation()
  const label = {
    online: t('cluster.status.online'),
    suspect: t('cluster.status.suspect'),
    offline: t('cluster.status.offline'),
    joining: t('cluster.status.joining'),
  }[status] ?? status
  return <StatusPill tone={STATUS_TONE[status] ?? 'muted'}>{label}</StatusPill>
}

/** The version a node runs, flagged when it is behind the reference. */
export function NodeVersion({ version, reference }: { version?: string; reference?: string }) {
  const { t } = useTranslation()
  if (!version) return <span className="text-muted-foreground">—</span>
  return (
    <span className="inline-flex items-center gap-1.5">
      <span>{version}</span>
      {isOutdated(version, reference) && (
        <StatusPill tone="warning" className="cursor-help">
          <span title={t('cluster.nodes.outdatedTitle', { version: reference })}>{t('cluster.nodes.outdated')}</span>
        </StatusPill>
      )}
    </span>
  )
}

export function NodeGroup({ node }: { node: ClusterNode }) {
  const group = nodeGroup(node)
  if (!group) return <span className="text-muted-foreground">—</span>
  return <StatusPill tone="primary">{group}</StatusPill>
}

// A phone gets one card per node instead of a nine-column table: the same
// fields, stacked, with the row's menu beside the status.
export function NodeCard({ node, kind, isLocal, version, reference, lastSeen, labels, menu }: {
  node: ClusterNode
  kind: NodeRoleKind
  isLocal: boolean
  version?: string
  reference?: string
  lastSeen: string
  labels?: ReactNode
  menu?: ReactNode
}) {
  const { t } = useTranslation()
  return (
    <div data-testid="node-card" className="bg-card rounded-2xl card-shadow p-4 space-y-2.5">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <span data-node-name className="text-[13px] font-medium break-all">{node.name}</span>
          {isLocal && <span className="ml-1.5 text-[10px] text-muted-foreground">({t('layout.cluster.localNode')})</span>}
        </div>
        <div className="flex items-center gap-1 shrink-0">
          <NodeStatus status={node.status} />
          {menu}
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-1.5 text-[13px]">
        <KindBadge kind={kind} />
        {nodeGroup(node) && <NodeGroup node={node} />}
        <NodeVersion version={version} reference={reference} />
      </div>
      {labels}
      <div className="text-[12px] text-muted-foreground space-y-0.5">
        <div className="break-all">{node.api_address}</div>
        <div>{t('cluster.nodes.lastSeen')}: {lastSeen}</div>
      </div>
    </div>
  )
}
