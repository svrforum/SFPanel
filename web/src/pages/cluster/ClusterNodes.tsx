import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Server, Trash2, RefreshCw, Crown } from 'lucide-react'
import { api } from '@/lib/api'
import type { ClusterNode, ClusterStatus } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'

export default function ClusterNodes() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<ClusterStatus | null>(null)
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [localId, setLocalId] = useState('')
  const [loading, setLoading] = useState(true)

  const loadNodes = () => {
    setLoading(true)
    Promise.all([
      api.getClusterStatus(),
      api.getClusterNodes().catch(() => ({ nodes: [], local_id: '', is_leader: false })),
    ]).then(([s, data]) => {
      setStatus(s)
      setNodes(data.nodes)
      setLocalId(data.local_id)
    }).finally(() => setLoading(false))
  }

  useEffect(() => { loadNodes() }, [])

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
              <TableHead>{t('cluster.nodes.apiAddress')}</TableHead>
              <TableHead>{t('cluster.nodes.grpcAddress')}</TableHead>
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
                <TableCell className="text-[13px] text-muted-foreground">{node.api_address}</TableCell>
                <TableCell className="text-[13px] text-muted-foreground">{node.grpc_address}</TableCell>
                <TableCell className="text-[13px] text-muted-foreground">
                  {new Date(node.joined_at).toLocaleDateString()}
                </TableCell>
                <TableCell>
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
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
