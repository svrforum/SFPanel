import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Server, Trash2, RefreshCw, Crown, Tag, ArrowRightLeft } from 'lucide-react'
import { api } from '@/lib/api'
import type { ClusterNode, ClusterStatus, ClusterNodeMetrics } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'

export default function ClusterNodes() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<ClusterStatus | null>(null)
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [metrics, setMetrics] = useState<ClusterNodeMetrics[]>([])
  const [localId, setLocalId] = useState('')
  const [loading, setLoading] = useState(true)

  // Label editing
  const [labelDialogOpen, setLabelDialogOpen] = useState(false)
  const [editingNodeId, setEditingNodeId] = useState('')
  const [editingNodeName, setEditingNodeName] = useState('')
  const [labelKey, setLabelKey] = useState('')
  const [labelValue, setLabelValue] = useState('')
  const [editLabels, setEditLabels] = useState<Record<string, string>>({})

  const loadNodes = () => {
    setLoading(true)
    Promise.all([
      api.getClusterStatus(),
      api.getClusterNodes().catch(() => ({ nodes: [], local_id: '', is_leader: false })),
      api.getClusterOverview().catch(() => null),
    ]).then(([s, data, overview]) => {
      setStatus(s)
      setNodes(data.nodes)
      setLocalId(data.local_id)
      if (overview?.metrics) setMetrics(overview.metrics)
    }).finally(() => setLoading(false))
  }

  useEffect(() => {
    loadNodes()
    const interval = setInterval(loadNodes, 15000)
    return () => clearInterval(interval)
  }, [])

  const handleRemove = async (nodeId: string, nodeName: string) => {
    if (!confirm(t('cluster.nodes.confirmRemove', { name: nodeName }))) return
    try {
      await api.removeClusterNode(nodeId)
      toast.success(t('cluster.nodes.removed', { name: nodeName }))
      loadNodes()
    } catch (err) {
      toast.error(String(err))
    }
  }

  const handleTransferLeadership = async (nodeId: string, nodeName: string) => {
    if (!confirm(t('cluster.nodes.confirmTransfer', { name: nodeName }))) return
    try {
      await api.transferClusterLeadership(nodeId)
      toast.success(t('cluster.nodes.leaderTransferred', { name: nodeName }))
      setTimeout(loadNodes, 2000)
    } catch (err) {
      toast.error(String(err))
    }
  }

  const openLabelDialog = (nodeId: string, nodeName: string, labels: Record<string, string>) => {
    setEditingNodeId(nodeId)
    setEditingNodeName(nodeName)
    setEditLabels({ ...labels })
    setLabelKey('')
    setLabelValue('')
    setLabelDialogOpen(true)
  }

  const handleAddLabel = () => {
    if (!labelKey.trim()) return
    setEditLabels(prev => ({ ...prev, [labelKey.trim()]: labelValue.trim() }))
    setLabelKey('')
    setLabelValue('')
  }

  const handleRemoveLabel = (key: string) => {
    setEditLabels(prev => {
      const next = { ...prev }
      delete next[key]
      return next
    })
  }

  const handleSaveLabels = async () => {
    try {
      await api.updateClusterNodeLabels(editingNodeId, editLabels)
      toast.success(t('cluster.nodes.labelsUpdated'))
      setLabelDialogOpen(false)
      loadNodes()
    } catch (err) {
      toast.error(String(err))
    }
  }

  if (!status?.enabled) {
    return (
      <div className="bg-card rounded-2xl p-8 card-shadow text-center">
        <Server className="h-12 w-12 text-muted-foreground mx-auto mb-3" />
        <p className="text-[13px] text-muted-foreground">{t('cluster.notEnabled.title')}</p>
      </div>
    )
  }

  const statusColor = (s: string) => {
    switch (s) {
      case 'online': return 'bg-[#00c471]/10 text-[#00c471]'
      case 'suspect': return 'bg-[#f59e0b]/10 text-[#f59e0b]'
      case 'offline': return 'bg-[#f04452]/10 text-[#f04452]'
      default: return 'bg-muted text-muted-foreground'
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
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
                      <Crown className="h-3.5 w-3.5 text-[#3182f6]" />
                    )}
                    {node.id === localId && (
                      <span className="text-[10px] text-muted-foreground">({t('layout.cluster.localNode')})</span>
                    )}
                  </div>
                </TableCell>
                <TableCell>
                  <span className={cn('inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium', statusColor(node.status))}>
                    {node.status}
                  </span>
                </TableCell>
                <TableCell className="text-[13px]">{node.role}</TableCell>
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
                        onClick={() => openLabelDialog(node.id, node.name, node.labels || {})}
                        className="p-0.5 rounded hover:bg-accent transition-colors"
                        title={t('cluster.nodes.editLabels')}
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
                        className="h-7 w-7 p-0 text-[#3182f6] hover:text-[#3182f6] hover:bg-[#3182f6]/10"
                        onClick={() => handleTransferLeadership(node.id, node.name)}
                        title={t('cluster.nodes.transferLeadership')}
                      >
                        <ArrowRightLeft className="h-3.5 w-3.5" />
                      </Button>
                    )}
                    {node.id !== localId && status.is_leader && (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 w-7 p-0 text-[#f04452] hover:text-[#f04452] hover:bg-[#f04452]/10"
                        onClick={() => handleRemove(node.id, node.name)}
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
      <Dialog open={labelDialogOpen} onOpenChange={setLabelDialogOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="text-[15px]">{t('cluster.nodes.editLabelsTitle', { name: editingNodeName })}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            {/* Existing labels */}
            <div className="space-y-2">
              {Object.entries(editLabels).map(([k, v]) => (
                <div key={k} className="flex items-center gap-2">
                  <span className="inline-flex items-center px-2 py-1 rounded-lg text-[12px] font-medium bg-secondary flex-1">
                    {k} = {v}
                  </span>
                  <button
                    onClick={() => handleRemoveLabel(k)}
                    className="p-1 rounded hover:bg-[#f04452]/10 transition-colors"
                  >
                    <Trash2 className="h-3.5 w-3.5 text-[#f04452]" />
                  </button>
                </div>
              ))}
              {Object.keys(editLabels).length === 0 && (
                <p className="text-[13px] text-muted-foreground text-center py-2">{t('cluster.nodes.noLabels')}</p>
              )}
            </div>

            {/* Add label form */}
            <div className="flex items-center gap-2">
              <Input
                value={labelKey}
                onChange={(e) => setLabelKey(e.target.value)}
                placeholder={t('cluster.nodes.labelKey')}
                className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px] flex-1"
              />
              <Input
                value={labelValue}
                onChange={(e) => setLabelValue(e.target.value)}
                placeholder={t('cluster.nodes.labelValue')}
                className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px] flex-1"
                onKeyDown={(e) => e.key === 'Enter' && handleAddLabel()}
              />
              <Button variant="outline" size="sm" className="rounded-xl" onClick={handleAddLabel} disabled={!labelKey.trim()}>
                +
              </Button>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" className="rounded-xl" onClick={() => setLabelDialogOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button className="rounded-xl" onClick={handleSaveLabels}>
              {t('common.save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
