import { useCallback, useState } from 'react'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { useTranslation } from 'react-i18next'
import { Server, Trash2, RefreshCw, Crown, Tag, ArrowRightLeft } from 'lucide-react'
import { api } from '@/lib/api'
import type { ClusterNode, ClusterStatus, ClusterNodeMetrics } from '@/types/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { NodeLabelsDialog } from './components/NodeLabelsDialog'

export default function ClusterNodes() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [status, setStatus] = useState<ClusterStatus | null>(null)
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [metrics, setMetrics] = useState<ClusterNodeMetrics[]>([])
  const [localId, setLocalId] = useState('')
  const [loading, setLoading] = useState(true)

  // Label editing
  const [labelDialogOpen, setLabelDialogOpen] = useState(false)
  const [labelNode, setLabelNode] = useState<ClusterNode | null>(null)

  // Background refreshes don't toggle the page-level loading spinner —
  // only the initial mount does (loading is already true at that point).
  // Calling setLoading(true) inside loadNodes used to trip
  // react-hooks/set-state-in-effect because the effect below kicks it off
  // synchronously.
  const loadNodes = useCallback(() => {
    Promise.all([
      // local:true pins the datacenter-scope calls to this node so local_id and
      // the leader-gated action buttons stay correct while a remote node is
      // selected (?node=).
      api.getClusterStatus(true),
      api.getClusterNodes(true).catch(() => ({ nodes: [], local_id: '', is_leader: false })),
      api.getClusterOverview(true).catch(() => null),
    ]).then(([s, data, overview]) => {
      setStatus(s)
      setNodes(data.nodes)
      setLocalId(data.local_id)
      if (overview?.metrics) setMetrics(overview.metrics)
    }).finally(() => setLoading(false))
  }, [])

  // Load on mount + poll every 15s while visible (paused when the tab is hidden).
  useVisibleInterval(loadNodes, 15000)

  const handleRemove = async (nodeId: string, nodeName: string) => {
    if (!(await confirm({ title: t('cluster.nodes.confirmRemove', { name: nodeName }), danger: true }))) return
    try {
      await api.removeClusterNode(nodeId)
      toast.success(t('cluster.nodes.removed', { name: nodeName }))
      loadNodes()
    } catch (err) {
      toast.error(String(err))
    }
  }

  const handleTransferLeadership = async (nodeId: string, nodeName: string) => {
    if (!(await confirm({ title: t('cluster.nodes.confirmTransfer', { name: nodeName }) }))) return
    try {
      await api.transferClusterLeadership(nodeId)
      toast.success(t('cluster.nodes.leaderTransferred', { name: nodeName }))
      setTimeout(loadNodes, 2000)
    } catch (err) {
      toast.error(String(err))
    }
  }

  const openLabelDialog = (node: ClusterNode) => {
    setLabelNode(node)
    setLabelDialogOpen(true)
  }

  if (!status?.enabled) {
    return (
      <div className="bg-card rounded-2xl p-8 card-shadow text-center">
        <Server className="h-12 w-12 text-muted-foreground mx-auto mb-3" />
        <p className="text-[13px] text-muted-foreground">{t('cluster.notEnabled.title')}</p>
      </div>
    )
  }

  // Badge-tint variant of lib/utils nodeStatusColor (which returns the solid
  // dot classes) — Tailwind needs the tinted classes spelled out literally.
  const statusColor = (s: string) => {
    switch (s) {
      case 'online': return 'bg-success/10 text-success'
      case 'suspect': return 'bg-warning/10 text-warning'
      case 'offline': return 'bg-destructive/10 text-destructive'
      default: return 'bg-muted text-muted-foreground'
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('cluster.nodes.count', { count: nodes.length })}
        </span>
        <Button variant="outline" size="sm" className="rounded-xl" onClick={loadNodes} disabled={loading}>
          <RefreshCw className={cn("h-4 w-4 mr-1", loading && "animate-spin")} />
          {t('common.refresh')}
        </Button>
      </div>

      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('cluster.nodes.name')}</TableHead>
              <TableHead>{t('common.status')}</TableHead>
              <TableHead>{t('cluster.nodes.role')}</TableHead>
              <TableHead>{t('cluster.nodes.version')}</TableHead>
              <TableHead>{t('cluster.nodes.labels')}</TableHead>
              <TableHead>{t('cluster.nodes.apiAddress')}</TableHead>
              <TableHead>{t('cluster.nodes.joinedAt')}</TableHead>
              <TableHead>{t('common.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {nodes.map((node) => (
              <TableRow key={node.id}>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <span className="text-[13px] font-medium">{node.name}</span>
                    {node.id === status.leader_id && (
                      <Crown className="h-3.5 w-3.5 text-primary" />
                    )}
                    {node.id === localId && (
                      <span className="text-[10px] text-muted-foreground">({t('layout.cluster.localNode')})</span>
                    )}
                  </div>
                </TableCell>
                <TableCell>
                  <span className={cn('inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium', statusColor(node.status))}>
                    {t(`cluster.status.${node.status}`, { defaultValue: node.status })}
                  </span>
                </TableCell>
                <TableCell className="text-[13px]">{t(`cluster.role.${node.role}`, { defaultValue: node.role })}</TableCell>
                <TableCell className="text-[13px]">
                  {metrics.find(m => m.node_id === node.id)?.version || '-'}
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-1 flex-wrap">
                    {node.labels && Object.entries(node.labels).map(([k, v]) => (
                      <span key={k} className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-secondary text-muted-foreground">
                        {k}={v}
                      </span>
                    ))}
                    {status.is_leader && (
                      <button
                        onClick={() => openLabelDialog(node)}
                        className="p-0.5 rounded hover:bg-accent transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                        title={t('cluster.nodes.editLabels')}
                        aria-label={t('cluster.nodes.editLabels')}
                      >
                        <Tag className="h-3 w-3 text-muted-foreground" />
                      </button>
                    )}
                  </div>
                </TableCell>
                <TableCell className="text-[13px] text-muted-foreground">{node.api_address}</TableCell>
                <TableCell className="text-[13px] text-muted-foreground">
                  {new Date(node.joined_at).toLocaleDateString()}
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-1">
                    {node.id !== localId && status.is_leader && node.status === 'online' && node.id !== status.leader_id && (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 w-7 p-0 text-primary hover:text-primary hover:bg-primary/10"
                        onClick={() => handleTransferLeadership(node.id, node.name)}
                        title={t('cluster.nodes.transferLeadership')}
                        aria-label={t('cluster.nodes.transferLeadership')}
                      >
                        <ArrowRightLeft className="h-3.5 w-3.5" />
                      </Button>
                    )}
                    {node.id !== localId && status.is_leader && (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 w-7 p-0 text-destructive hover:text-destructive hover:bg-destructive/10"
                        onClick={() => handleRemove(node.id, node.name)}
                        aria-label={t('common.delete')}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Label editing dialog */}
      <NodeLabelsDialog
        open={labelDialogOpen}
        onOpenChange={setLabelDialogOpen}
        node={labelNode}
        onSaved={loadNodes}
      />
    </div>
  )
}
