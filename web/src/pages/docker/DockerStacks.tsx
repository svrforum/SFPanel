import { useState, useEffect, useCallback, useRef } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useParams, useNavigate, useSearchParams } from 'react-router-dom'
import {
  Plus, Play, Square, RotateCw, ArrowUp, RefreshCw,
  Trash2, Terminal, ScrollText, FileText, FileCode, Save, Loader2,
  CheckCircle2, XCircle, Download, Undo2, Search, ChevronLeft, Eye,
  HeartPulse, Info, ArrowRightLeft, Monitor, AlertTriangle,
} from 'lucide-react'
import { HealthcheckComposerDialog } from '@/components/compose/HealthcheckComposerDialog'
import { MigrateStackDialog } from '@/pages/docker/components/MigrateStackDialog'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import type { ComposeProjectWithStatus, ComposeService, StackUpdateCheck, RollbackInfo, ClusterNodeStacks } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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
import ComposeEditor from '@/components/ComposeEditor'
import ComposeLogs from '@/components/ComposeLogs'
import ContainerLogs from '@/components/ContainerLogs'
import ContainerShell from '@/components/ContainerShell'
import ContainerInspect from '@/components/ContainerInspect'
import { DiffSheet } from '@/components/compose/DiffSheet'
import { GitImportForm } from '@/components/compose/GitImportForm'

