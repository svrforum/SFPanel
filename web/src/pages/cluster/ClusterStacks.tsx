import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Server, RefreshCw, ArrowRightLeft, Layers, AlertTriangle } from 'lucide-react'
import { api } from '@/lib/api'
import type { ClusterNodeStacks, ClusterStatus } from '@/types/api'
import { Button } from '@/components/ui/button'
import { MigrateStackDialog } from '@/pages/docker/components/MigrateStackDialog'
import { cn } from '@/lib/utils'

// Node health dot — reachability for stacks (error) takes precedence, otherwise
// the node's reported health. Mirrors the 4-state palette used by NodeSelector.
function nodeDot(status: string, error?: string) {
  if (error) return 'bg-[#f04452]'
  switch (status) {
    case 'online': return 'bg-[#00c471]'
    case 'suspect': return 'bg-[#f59e0b]'
    case 'offline': return 'bg-[#f04452]'
    default: return 'bg-muted-foreground'
  }
}

function stackDot(status: string) {
  switch (status) {
    case 'running': return 'bg-[#00c471]'
    case 'partial': return 'bg-[#f59e0b]'
    default: return 'bg-muted-foreground/40'
  }
}

// ClusterStacks (Cluster › Docker Stacks) is the cluster-wide management view:
// every node's compose stacks grouped by node, with open-on-node and migrate.
// The per-node Docker › Stacks page stays single-node; this is the aggregate.
export default function ClusterStacks() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [status, setStatus] = useState<ClusterStatus | null>(null)
  const [nodes, setNodes] = useState<ClusterNodeStacks[]>([])
  const [loading, setLoading] = useState(true)
  const [migrateOpen, setMigrateOpen] = useState(false)
  const [migrateProject, setMigrateProject] = useState('')
  const [migrateSource, setMigrateSource] = useState<string | undefined>(undefined)

  const load = useCallback(async (showLoading = true) => {
    if (showLoading) setLoading(true)
    try {
      const [s, data] = await Promise.all([
        // local: this is a cluster-wide aggregate page — never scope status to a
        // (possibly offline) remote currentNode, which would 503 and falsely read
        // as "cluster not enabled". getClusterStacks already fans out locally.
        api.getClusterStatus(true),
        api.getClusterStacks().catch(() => [] as ClusterNodeStacks[]),
      ])
      setStatus(s)
      setNodes(data)
    } finally {
      if (showLoading) setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const openMigrate = (project: string, sourceNode: string) => {
    setMigrateProject(project)
    setMigrateSource(sourceNode)
    setMigrateOpen(true)
  }

  // Drill into a stack on its own node: set the global node context (so the
  // shared detail page + chrome target that node) and navigate to the detail
  // view. Dispatch the sync event every other node-switcher uses.
  const openStack = (node: ClusterNodeStacks, name: string) => {
    api.setCurrentNode(node.local ? null : node.node_id)
    window.dispatchEvent(new Event('sfpanel:node-changed'))
    navigate(`/docker/stacks/${name}`)
  }

  const nodeError = (code?: string) => {
    if (!code) return ''
    if (code === 'unreachable') return t('cluster.stacks.nodeUnreachable')
    if (code === 'list_failed') return t('cluster.stacks.nodeListFailed')
    return code
  }

  if (!loading && !status?.enabled) {
    return (
      <div className="bg-card rounded-2xl p-8 card-shadow text-center">
        <Server className="h-12 w-12 text-muted-foreground mx-auto mb-3" />
        <p className="text-[13px] text-muted-foreground">{t('cluster.notEnabled.title')}</p>
      </div>
    )
  }

  const totalStacks = nodes.reduce((n, ns) => n + ns.stacks.length, 0)

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          <Layers className="h-3.5 w-3.5" />
          {t('cluster.stacks.count', { count: totalStacks })}
        </span>
        <Button variant="outline" size="sm" className="rounded-xl" onClick={() => void load()} disabled={loading}>
          <RefreshCw className={cn('h-4 w-4 mr-1', loading && 'animate-spin')} />
          {t('common.refresh')}
        </Button>
      </div>

      {loading && nodes.length === 0 && (
        <p className="text-[13px] text-muted-foreground py-8 text-center">{t('common.loading')}</p>
      )}

      {!loading && totalStacks === 0 && nodes.every((n) => !n.error) && (
        <div className="bg-card rounded-2xl p-8 card-shadow text-center">
          <Layers className="h-12 w-12 text-muted-foreground mx-auto mb-3" />
          <p className="text-[13px] text-muted-foreground">{t('cluster.stacks.empty')}</p>
        </div>
      )}

      <div className="space-y-3">
        {nodes.map((node) => (
          <div key={node.node_id} className="bg-card rounded-2xl card-shadow overflow-hidden">
            {/* Node header */}
            <div className="flex items-center gap-2 px-4 py-3 border-b border-border/60">
              <span className={cn('h-2 w-2 rounded-full shrink-0', nodeDot(node.status, node.error))} />
              <span className="text-[14px] font-semibold truncate min-w-0">{node.node_name}</span>
              {node.local && (
                <span className="text-[11px] text-muted-foreground">({t('layout.cluster.localNode')})</span>
              )}
              {node.error ? (
                <span className="inline-flex items-center gap-1 text-[11px] text-[#f59e0b] ml-auto">
                  <AlertTriangle className="h-3 w-3 shrink-0" />
                  {nodeError(node.error)}
                </span>
              ) : (
                <span className="text-[11px] text-muted-foreground ml-auto">
                  {t('cluster.stacks.nodeCount', { count: node.stacks.length })}
                </span>
              )}
            </div>

            {/* Stack rows */}
            {node.stacks.length > 0 ? (
              <div className="divide-y divide-border/40">
                {node.stacks.map((p) => (
                  <div key={p.name} className="group flex items-center gap-3 px-4 py-2.5 hover:bg-secondary/40">
                    <span className={cn('inline-block w-2 h-2 rounded-full shrink-0', stackDot(p.real_status))} />
                    <button
                      type="button"
                      onClick={() => openStack(node, p.name)}
                      className="text-[13px] font-medium truncate min-w-0 flex-1 text-left rounded hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
                    >
                      {p.name}
                    </button>
                    <span className="text-[11px] text-muted-foreground shrink-0">{p.running_count}/{p.service_count}</span>
                    <button
                      type="button"
                      onClick={() => openMigrate(p.name, node.node_id)}
                      title={t('docker.migrate.action')}
                      aria-label={t('docker.migrate.action')}
                      className="shrink-0 rounded text-muted-foreground opacity-60 transition hover:text-primary group-hover:opacity-100 focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
                    >
                      <ArrowRightLeft className="h-3.5 w-3.5" />
                    </button>
                  </div>
                ))}
              </div>
            ) : (
              !node.error && (
                <p className="px-4 py-3 text-[12px] text-muted-foreground">{t('docker.stacks.noStacks')}</p>
              )
            )}
          </div>
        ))}
      </div>

      <MigrateStackDialog
        open={migrateOpen}
        onOpenChange={setMigrateOpen}
        project={migrateProject}
        sourceNodeId={migrateSource}
        onMigrated={() => void load(false)}
      />
    </div>
  )
}
