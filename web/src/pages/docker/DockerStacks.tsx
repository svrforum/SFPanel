import { useState, useEffect, useCallback, useRef } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useParams, useNavigate, useSearchParams } from 'react-router-dom'
import {
  Plus, Play, Square, RotateCw, RefreshCw,
  Trash2, Terminal, ScrollText, FileCode, Loader2,
  CheckCircle2, XCircle, Download, Undo2, Search, ChevronLeft,
  HeartPulse, Info, ArrowRightLeft, Monitor, AlertTriangle, AlertCircle,
} from 'lucide-react'
import { HealthcheckComposerDialog } from '@/components/compose/HealthcheckComposerDialog'
import { MigrateStackDialog } from '@/pages/docker/components/MigrateStackDialog'
import { CreateStackDialog } from '@/pages/docker/components/CreateStackDialog'
import { StackEditorPanel } from '@/pages/docker/components/StackEditorPanel'
import { StackProgressDialog, useStackProgress } from '@/pages/docker/components/StackProgressDialog'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { useConfirm } from '@/components/ConfirmDialog'
import type { ComposeProjectWithStatus, ComposeService, StackUpdateCheck, RollbackInfo, ClusterNodeStacks } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import ComposeLogs from '@/components/compose/ComposeLogs'
import ContainerLogs from '@/components/docker/ContainerLogs'
import ContainerShell from '@/components/docker/ContainerShell'
import ContainerInspect from '@/components/docker/ContainerInspect'
import { DiffSheet } from '@/components/compose/DiffSheet'

function statusIcon(status: string) {
  switch (status) {
    case 'running':
      return <span className="inline-block w-2 h-2 rounded-full bg-success" />
    case 'partial':
      return <span className="inline-block w-2 h-2 rounded-full bg-warning" />
    default:
      return <span className="inline-block w-2 h-2 rounded-full bg-muted-foreground/40" />
  }
}

function serviceBadge(state: string) {
  const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
  switch (state?.toLowerCase()) {
    case 'running':
      return <span className={`${base} bg-success/10 text-success`}>running</span>
    case 'exited':
      return <span className={`${base} bg-destructive/10 text-destructive`}>exited</span>
    case 'paused':
      return <span className={`${base} bg-warning/10 text-warning`}>paused</span>
    default:
      return <span className={`${base} bg-secondary text-muted-foreground`}>{state || 'unknown'}</span>
  }
}

// Cluster node health dot — a fetch error (stacks couldn't load) takes
// precedence, otherwise the node's reported health (same mapping as nodeStatusColor).
function nodeDot(status: string, error?: string) {
  if (error) return 'bg-destructive'
  switch (status) {
    case 'online': return 'bg-success'
    case 'suspect': return 'bg-warning'
    case 'offline': return 'bg-destructive'
    default: return 'bg-muted-foreground'
  }
}