const DEFAULT_COMPOSE = `services:
  app:
    image: nginx:latest
    ports:
      - "8080:80"
`

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
// precedence, otherwise the node's reported health. Mirrors NodeSelector.
function nodeDot(status: string, error?: string) {
  if (error) return 'bg-destructive'
  switch (status) {
    case 'online': return 'bg-success'
    case 'suspect': return 'bg-warning'
    case 'offline': return 'bg-destructive'
    default: return 'bg-muted-foreground'
  }
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
  const [services, setServices] = useState<ComposeService[]>([])
  const [servicesLoading, setServicesLoading] = useState(false)
  const [actionLoading, setActionLoading] = useState<string | null>(null)

  // Create dialog
  const [createOpen, setCreateOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newYaml, setNewYaml] = useState(DEFAULT_COMPOSE)
  const [creating, setCreating] = useState(false)

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
  const [editSaving, setEditSaving] = useState(false)
  const [envSaving, setEnvSaving] = useState(false)
  const [validating, setValidating] = useState(false)
  const [validationResult, setValidationResult] = useState<{ valid: boolean; message: string } | null>(null)
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

  // Progress modal
  const [progressOpen, setProgressOpen] = useState(false)
  const [progressTitle, setProgressTitle] = useState('')
  const [progressLines, setProgressLines] = useState<string[]>([])
  const [progressDone, setProgressDone] = useState(false)
  const [progressError, setProgressError] = useState(false)
  const progressEndRef = useRef<HTMLDivElement>(null)

  // Service logs/shell dialogs
  const [logService, setLogService] = useState<ComposeService | null>(null)
  const [shellService, setShellService] = useState<ComposeService | null>(null)
  const [inspectService, setInspectService] = useState<ComposeService | null>(null)

  useEffect(() => {
    progressEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [progressLines])

  // In cluster mode the selected stack lives in its node group (it isn't in the
  // local `projects` list); otherwise resolve from the single-node list.
  const selectedProject = clusterMode
    ? selectedNodeGroup?.stacks.find(p => p.name === selectedName)
    : projects.find(p => p.name === selectedName)

  const fetchProjects = useCallback(async (showLoading = true) => {
    try {
      if (showLoading) setLoading(true)
      const data = await api.getComposeProjects()
      setProjects(data || [])
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('docker.compose.fetchFailed')
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

  useEffect(() => {
    setValidationResult(null)
  }, [editYaml])

  const handleValidate = async () => {
    if (!selectedName) return
    setValidating(true)
    setValidationResult(null)
    try {
      const result = await api.validateCompose(selectedName)
      setValidationResult(result)
      if (result.valid) {
        toast.success(t('docker.stacks.validateSuccess'))
      } else {
        toast.error(t('docker.stacks.validateFailed'))
      }
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.validateFailed'))
    } finally {
      setValidating(false)
    }
  }

  const handleCreate = async () => {
    if (!newName.trim() || !newYaml.trim()) return
    setCreating(true)
    try {
      await api.createComposeProject(newName.trim(), newYaml)
      toast.success(t('docker.compose.createSuccess', { name: newName }))
      setCreateOpen(false)
      setNewName('')
      setNewYaml(DEFAULT_COMPOSE)
      await fetchProjects()
      navigate(`${basePath}/${newName.trim()}`)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.compose.createFailed'))
    } finally {
      setCreating(false)
    }
  }

  const handleUp = async (name: string) => {
    setProgressTitle(t('docker.stacks.deploying'))
    setProgressLines([])
    setProgressDone(false)
    setProgressError(false)
    setProgressOpen(true)

    try {
      await api.composeUpStream(name, (event) => {
        if (event.phase === 'error') {
          setProgressError(true)
          setProgressLines(prev => [...prev, `❌ ${event.line}`])
        } else if (event.phase === 'complete') {
          setProgressLines(prev => [...prev, `✅ ${event.line}`])
        } else {
          setProgressLines(prev => [...prev, event.line])
        }
      })
      setProgressDone(true)
      toast.success(t('docker.compose.upSuccess', { name }))
      await Promise.all([
        refreshList(),
        selectedName === name ? fetchServices(name) : Promise.resolve(),
      ])
    } catch (err: unknown) {
      setProgressError(true)
      setProgressDone(true)
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
    setProgressTitle(t('docker.stacks.deploying'))
    setProgressLines([])
    setProgressDone(false)
    setProgressError(false)
    setProgressOpen(true)

    try {
      await api.composeUpStream(selectedName, (event) => {
        if (event.phase === 'error') {
          setProgressError(true)
          setProgressLines(prev => [...prev, `❌ ${event.line}`])
        } else if (event.phase === 'complete') {
          setProgressLines(prev => [...prev, `✅ ${event.line}`])
        } else {
          setProgressLines(prev => [...prev, event.line])
        }
      })
      setProgressDone(true)
      toast.success(t('docker.stacks.deploySuccess'))
      await Promise.all([refreshList(), fetchServices(selectedName)])
    } catch (err: unknown) {
      setProgressError(true)
      setProgressDone(true)
      toast.error(err instanceof Error ? err.message : t('docker.stacks.deployFailed'))
    }
  }

  const handleSaveYaml = async () => {
    if (!selectedName || !editYaml.trim()) return
    setEditSaving(true)
    try {
      await api.updateComposeProject(selectedName, editYaml)
      toast.success(t('docker.stacks.saved'))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.saveFailed'))
    } finally {
      setEditSaving(false)
    }
  }

  const handleSaveEnv = async () => {
    if (!selectedName) return
    setEnvSaving(true)
    try {
      await api.updateComposeEnv(selectedName, editEnv)
      toast.success(t('docker.stacks.envSaved'))
      // Refresh project to update has_env status
      refreshList()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.envSaveFailed'))
    } finally {
      setEnvSaving(false)
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

    setProgressTitle(t('docker.stacks.updating'))
    setProgressLines([])
    setProgressDone(false)
    setProgressError(false)
    setProgressOpen(true)

    try {
      await api.updateStackStream(selectedName, (event) => {
        if (event.phase === 'error') {
          setProgressError(true)
          setProgressLines(prev => [...prev, `❌ ${event.line}`])
        } else if (event.phase === 'complete') {
          setProgressLines(prev => [...prev, `✅ ${event.line}`])
        } else {
          setProgressLines(prev => [...prev, event.line])
        }
      })
      setProgressDone(true)
      toast.success(t('docker.stacks.updateSuccess'))
      setUpdateCheck(null)
      if (selectedName) {
        api.hasRollback(selectedName).then(r => setRollbackInfo(r)).catch(() => {})
      }
      await Promise.all([refreshList(), fetchServices(selectedName)])
    } catch (err: unknown) {
      setProgressError(true)
      setProgressDone(true)
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
            <Button variant="ghost" size="icon-xs" onClick={() => (clusterMode ? fetchClusterStacks() : fetchProjects())} disabled={clusterMode ? clusterLoading : loading}>
              <RefreshCw className={`h-3.5 w-3.5 ${(clusterMode ? clusterLoading : loading) ? 'animate-spin' : ''}`} />
            </Button>
            {!clusterMode && (
              <Button variant="ghost" size="icon-xs" onClick={() => setCreateOpen(true)}>
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
        {/* Desktop stack list */}
        <div className="hidden md:block space-y-1">
          {projects.length === 0 && !loading && (
            <p className="text-[13px] text-muted-foreground py-4 text-center">{t('docker.stacks.noStacks')}</p>
          )}
          {projects.map(p => (
            <div
              key={p.name}
              className={`flex items-center gap-2 px-3 py-2 rounded-xl cursor-pointer transition-all duration-200 ${
                selectedName === p.name
                  ? 'bg-primary/10 ring-1 ring-primary/20'
                  : 'hover:bg-secondary/50'
              }`}
              onClick={() => navigate(`${basePath}/${p.name}`)}
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
          {projects.length === 0 && !loading && (
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
                className="flex items-center gap-2 cursor-pointer"
                onClick={() => navigate(`${basePath}/${p.name}`)}
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
                      disabled={progressOpen && !progressDone}
                      onClick={handleUpdate}
                    >
                      {progressOpen && !progressDone ? (
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
                                className="text-left hover:text-primary hover:underline max-w-full truncate">{svc.name}</button>
                            ) : svc.name}
                          </TableCell>
                          <TableCell className="text-muted-foreground text-xs font-mono">{svc.image}</TableCell>
                          <TableCell>{serviceBadge(svc.state)}</TableCell>
                          <TableCell className="text-muted-foreground text-xs font-mono">{svc.ports || '-'}</TableCell>
                          <TableCell className="text-right">
                            <div className="flex items-center justify-end gap-1">
                              {svc.state === 'running' ? (
                                <Button variant="ghost" size="icon-xs" title={t('docker.stacks.stopService')}
                                  disabled={actionLoading === svc.name}
                                  onClick={() => handleServiceAction('stop', svc.name)}>
                                  {actionLoading === svc.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Square className="h-3.5 w-3.5" />}
                                </Button>
                              ) : (
                                <Button variant="ghost" size="icon-xs" title={t('docker.stacks.startService')}
                                  disabled={actionLoading === svc.name}
                                  onClick={() => handleServiceAction('start', svc.name)}>
                                  {actionLoading === svc.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
                                </Button>
                              )}
                              <Button variant="ghost" size="icon-xs" title={t('docker.stacks.restartService')}
                                disabled={actionLoading === svc.name}
                                onClick={() => handleServiceAction('restart', svc.name)}>
                                {actionLoading === svc.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RotateCw className="h-3.5 w-3.5" />}
                              </Button>
                              <Button variant="ghost" size="icon-xs" title="Healthcheck"
                                onClick={() => setHealthcheckTarget(svc)}>
                                <HeartPulse className={`h-3.5 w-3.5 ${svc.has_healthcheck ? 'text-success' : ''}`} />
                              </Button>
                              <Button variant="ghost" size="icon-xs" title={t('docker.containers.inspect')}
                                disabled={!svc.container_id}
                                onClick={() => setInspectService(svc)}>
                                <Info className="h-3.5 w-3.5" />
                              </Button>
                              <Button variant="ghost" size="icon-xs" title={t('docker.stacks.viewLogs')}
                                onClick={() => setLogService(svc)}>
                                <ScrollText className="h-3.5 w-3.5" />
                              </Button>
                              {svc.container_id && svc.state === 'running' && (
                                <Button variant="ghost" size="icon-xs" title={t('docker.stacks.openShell')}
                                  onClick={() => setShellService(svc)}>
                                  <Terminal className="h-3.5 w-3.5" />
                                </Button>
                              )}
                            </div>
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
                            className="text-[13px] font-medium truncate min-w-0 flex-1 text-left hover:text-primary">{svc.name}</button>
                        ) : (
                          <span className="text-[13px] font-medium truncate min-w-0 flex-1">{svc.name}</span>
                        )}
                        {serviceBadge(svc.state)}
                      </div>
                      <div className="text-[11px] text-muted-foreground font-mono truncate mb-1" title={svc.image}>{svc.image}</div>
                      {svc.ports && (
                        <div className="text-[11px] text-muted-foreground font-mono truncate mb-2">{svc.ports}</div>
                      )}
                      <div className="flex items-center gap-1 pt-2 border-t border-border/50">
                        {svc.state === 'running' ? (
                          <Button variant="ghost" size="icon-xs" title={t('docker.stacks.stopService')}
                            disabled={actionLoading === svc.name}
                            onClick={() => handleServiceAction('stop', svc.name)}>
                            {actionLoading === svc.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Square className="h-3.5 w-3.5" />}
                          </Button>
                        ) : (
                          <Button variant="ghost" size="icon-xs" title={t('docker.stacks.startService')}
                            disabled={actionLoading === svc.name}
                            onClick={() => handleServiceAction('start', svc.name)}>
                            {actionLoading === svc.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
                          </Button>
                        )}
                        <Button variant="ghost" size="icon-xs" title={t('docker.stacks.restartService')}
                          disabled={actionLoading === svc.name}
                          onClick={() => handleServiceAction('restart', svc.name)}>
                          {actionLoading === svc.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RotateCw className="h-3.5 w-3.5" />}
                        </Button>
                        <Button variant="ghost" size="icon-xs" title="Healthcheck"
                          onClick={() => setHealthcheckTarget(svc)}>
                          <HeartPulse className={`h-3.5 w-3.5 ${svc.has_healthcheck ? 'text-success' : ''}`} />
                        </Button>
                        <Button variant="ghost" size="icon-xs" title={t('docker.containers.inspect')}
                          disabled={!svc.container_id}
                          onClick={() => setInspectService(svc)}>
                          <Info className="h-3.5 w-3.5" />
                        </Button>
                        <Button variant="ghost" size="icon-xs" title={t('docker.stacks.viewLogs')}
                          onClick={() => setLogService(svc)}>
                          <ScrollText className="h-3.5 w-3.5" />
                        </Button>
                        {svc.container_id && svc.state === 'running' && (
                          <Button variant="ghost" size="icon-xs" title={t('docker.stacks.openShell')}
                            onClick={() => setShellService(svc)}>
                            <Terminal className="h-3.5 w-3.5" />
                          </Button>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              </TabsContent>

              <TabsContent value="editor">
                <div className="space-y-3">
                  {/* Compose / Env sub-tabs */}
                  <div className="flex items-center gap-1 bg-secondary/40 rounded-xl p-1 w-fit overflow-x-auto">
                    <button
                      className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium transition-all ${
                        editorTab === 'compose' ? 'bg-primary/10 text-primary card-shadow' : 'text-muted-foreground hover:text-foreground'
                      }`}
                      onClick={() => setEditorTab('compose')}
                    >
                      <FileCode className={`h-3.5 w-3.5 ${editorTab === 'compose' ? 'text-primary' : ''}`} />
                      {selectedProject?.compose_file || 'docker-compose.yml'}
                    </button>
                    <button
                      className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium transition-all ${
                        editorTab === 'env' ? 'bg-warning/10 text-warning card-shadow' : 'text-muted-foreground hover:text-foreground'
                      }`}
                      onClick={() => setEditorTab('env')}
                    >
                      <FileText className={`h-3.5 w-3.5 ${editorTab === 'env' ? 'text-warning' : ''}`} />
                      .env
                    </button>
                  </div>

                  {editorTab === 'compose' ? (
                    <>
                      <div className="rounded-2xl overflow-hidden border-t-2 border-t-primary card-shadow">
                        <ComposeEditor value={editYaml} onChange={setEditYaml} />
                      </div>
                      {validationResult && (
                        <div className={`flex items-center gap-2 px-3 py-2 rounded-xl text-[13px] ${
                          validationResult.valid
                            ? 'bg-success/10 text-success'
                            : 'bg-destructive/10 text-destructive'
                        }`}>
                          {validationResult.valid ? <CheckCircle2 className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
                          <span>{validationResult.valid ? t('docker.stacks.configValid') : validationResult.message}</span>
                        </div>
                      )}
                      <div className="flex flex-wrap justify-end gap-2">
                        <Button
                          variant="outline"
                          onClick={handleValidate}
                          disabled={validating || !editYaml.trim()}
                          className="rounded-xl"
                        >
                          {validating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <CheckCircle2 className="h-3.5 w-3.5" />}
                          {t('docker.stacks.validate')}
                        </Button>
                        <Button
                          variant="outline"
                          onClick={() => setDiffOpen(true)}
                          disabled={!editYaml.trim()}
                          className="rounded-xl"
                          title={!editYaml.trim() ? 'YAML을 입력해주세요' : '변경사항 미리보기'}
                        >
                          <Eye className="h-3.5 w-3.5" />
                          변경사항 미리보기
                        </Button>
                        <Button
                          variant="outline"
                          onClick={handleSaveYaml}
                          disabled={editSaving || !editYaml.trim()}
                          className="rounded-xl"
                        >
                          <Save className="h-3.5 w-3.5" />
                          {editSaving ? t('common.saving') : t('common.save')}
                        </Button>
                        <Button
                          onClick={handleDeploy}
                          disabled={editSaving || !editYaml.trim()}
                          className="rounded-xl bg-success hover:bg-success/90"
                        >
                          <ArrowUp className="h-3.5 w-3.5" />
                          {editSaving ? t('common.saving') : t('docker.stacks.deploy')}
                        </Button>
                      </div>
                    </>
                  ) : (
                    <>
                      <div className="rounded-2xl overflow-hidden border-t-2 border-t-warning card-shadow">
                        <ComposeEditor value={editEnv} onChange={setEditEnv} language="ini" />
                      </div>
                      <div className="flex justify-end gap-2">
                        <Button
                          onClick={handleSaveEnv}
                          disabled={envSaving}
                          className="rounded-xl"
                        >
                          <Save className="h-3.5 w-3.5" />
                          {envSaving ? t('common.saving') : t('common.save')}
                        </Button>
                      </div>
                    </>
                  )}
                </div>
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
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="w-[calc(100vw-2rem)] md:w-full sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t('docker.compose.createTitle')}</DialogTitle>
            <DialogDescription>{t('docker.stacks.createDescription')}</DialogDescription>
          </DialogHeader>
          <Tabs defaultValue="manual" className="w-full">
            <TabsList>
              <TabsTrigger value="manual">수동 작성</TabsTrigger>
              <TabsTrigger value="git">git에서 가져오기</TabsTrigger>
            </TabsList>
            <TabsContent value="manual" className="space-y-4 pt-2">
              <div className="space-y-2">
                <Label htmlFor="project-name">{t('docker.compose.projectName')}</Label>
                <Input id="project-name" placeholder="e.g., my-project" value={newName}
                  onChange={(e) => setNewName(e.target.value)} />
                <p className="text-[11px] text-muted-foreground">
                  {t('docker.stacks.createPathHint', { path: `/opt/stacks/${newName || '{name}'}` })}
                </p>
              </div>
              <div className="space-y-2">
                <Label>{t('docker.compose.composeFile')}</Label>
                <div className="rounded-md overflow-hidden border">
                  <ComposeEditor value={newYaml} onChange={setNewYaml} />
                </div>
              </div>
              <DialogFooter>
                <Button variant="outline" onClick={() => setCreateOpen(false)}>{t('common.cancel')}</Button>
                <Button onClick={handleCreate} disabled={creating || !newName.trim() || !newYaml.trim()}>
                  {creating ? t('common.creating') : t('common.create')}
                </Button>
              </DialogFooter>
            </TabsContent>
            <TabsContent value="git" className="pt-2">
              <GitImportForm
                onSuccess={(projectName) => {
                  setCreateOpen(false)
                  void fetchProjects()
                  navigate(`${basePath}/${projectName}`)
                }}
                onCancel={() => setCreateOpen(false)}
              />
            </TabsContent>
          </Tabs>
        </DialogContent>
      </Dialog>

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
            <DialogTitle className="truncate">{shellService?.name} — Shell</DialogTitle>
          </DialogHeader>
          {shellService?.container_id && <ContainerShell containerId={shellService.container_id} />}
        </DialogContent>
      </Dialog>

      {/* Deploy/Update progress modal */}
      <Dialog open={progressOpen} onOpenChange={(open) => {
        if (!open && progressDone) setProgressOpen(false)
      }}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {!progressDone && <Loader2 className="h-4 w-4 animate-spin text-primary" />}
              {progressDone && !progressError && <CheckCircle2 className="h-4 w-4 text-success" />}
              {progressDone && progressError && <XCircle className="h-4 w-4 text-destructive" />}
              {progressTitle}
            </DialogTitle>
          </DialogHeader>
          <div className="bg-terminal rounded-xl p-4 max-h-[400px] overflow-y-auto font-mono text-[12px] text-terminal-foreground leading-5">
            {progressLines.map((line, i) => (
              <div key={i} className={`whitespace-pre-wrap break-all ${
                line.startsWith('✅') ? 'text-success' :
                line.startsWith('❌') ? 'text-destructive' :
                line.startsWith('[pull]') ? 'text-primary' :
                line.startsWith('[recreate]') ? 'text-warning' :
                ''
              }`}>
                {line}
              </div>
            ))}
            {!progressDone && (
              <div className="flex items-center gap-1.5 text-muted-foreground mt-1">
                <span className="inline-block w-1.5 h-1.5 rounded-full bg-primary animate-pulse" />
                {t('common.loading')}
              </div>
            )}
            <div ref={progressEndRef} />
          </div>
          {progressDone && (
            <DialogFooter>
              <Button onClick={() => setProgressOpen(false)} className="rounded-xl">
                {t('common.close')}
              </Button>
            </DialogFooter>
          )}
        </DialogContent>
      </Dialog>

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