// Seven-button action strip for a compose service — shared between the desktop
// table row and the mobile card, which used to carry two copies of it.
function ServiceActions({
  svc,
  actionLoading,
  onAction,
  onHealthcheck,
  onInspect,
  onLogs,
  onShell,
  className,
}: {
  svc: ComposeService
  actionLoading: string | null
  onAction: (action: 'restart' | 'stop' | 'start', service: string) => void
  onHealthcheck: (svc: ComposeService) => void
  onInspect: (svc: ComposeService) => void
  onLogs: (svc: ComposeService) => void
  onShell: (svc: ComposeService) => void
  className?: string
}) {
  const { t } = useTranslation()
  const busy = actionLoading === svc.name
  return (
    <div className={cn('flex items-center gap-1', className)}>
      {svc.state === 'running' ? (
        <Button variant="ghost" size="icon-xs" title={t('docker.stacks.stopService')} aria-label={t('docker.stacks.stopService')}
          disabled={busy}
          onClick={() => onAction('stop', svc.name)}>
          {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Square className="h-3.5 w-3.5" />}
        </Button>
      ) : (
        <Button variant="ghost" size="icon-xs" title={t('docker.stacks.startService')} aria-label={t('docker.stacks.startService')}
          disabled={busy}
          onClick={() => onAction('start', svc.name)}>
          {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
        </Button>
      )}
      <Button variant="ghost" size="icon-xs" title={t('docker.stacks.restartService')} aria-label={t('docker.stacks.restartService')}
        disabled={busy}
        onClick={() => onAction('restart', svc.name)}>
        {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RotateCw className="h-3.5 w-3.5" />}
      </Button>
      <Button variant="ghost" size="icon-xs" title={t('compose.healthcheck.title', 'Healthcheck')} aria-label={t('compose.healthcheck.title', 'Healthcheck')}
        onClick={() => onHealthcheck(svc)}>
        <HeartPulse className={`h-3.5 w-3.5 ${svc.has_healthcheck ? 'text-success' : ''}`} />
      </Button>
      <Button variant="ghost" size="icon-xs" title={t('docker.containers.inspect')} aria-label={t('docker.containers.inspect')}
        disabled={!svc.container_id}
        onClick={() => onInspect(svc)}>
        <Info className="h-3.5 w-3.5" />
      </Button>
      <Button variant="ghost" size="icon-xs" title={t('docker.stacks.viewLogs')} aria-label={t('docker.stacks.viewLogs')}
        onClick={() => onLogs(svc)}>
        <ScrollText className="h-3.5 w-3.5" />
      </Button>
      {svc.container_id && svc.state === 'running' && (
        <Button variant="ghost" size="icon-xs" title={t('docker.stacks.openShell')} aria-label={t('docker.stacks.openShell')}
          onClick={() => onShell(svc)}>
          <Terminal className="h-3.5 w-3.5" />
        </Button>
      )}
    </div>
  )
}

// clusterMode renders the cluster-wide master-detail (Cluster › Docker): the left
// list is every node's stacks grouped by node. Selecting one navigates to
// /cluster/stacks/:node/:name; the detail panel is identical to the single-node
// page and scopes its fetches to the route node (api.currentNode = routeNode).
export default function DockerStacks({ clusterMode = false }: { clusterMode?: boolean }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  // In cluster mode the route is /cluster/stacks/:node/:name — the owning node is
  // in the URL (not just api.currentNode), so same-named stacks on different
  // nodes are distinct routes and reloads/deep-links resolve the right node.
  const { name: selectedName, node: routeNode } = useParams()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const basePath = clusterMode ? '/cluster/stacks' : '/docker/stacks'

  const [projects, setProjects] = useState<ComposeProjectWithStatus[]>([])
  const [loading, setLoading] = useState(true)
  const [listError, setListError] = useState<string | null>(null)
  const [services, setServices] = useState<ComposeService[]>([])
  const [servicesLoading, setServicesLoading] = useState(false)
  const [actionLoading, setActionLoading] = useState<string | null>(null)

  // Create dialog
  const [createOpen, setCreateOpen] = useState(false)

  // Node-to-node migration. In single-node mode the source is the current node;
  // in cluster mode the left list passes the stack's own node explicitly.
  const [migrateOpen, setMigrateOpen] = useState(false)
  const [migrateProject, setMigrateProject] = useState('')
  const [migrateSourceNode, setMigrateSourceNode] = useState<string | undefined>(undefined)
  const [clusterNodeCount, setClusterNodeCount] = useState(0)
  useEffect(() => {
    // local: cluster membership is the same from any node; don't proxy this to a
    // remote currentNode just to count nodes.
    api.getClusterStatus(true)
      .then((s) => setClusterNodeCount(s.enabled ? (s.node_count ?? 0) : 0))
      .catch(() => setClusterNodeCount(0))
  }, [])

  // Cluster mode: the left list is every node's stacks grouped by node, and it
  // also resolves the detail's selectedProject.
  const [clusterStacks, setClusterStacks] = useState<ClusterNodeStacks[]>([])
  const [clusterLoading, setClusterLoading] = useState(false)
  const fetchClusterStacks = useCallback(async () => {
    setClusterLoading(true)
    try {
      setClusterStacks(await api.getClusterStacks())
    } catch {
      setClusterStacks([])
    } finally {
      setClusterLoading(false)
    }
  }, [])

  // Single-node mode: resolve the active remote node's name for the header chip.
  const [currentNodeName, setCurrentNodeName] = useState<string | null>(null)
  useEffect(() => {
    if (clusterMode) return
    const nid = api.currentNode
    if (!nid) { setCurrentNodeName(null); return }
    // local: node names are the same from any node — resolve locally instead of
    // proxying this lookup to the remote node we're trying to name.
    api.getClusterNodes(true)
      .then((d) => setCurrentNodeName(d.nodes.find((n) => n.id === nid)?.name ?? null))
      .catch(() => setCurrentNodeName(null))
  }, [clusterMode])

  // The node group the selected stack lives on (cluster mode), resolved from the
  // ROUTE node — deterministic across reloads and same-named stacks on two nodes.
  const selectedNodeGroup = clusterMode
    ? clusterStacks.find((n) => n.node_id === routeNode)
    : undefined
  const detailNodeName = clusterMode
    ? (selectedNodeGroup && !selectedNodeGroup.local ? selectedNodeGroup.node_name : null)
    : currentNodeName

  const openMigrate = (project: string, sourceNode?: string) => {
    setMigrateProject(project)
    setMigrateSourceNode(sourceNode)
    setMigrateOpen(true)
  }

  // Editor state
  const [editYaml, setEditYaml] = useState('')
  // diskYaml mirrors what's actually on disk — kept separate from the
  // Monaco buffer so server-side SHA preconditions (e.g. the healthcheck
  // composer) hash the same content the server will read with os.ReadFile.
  // Using editYaml here would fail the precondition whenever the operator
  // has unsaved edits in the editor.
  const [diskYaml, setDiskYaml] = useState('')
  const [editEnv, setEditEnv] = useState('')
  const [editorTab, setEditorTab] = useState<'compose' | 'env'>('compose')
  const [mainTab, setMainTab] = useState<'services' | 'editor' | 'logs'>('services')
  // Deploy flow (save + up-stream) — the panel's own Save has separate state.
  const [editSaving, setEditSaving] = useState(false)
  const [diffOpen, setDiffOpen] = useState(false)

  // Delete dialog
  const [deleteTarget, setDeleteTarget] = useState<ComposeProjectWithStatus | null>(null)
  const [deleteImages, setDeleteImages] = useState(false)
  const [deleteVolumes, setDeleteVolumes] = useState(false)

  // Image update check
  const [updateCheck, setUpdateCheck] = useState<StackUpdateCheck | null>(null)
  const [checkingUpdates, setCheckingUpdates] = useState(false)
  const [rollingBack, setRollingBack] = useState(false)
  const [rollbackInfo, setRollbackInfo] = useState<RollbackInfo | null>(null)
  const [healthcheckTarget, setHealthcheckTarget] = useState<ComposeService | null>(null)

  // Deploy/update progress modal (shared stream handler)
  const { progress, runProgressStream, closeProgress } = useStackProgress()

  // Service logs/shell dialogs
  const [logService, setLogService] = useState<ComposeService | null>(null)
  const [shellService, setShellService] = useState<ComposeService | null>(null)
  const [inspectService, setInspectService] = useState<ComposeService | null>(null)

  // In cluster mode the selected stack lives in its node group (it isn't in the
  // local `projects` list); otherwise resolve from the single-node list.
  const selectedProject = clusterMode
    ? selectedNodeGroup?.stacks.find(p => p.name === selectedName)
    : projects.find(p => p.name === selectedName)

  const fetchProjects = useCallback(async (showLoading = true) => {
    try {
      if (showLoading) setLoading(true)
      setListError(null)
      const data = await api.getComposeProjects()
      setProjects(data || [])
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('docker.compose.fetchFailed')
      setListError(msg)
      toast.error(msg)
    } finally {
      if (showLoading) setLoading(false)
    }
  }, [t])

  // After list-affecting actions, refresh whichever list is showing so statuses
  // (and selectedProject in cluster mode) stay current.
  const refreshList = useCallback(
    (showLoading = false) => (clusterMode ? fetchClusterStacks() : fetchProjects(showLoading)),
    [clusterMode, fetchClusterStacks, fetchProjects],
  )

  // Tracks the latest stack the user is looking at, so that in-flight fetches for
  // a previously-selected stack don't overwrite the services list when they resolve
  // late (rapid stack switching produced a visible mix of two stacks' services).
  const latestSelectedRef = useRef<string>('')

  const fetchServices = useCallback(async (name: string) => {
    try {
      setServicesLoading(true)
      const data = await api.getComposeServices(name)
      if (latestSelectedRef.current !== name) return
      setServices(data || [])
    } catch {
      if (latestSelectedRef.current !== name) return
      setServices([])
    } finally {
      if (latestSelectedRef.current === name) setServicesLoading(false)
    }
  }, [])

  useEffect(() => {
    if (clusterMode) fetchClusterStacks()
    else fetchProjects()
  }, [clusterMode, fetchClusterStacks, fetchProjects])

  useEffect(() => {
    // Create flow is single-node only (the cluster master-detail has no '+');
    // its route /cluster/stacks/NAME wouldn't match the :node/:name detail route.
    if (!clusterMode && searchParams.get('new') === '1') {
      setCreateOpen(true)
      setSearchParams({}, { replace: true })
    }
  }, [clusterMode, searchParams, setSearchParams])

  // Tracks the node we last pointed api.currentNode at (cluster mode), so the
  // unmount cleanup only clears it if the sidebar didn't re-point it meanwhile.
  const lastClusterNode = useRef<string | null>(null)

  useEffect(() => {
    // Cluster mode: the route node is authoritative — scope every detail fetch
    // and action to it. Keying on routeNode makes the panel reload when only the
    // node changes (same-named stack on a different node).
    if (clusterMode && routeNode) {
      api.setCurrentNode(routeNode)
      lastClusterNode.current = routeNode
    }
    if (selectedName) {
      latestSelectedRef.current = selectedName
      // Clear stale services from a previously-selected stack so the row doesn't
      // briefly show another stack's services during the new fetch.
      setServices([])
      fetchServices(selectedName)
      // Load YAML
      api.getComposeProject(selectedName).then(data => {
        setEditYaml(data.yaml)
        setDiskYaml(data.yaml)
      }).catch(() => {})
      // Load .env
      api.getComposeEnv(selectedName).then(data => {
        setEditEnv(data.content)
      }).catch(() => {})
    } else {
      latestSelectedRef.current = ''
    }
  }, [selectedName, routeNode, clusterMode, fetchServices])

  // Don't leak the in-page node selection to the rest of the app on leave —
  // unless the sidebar tree re-pointed currentNode while navigating away.
  useEffect(() => {
    if (!clusterMode) return
    return () => {
      if (api.currentNode && api.currentNode === lastClusterNode.current) {
        api.setCurrentNode(null)
      }
    }
  }, [clusterMode])

  const handleUp = async (name: string) => {
    try {
      await runProgressStream(t('docker.stacks.deploying'), (onEvent) => api.composeUpStream(name, onEvent))
      toast.success(t('docker.compose.upSuccess', { name }))
      await Promise.all([
        refreshList(),
        selectedName === name ? fetchServices(name) : Promise.resolve(),
      ])
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.compose.upFailed'))
    }
  }

  const handleDown = async (name: string) => {
    setActionLoading(name)
    try {
      await api.composeDown(name)
      toast.success(t('docker.compose.downSuccess', { name }))
      await Promise.all([
        refreshList(),
        selectedName === name ? fetchServices(name) : Promise.resolve(),
      ])
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.compose.downFailed'))
    } finally {
      setActionLoading(null)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setActionLoading(deleteTarget.name)
    try {
      await api.deleteComposeProject(deleteTarget.name, {
        removeImages: deleteImages,
        removeVolumes: deleteVolumes,
      })
      toast.success(t('docker.compose.deleted'))
      setDeleteTarget(null)
      setDeleteImages(false)
      setDeleteVolumes(false)
      if (selectedName === deleteTarget.name) navigate(basePath)
      await refreshList(true)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.compose.deleteFailed'))
    } finally {
      setActionLoading(null)
    }
  }

  const handleDeploy = async () => {
    if (!selectedName || !editYaml.trim()) return
    setEditSaving(true)
    try {
      await api.updateComposeProject(selectedName, editYaml)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.saveFailed'))
      setEditSaving(false)
      return
    }
    setEditSaving(false)

    // Open progress modal and stream
    try {
      await runProgressStream(t('docker.stacks.deploying'), (onEvent) => api.composeUpStream(selectedName, onEvent))
      toast.success(t('docker.stacks.deploySuccess'))
      await Promise.all([refreshList(), fetchServices(selectedName)])
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.deployFailed'))
    }
  }

  // Reset update check and rollback when the stack OR its node changes
  useEffect(() => {
    setUpdateCheck(null)
    if (selectedName) {
      api.hasRollback(selectedName).then(r => setRollbackInfo(r)).catch(() => setRollbackInfo(null))
    } else {
      setRollbackInfo(null)
    }
  }, [selectedName, routeNode])

  const handleCheckUpdates = async () => {
    if (!selectedName) return
    setCheckingUpdates(true)
    setUpdateCheck(null)
    try {
      const result = await api.checkStackUpdates(selectedName)
      setUpdateCheck(result)
      if (result.has_updates) {
        toast.info(t('docker.stacks.updateAvailable'))
      } else {
        toast.success(t('docker.stacks.upToDate'))
      }
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.updateFailed'))
    } finally {
      setCheckingUpdates(false)
    }
  }

  const handleUpdate = async () => {
    if (!selectedName) return
    try {
      await runProgressStream(t('docker.stacks.updating'), (onEvent) => api.updateStackStream(selectedName, onEvent))
      toast.success(t('docker.stacks.updateSuccess'))
      setUpdateCheck(null)
      api.hasRollback(selectedName).then(r => setRollbackInfo(r)).catch(() => {})
      await Promise.all([refreshList(), fetchServices(selectedName)])
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.updateFailed'))
    }
  }

  const handleRollback = async () => {
    if (!selectedName) return
    const detailLines = rollbackInfo?.details?.map(e => {
      const prev = e.prev_image_id.substring(7, 19)
      const curr = e.curr_image_id ? e.curr_image_id.substring(7, 19) : '?'
      return `  ${e.service}: ${curr} → ${prev}`
    }).join('\n') || ''
    if (!(await confirm({ title: t('docker.stacks.confirmRollback'), description: detailLines, danger: true }))) return
    setRollingBack(true)
    try {
      await api.rollbackStack(selectedName)
      toast.success(t('docker.stacks.rollbackSuccess'))
      setUpdateCheck(null)
      setRollbackInfo(null)
      await Promise.all([refreshList(), fetchServices(selectedName)])
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.rollbackFailed'))
    } finally {
      setRollingBack(false)
    }
  }

  const handleServiceAction = async (action: 'restart' | 'stop' | 'start', service: string) => {
    if (!selectedName) return
    setActionLoading(service)
    try {
      if (action === 'restart') await api.restartComposeService(selectedName, service)
      else if (action === 'stop') await api.stopComposeService(selectedName, service)
      else if (action === 'start') await api.startComposeService(selectedName, service)
      toast.success(t(`docker.stacks.${action}Success`))
      await Promise.all([fetchServices(selectedName), refreshList()])
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.actionFailed'))
    } finally {
      setActionLoading(null)
    }
  }

  return (
    <div className="flex flex-col md:flex-row gap-4 h-full">
      {/* Stack list (left panel) — hidden on mobile when a stack is selected.
          In cluster mode it lists every node's stacks grouped by node. */}
      <div className={`md:w-[220px] shrink-0 space-y-2 ${selectedName ? 'hidden md:block' : ''}`}>
        <div className="flex items-center justify-between">
          <span className="text-[15px] font-semibold">{t('docker.stacks.title')}</span>
          <div className="flex gap-1">
            <Button variant="ghost" size="icon-xs" aria-label={t('common.refresh')} onClick={() => (clusterMode ? fetchClusterStacks() : fetchProjects())} disabled={clusterMode ? clusterLoading : loading}>
              <RefreshCw className={`h-3.5 w-3.5 ${(clusterMode ? clusterLoading : loading) ? 'animate-spin' : ''}`} />
            </Button>
            {!clusterMode && (
              <Button variant="ghost" size="icon-xs" aria-label={t('docker.compose.createTitle')} onClick={() => setCreateOpen(true)}>
                <Plus className="h-3.5 w-3.5" />
              </Button>
            )}
          </div>
        </div>

        {clusterMode ? (
          /* Cluster-wide list: every node's stacks grouped by node (responsive) */
          <div className="space-y-1">
            {clusterLoading && clusterStacks.length === 0 && (
              <p className="text-[13px] text-muted-foreground py-4 text-center">{t('common.loading')}</p>
            )}
            {!clusterLoading && clusterStacks.length === 0 && (
              <p className="text-[13px] text-muted-foreground py-4 text-center">{t('docker.stacks.noStacks')}</p>
            )}
            {clusterStacks.map(node => (
              <div key={node.node_id} className="space-y-0.5">
                <div className="flex items-center gap-1.5 px-2 pt-2 text-[11px] font-medium text-muted-foreground">
                  <span className={`h-1.5 w-1.5 rounded-full shrink-0 ${nodeDot(node.status, node.error)}`} />
                  <span className="truncate">{node.node_name}</span>
                  {node.local && <span className="text-muted-foreground/70 shrink-0">· {t('layout.cluster.localNode')}</span>}
                  {node.error && (
                    <span className="shrink-0 inline-flex items-center gap-0.5 text-warning">
                      <AlertTriangle className="h-3 w-3" />
                      {node.error === 'unreachable' ? t('cluster.stacks.nodeUnreachable') : node.error === 'list_failed' ? t('cluster.stacks.nodeListFailed') : node.error}
                    </span>
                  )}
                </div>
                {node.stacks.map(p => {
                  const isSel = selectedName === p.name && routeNode === node.node_id
                  return (
                    <div key={node.node_id + '/' + p.name} className={`group flex items-center gap-2 px-3 py-2 rounded-xl ${isSel ? 'bg-primary/10 ring-1 ring-primary/20' : 'hover:bg-secondary/50'}`}>
                      {statusIcon(p.real_status)}
                      <button
                        type="button"
                        className="text-[13px] font-medium truncate min-w-0 flex-1 text-left rounded hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
                        onClick={() => navigate(`${basePath}/${node.node_id}/${p.name}`)}
                      >{p.name}</button>
                      <span className="text-[11px] text-muted-foreground shrink-0">{p.running_count}/{p.service_count}</span>
                      <button
                        type="button"
                        title={t('docker.migrate.action')}
                        aria-label={t('docker.migrate.action')}
                        className="shrink-0 rounded text-muted-foreground opacity-60 transition hover:text-primary group-hover:opacity-100 focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
                        onClick={() => openMigrate(p.name, node.node_id)}
                      >
                        <ArrowRightLeft className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  )
                })}
                {node.stacks.length === 0 && !node.error && (
                  <p className="px-3 py-1 text-[12px] text-muted-foreground/70">{t('docker.stacks.noStacks')}</p>
                )}
              </div>
            ))}
          </div>
        ) : (
        <>
        {/* Load failure (docker-family error-state pattern, narrow-panel layout) */}
        {listError && projects.length === 0 && !loading && (
          <div className="bg-destructive/10 text-destructive rounded-xl p-3 space-y-2">
            <div className="flex items-start gap-2">
              <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
              <div className="min-w-0 flex-1">
                <p className="text-[13px] font-medium">{t('docker.stacks.loadError', 'Failed to load stacks')}</p>
                <p className="text-[12px] opacity-80 mt-0.5 break-words">{listError}</p>
              </div>
            </div>
            <Button variant="outline" size="sm" className="rounded-xl w-full" onClick={() => fetchProjects()}>
              <RefreshCw className="h-3.5 w-3.5" />
              {t('common.retry')}
            </Button>
          </div>
        )}
        {/* Desktop stack list */}
        <div className="hidden md:block space-y-1">
          {projects.length === 0 && !loading && !listError && (
            <p className="text-[13px] text-muted-foreground py-4 text-center">{t('docker.stacks.noStacks')}</p>
          )}
          {projects.map(p => (
            <div
              key={p.name}
              role="button"
              tabIndex={0}
              className={`flex items-center gap-2 px-3 py-2 rounded-xl cursor-pointer transition-all duration-200 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
                selectedName === p.name
                  ? 'bg-primary/10 ring-1 ring-primary/20'
                  : 'hover:bg-secondary/50'
              }`}
              onClick={() => navigate(`${basePath}/${p.name}`)}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate(`${basePath}/${p.name}`) } }}
            >
              {statusIcon(p.real_status)}
              <span className="text-[13px] font-medium truncate min-w-0 flex-1">{p.name}</span>
              <span className="text-[11px] text-muted-foreground shrink-0">
                {p.running_count}/{p.service_count}
              </span>
            </div>
          ))}
        </div>

        {/* Mobile stack cards */}
        <div className="md:hidden space-y-2">
          {projects.length === 0 && !loading && !listError && (
            <p className="text-[13px] text-muted-foreground py-4 text-center">{t('docker.stacks.noStacks')}</p>
          )}
          {projects.map(p => (
            <div
              key={p.name}
              className={`bg-card rounded-2xl p-4 card-shadow ${
                selectedName === p.name ? 'ring-1 ring-primary/20' : ''
              }`}
            >
              <div
                role="button"
                tabIndex={0}
                className="flex items-center gap-2 cursor-pointer rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                onClick={() => navigate(`${basePath}/${p.name}`)}
                onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate(`${basePath}/${p.name}`) } }}
              >
                {statusIcon(p.real_status)}
                <span className="text-[13px] font-medium truncate min-w-0 flex-1">{p.name}</span>
                <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium shrink-0 ${
                  p.real_status === 'running' ? 'bg-success/10 text-success' :
                  p.real_status === 'partial' ? 'bg-warning/10 text-warning' :
                  'bg-secondary text-muted-foreground'
                }`}>
                  {p.running_count}/{p.service_count}
                </span>
              </div>
              <div className="flex items-center gap-1 mt-2 pt-2 border-t border-border/50">
                {p.real_status !== 'running' && (
                  <Button
                    size="sm" variant="ghost"
                    className="rounded-xl h-7 px-2 text-[11px] text-success"
                    disabled={actionLoading === p.name}
                    onClick={() => handleUp(p.name)}
                  >
                    {actionLoading === p.name ? <Loader2 className="h-3 w-3 animate-spin" /> : <Play className="h-3 w-3" />}
                    {t('docker.compose.up')}
                  </Button>
                )}
                {(p.real_status === 'running' || p.real_status === 'partial') && (
                  <>
                    <Button
                      size="sm" variant="ghost"
                      className="rounded-xl h-7 px-2 text-[11px] text-destructive"
                      disabled={actionLoading === p.name}
                      onClick={() => handleDown(p.name)}
                    >
                      {actionLoading === p.name ? <Loader2 className="h-3 w-3 animate-spin" /> : <Square className="h-3 w-3" />}
                      {t('docker.compose.down')}
                    </Button>
                    <Button
                      size="sm" variant="ghost"
                      className="rounded-xl h-7 px-2 text-[11px]"
                      disabled={actionLoading === p.name}
                      onClick={() => handleUp(p.name)}
                    >
                      {actionLoading === p.name ? <Loader2 className="h-3 w-3 animate-spin" /> : <RotateCw className="h-3 w-3" />}
                      {t('docker.stacks.redeploy')}
                    </Button>
                  </>
                )}
                <div className="flex-1" />
                <Button
                  size="sm" variant="ghost"
                  className="rounded-xl h-7 px-2 text-[11px]"
                  onClick={() => navigate(`${basePath}/${p.name}`)}
                >
                  <FileCode className="h-3 w-3" />
                  {t('docker.stacks.editor')}
                </Button>
                <Button
                  variant="ghost" size="icon-xs"
                  aria-label={t('common.delete')}
                  onClick={() => setDeleteTarget(p)}
                >
                  <Trash2 className="h-3 w-3" />
                </Button>
              </div>
            </div>
          ))}
        </div>
        </>
        )}
      </div>

      {/* Stack detail (right panel) */}
      <div className="flex-1 min-w-0">
        {!selectedName ? (
          <div className="hidden md:flex items-center justify-center h-64 text-muted-foreground text-[13px]">
            {t('docker.stacks.selectStack')}
          </div>
        ) : (
          <div className="space-y-4">
            {/* Stack header */}
            <div className="space-y-2">
              <div className="flex items-center gap-3 flex-wrap">
                <Button
                  variant="ghost" size="icon-xs"
                  className="md:hidden"
                  onClick={() => navigate(basePath)}
                >
                  <ChevronLeft className="h-4 w-4" />
                </Button>
                <h2 className="text-[18px] font-bold truncate min-w-0 max-w-full">{selectedName}</h2>
                {detailNodeName && (
                  <span
                    className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium bg-primary/10 text-primary shrink-0"
                    title={t('docker.stacks.onNode', { node: detailNodeName })}
                  >
                    <Monitor className="h-3 w-3" />
                    {detailNodeName}
                  </span>
                )}
                {selectedProject && (
                  <>
                    <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium shrink-0 ${
                      selectedProject.real_status === 'running' ? 'bg-success/10 text-success' :
                      selectedProject.real_status === 'partial' ? 'bg-warning/10 text-warning' :
                      'bg-secondary text-muted-foreground'
                    }`}>
                      {t(`docker.stacks.${selectedProject.real_status}`)}
                    </span>
                    <span className="text-[11px] text-muted-foreground font-mono hidden sm:inline truncate min-w-0" title={selectedProject.path}>
                      {selectedProject.path}
                    </span>
                  </>
                )}
              </div>
              <div className="flex items-center gap-2 flex-wrap">
                {selectedProject?.real_status !== 'running' && (
                  <Button
                    size="sm"
                    className="rounded-xl bg-success hover:bg-success/90 text-white"
                    disabled={actionLoading === selectedName}
                    onClick={() => handleUp(selectedName)}
                  >
                    {actionLoading === selectedName ? (
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                      <Play className="h-3.5 w-3.5" />
                    )}
                    {t('docker.compose.up')}
                  </Button>
                )}
                {selectedProject?.real_status === 'running' || selectedProject?.real_status === 'partial' ? (
                  <Button
                    variant="outline" size="sm"
                    className="rounded-xl border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
                    disabled={actionLoading === selectedName}
                    onClick={() => handleDown(selectedName)}
                  >
                    {actionLoading === selectedName ? (
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                      <Square className="h-3.5 w-3.5" />
                    )}
                    {t('docker.compose.down')}
                  </Button>
                ) : null}
                {selectedProject?.real_status === 'running' && (
                  <Button
                    variant="outline" size="sm" className="rounded-xl"
                    disabled={actionLoading === selectedName}
                    onClick={() => handleUp(selectedName)}
                  >
                    {actionLoading === selectedName ? (
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                      <RotateCw className="h-3.5 w-3.5" />
                    )}
                    {t('docker.stacks.redeploy')}
                  </Button>
                )}
                <Button
                  variant="outline" size="sm" className="rounded-xl"
                  disabled={checkingUpdates}
                  onClick={handleCheckUpdates}
                >
                  {checkingUpdates ? (
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  ) : (
                    <Search className="h-3.5 w-3.5" />
                  )}
                  {t('docker.stacks.checkUpdates')}
                </Button>
                {clusterNodeCount > 1 && (
                  <Button
                    variant="outline" size="sm" className="rounded-xl"
                    onClick={() => selectedName && openMigrate(selectedName, clusterMode ? routeNode : undefined)}
                  >
                    <ArrowRightLeft className="h-3.5 w-3.5" />
                    {t('docker.migrate.action')}
                  </Button>
                )}
                {rollbackInfo?.has_rollback && (
                  <div className="flex items-center gap-2">
                    <Button
                      variant="outline" size="sm"
                      className="rounded-xl border-warning/30 text-warning hover:bg-warning/10 hover:text-warning"
                      disabled={rollingBack}
                      onClick={handleRollback}
                      title={rollbackInfo.details?.map(e => {
                        const prev = e.prev_image_id.substring(7, 19)
                        const curr = e.curr_image_id ? e.curr_image_id.substring(7, 19) : '?'
                        return `${e.service}: ${curr} → ${prev}`
                      }).join('\n')}
                    >
                      {rollingBack ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      ) : (
                        <Undo2 className="h-3.5 w-3.5" />
                      )}
                      {t('docker.stacks.rollback')}
                    </Button>
                    {rollbackInfo.details && rollbackInfo.details.length > 0 && (
                      <span className="text-[11px] text-muted-foreground font-mono hidden md:inline">
                        {rollbackInfo.details.map(e => {
                          const prev = e.prev_image_id.substring(7, 19)
                          const curr = e.curr_image_id ? e.curr_image_id.substring(7, 19) : '?'
                          return `${curr} → ${prev}`
                        }).join(', ')}
                      </span>
                    )}
                  </div>
                )}
                <Button
                  variant="ghost" size="icon-xs"
                  aria-label={t('common.delete')}
                  onClick={() => setDeleteTarget(selectedProject || null)}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            </div>

            {/* Update check results */}
            {updateCheck && (
              <div className={`rounded-xl p-4 ${
                updateCheck.has_updates
                  ? 'bg-primary/5 ring-1 ring-primary/20'
                  : 'bg-success/5 ring-1 ring-success/20'
              }`}>
                <div className="flex items-center justify-between mb-2">
                  <span className="text-[13px] font-semibold">
                    {t('docker.stacks.imageUpdates')}
                  </span>
                  {updateCheck.has_updates && (
                    <Button
                      size="sm" className="rounded-xl bg-primary hover:bg-primary/90 text-white"
                      disabled={progress.open && !progress.done}
                      onClick={handleUpdate}
                    >
                      {progress.open && !progress.done ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      ) : (
                        <Download className="h-3.5 w-3.5" />
                      )}
                      {t('docker.stacks.updateAll')}
                    </Button>
                  )}
                </div>
                <div className="space-y-1">
                  {updateCheck.images.map(img => (
                    <div key={img.image} className="flex items-center gap-2 text-[13px] min-w-0">
                      {img.error ? (
                        <>
                          <XCircle className="h-3.5 w-3.5 text-destructive shrink-0" />
                          <span className="font-mono text-[12px] truncate min-w-0 flex-1" title={img.image}>{img.image}</span>
                          <span className="text-[11px] text-destructive shrink-0">{t('docker.stacks.registryError')}</span>
                        </>
                      ) : img.has_update ? (
                        <div className="flex flex-col gap-0.5 min-w-0 flex-1">
                          <div className="flex items-center gap-2 min-w-0">
                            <span className="inline-block w-2 h-2 rounded-full bg-primary shrink-0" />
                            <span className="font-mono text-[12px] truncate min-w-0 flex-1" title={img.image}>{img.image}</span>
                            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-primary/10 text-primary shrink-0">
                              {t('docker.stacks.updateAvailable')}
                            </span>
                          </div>
                          {img.current_digest && img.remote_digest && (
                            <span className="pl-4 font-mono text-[11px] text-muted-foreground truncate">
                              {img.current_digest} → {img.remote_digest}
                            </span>
                          )}
                        </div>
                      ) : (
                        <>
                          <CheckCircle2 className="h-3.5 w-3.5 text-success shrink-0" />
                          <span className="font-mono text-[12px] truncate min-w-0 flex-1" title={img.image}>{img.image}</span>
                          <span className="text-[11px] text-muted-foreground shrink-0">{t('docker.stacks.upToDate')}</span>
                        </>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Tabs */}
            <Tabs value={mainTab} onValueChange={(v) => setMainTab(v as 'services' | 'editor' | 'logs')}>
              <TabsList className="bg-secondary/50 rounded-xl p-1">
                <TabsTrigger value="services" className="rounded-lg text-[13px] data-[state=active]:text-success">
                  <Play className="h-3.5 w-3.5 mr-1" />
                  {t('docker.stacks.services')}
                </TabsTrigger>
                <TabsTrigger value="editor" className="rounded-lg text-[13px] data-[state=active]:text-primary">
                  <FileCode className="h-3.5 w-3.5 mr-1" />
                  {t('docker.stacks.editor')}
                </TabsTrigger>
                <TabsTrigger value="logs" className="rounded-lg text-[13px] data-[state=active]:text-warning">
                  <ScrollText className="h-3.5 w-3.5 mr-1" />
                  {t('docker.stacks.logs')}
                </TabsTrigger>
              </TabsList>

              <TabsContent value="services">
                {/* Desktop table */}
                <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden border-t-2 border-t-success">
                  <Table>
                    <TableHeader>
                      <TableRow className="border-border/50">
                        <TableHead className="text-[11px]">{t('common.name')}</TableHead>
                        <TableHead className="text-[11px]">{t('docker.containers.image')}</TableHead>
                        <TableHead className="text-[11px]">{t('common.status')}</TableHead>
                        <TableHead className="text-[11px]">{t('docker.containers.ports')}</TableHead>
                        <TableHead className="text-right text-[11px]">{t('common.actions')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {servicesLoading && (
                        <TableRow>
                          <TableCell colSpan={5} className="text-center text-muted-foreground py-8">{t('common.loading')}</TableCell>
                        </TableRow>
                      )}
                      {!servicesLoading && services.length === 0 && (
                        <TableRow>
                          <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                            {t('docker.stacks.noServices')}
                          </TableCell>
                        </TableRow>
                      )}
                      {services.map(svc => (
                        <TableRow key={svc.name}>
                          <TableCell className="font-medium text-[13px]">
                            {svc.container_id ? (
                              <button onClick={() => setInspectService(svc)} title={t('docker.containers.inspect')}
                                className="text-left hover:text-primary hover:underline max-w-full truncate rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0">{svc.name}</button>
                            ) : svc.name}
                          </TableCell>
                          <TableCell className="text-muted-foreground text-xs font-mono">{svc.image}</TableCell>
                          <TableCell>{serviceBadge(svc.state)}</TableCell>
                          <TableCell className="text-muted-foreground text-xs font-mono">{svc.ports || '-'}</TableCell>
                          <TableCell className="text-right">
                            <ServiceActions
                              svc={svc}
                              actionLoading={actionLoading}
                              onAction={handleServiceAction}
                              onHealthcheck={setHealthcheckTarget}
                              onInspect={setInspectService}
                              onLogs={setLogService}
                              onShell={setShellService}
                              className="justify-end"
                            />
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>

                {/* Mobile service cards */}
                <div className="md:hidden space-y-2">
                  {servicesLoading && (
                    <p className="text-center text-muted-foreground py-8 text-[13px]">{t('common.loading')}</p>
                  )}
                  {!servicesLoading && services.length === 0 && (
                    <p className="text-center text-muted-foreground py-8 text-[13px]">
                      {t('docker.stacks.noServices')}
                    </p>
                  )}
                  {services.map(svc => (
                    <div key={svc.name} className="bg-card rounded-2xl p-4 card-shadow">
                      <div className="flex items-center gap-2 mb-2 min-w-0">
                        {svc.container_id ? (
                          <button onClick={() => setInspectService(svc)} title={t('docker.containers.inspect')}
                            className="text-[13px] font-medium truncate min-w-0 flex-1 text-left hover:text-primary rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0">{svc.name}</button>
                        ) : (
                          <span className="text-[13px] font-medium truncate min-w-0 flex-1">{svc.name}</span>
                        )}
                        {serviceBadge(svc.state)}
                      </div>
                      <div className="text-[11px] text-muted-foreground font-mono truncate mb-1" title={svc.image}>{svc.image}</div>
                      {svc.ports && (
                        <div className="text-[11px] text-muted-foreground font-mono truncate mb-2">{svc.ports}</div>
                      )}
                      <ServiceActions
                        svc={svc}
                        actionLoading={actionLoading}
                        onAction={handleServiceAction}
                        onHealthcheck={setHealthcheckTarget}
                        onInspect={setInspectService}
                        onLogs={setLogService}
                        onShell={setShellService}
                        className="pt-2 border-t border-border/50"
                      />
                    </div>
                  ))}
                </div>
              </TabsContent>

              <TabsContent value="editor">
                <StackEditorPanel
                  project={selectedName}
                  composeFileName={selectedProject?.compose_file || 'docker-compose.yml'}
                  yaml={editYaml}
                  onYamlChange={setEditYaml}
                  env={editEnv}
                  onEnvChange={setEditEnv}
                  tab={editorTab}
                  onTabChange={setEditorTab}
                  deploying={editSaving}
                  onDeploy={handleDeploy}
                  onOpenDiff={() => setDiffOpen(true)}
                  onEnvSaved={() => { void refreshList() }}
                />
              </TabsContent>

              <TabsContent value="logs">
                {selectedName && (
                  <ComposeLogs
                    project={selectedName}
                    serviceNames={services.map(s => s.name)}
                  />
                )}
              </TabsContent>
            </Tabs>
          </div>
        )}
      </div>

      {/* Create project dialog */}
      <CreateStackDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={async (projectName) => {
          await fetchProjects()
          navigate(`${basePath}/${projectName}`)
        }}
      />

      {/* Delete dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => {
        if (!open) {
          setDeleteTarget(null)
          setDeleteImages(false)
          setDeleteVolumes(false)
        }
      }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('docker.compose.deleteTitle')}</DialogTitle>
            <DialogDescription>
              <Trans i18nKey="docker.compose.deleteConfirm" values={{ name: deleteTarget?.name ?? '' }}
                components={{ strong: <span className="font-semibold" /> }} />
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <label className="flex items-center gap-3 cursor-pointer group">
              <input
                type="checkbox"
                checked={deleteImages}
                onChange={(e) => setDeleteImages(e.target.checked)}
                className="h-4 w-4 rounded border-border accent-destructive"
              />
              <div>
                <p className="text-[13px] font-medium group-hover:text-foreground transition-colors">{t('docker.compose.deleteImages')}</p>
                <p className="text-[11px] text-muted-foreground">{t('docker.compose.deleteImagesDesc')}</p>
              </div>
            </label>
            <label className="flex items-center gap-3 cursor-pointer group">
              <input
                type="checkbox"
                checked={deleteVolumes}
                onChange={(e) => setDeleteVolumes(e.target.checked)}
                className="h-4 w-4 rounded border-border accent-destructive"
              />
              <div>
                <p className="text-[13px] font-medium group-hover:text-foreground transition-colors">{t('docker.compose.deleteVolumes')}</p>
                <p className="text-[11px] text-muted-foreground">{t('docker.compose.deleteVolumesDesc')}</p>
              </div>
            </label>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>{t('common.cancel')}</Button>
            <Button variant="destructive" onClick={handleDelete} disabled={actionLoading === deleteTarget?.name}>
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Service logs dialog */}
      <Dialog open={!!logService} onOpenChange={(open) => !open && setLogService(null)}>
        <DialogContent className="w-[calc(100vw-2rem)] md:w-full sm:max-w-3xl h-[90vh] md:h-[80vh]">
          <DialogHeader>
            <DialogTitle className="truncate">{logService?.name} — {t('docker.stacks.logs')}</DialogTitle>
          </DialogHeader>
          {logService?.container_id && <ContainerLogs containerId={logService.container_id} />}
        </DialogContent>
      </Dialog>

      {/* Service shell dialog */}
      <Dialog open={!!inspectService} onOpenChange={(open) => !open && setInspectService(null)}>
        <DialogContent className="w-[calc(100vw-2rem)] md:w-full sm:max-w-4xl max-h-[90vh] overflow-y-auto overflow-x-hidden">
          <DialogHeader className="min-w-0">
            <DialogTitle className="truncate">{inspectService?.name} — {t('docker.containers.inspect')}</DialogTitle>
          </DialogHeader>
          {inspectService?.container_id && <ContainerInspect containerId={inspectService.container_id} />}
        </DialogContent>
      </Dialog>

      <Dialog open={!!shellService} onOpenChange={(open) => !open && setShellService(null)}>
        <DialogContent className="w-[calc(100vw-2rem)] md:w-full sm:max-w-3xl h-[90vh] md:h-[80vh]">
          <DialogHeader>
            <DialogTitle className="truncate">{shellService?.name} — {t('docker.containers.shell')}</DialogTitle>
          </DialogHeader>
          {shellService?.container_id && <ContainerShell containerId={shellService.container_id} />}
        </DialogContent>
      </Dialog>

      {/* Deploy/Update progress modal */}
      <StackProgressDialog progress={progress} onClose={closeProgress} />

      {/* Diff preview sheet */}
      {selectedName && (
        <DiffSheet
          open={diffOpen}
          onOpenChange={setDiffOpen}
          projectName={selectedName}
          proposedYaml={editYaml}
          onApply={() => {
            setDiffOpen(false)
            handleDeploy()
          }}
        />
      )}

      {/* Healthcheck composer */}
      {selectedName && healthcheckTarget && (
        <HealthcheckComposerDialog
          open={!!healthcheckTarget}
          onOpenChange={(open) => !open && setHealthcheckTarget(null)}
          project={selectedName}
          service={healthcheckTarget.name}
          baseYaml={diskYaml}
          onApplied={(newYaml) => {
            setEditYaml(newYaml)
            setDiskYaml(newYaml)
            setMainTab('editor')
            setEditorTab('compose')
            setHealthcheckTarget(null)
            // Refresh services so the HeartPulse indicator color reflects the new state.
            void fetchServices(selectedName)
          }}
        />
      )}

      <MigrateStackDialog
        open={migrateOpen}
        onOpenChange={setMigrateOpen}
        project={migrateProject}
        sourceNodeId={migrateSourceNode}
        onMigrated={() => {
          // Stack moved off this node — refresh the list, and if it was the open
          // stack clear the now-stale detail route (matches the delete flow).
          void refreshList()
          if (migrateProject && migrateProject === selectedName) navigate(basePath)
        }}
      />
    </div>
  )
}
