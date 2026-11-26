import { useState, useEffect, useCallback, useRef, lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useParams, useNavigate, useSearchParams, useLocation } from 'react-router-dom'
import {
  Plus, Play, Square, RotateCw, RefreshCw,
  Trash2, Terminal, ScrollText, FileCode, Loader2,
  CheckCircle2, XCircle, Download, Undo2, Search, ChevronLeft,
  HeartPulse, Info, ArrowRightLeft, Monitor, AlertCircle, SquareTerminal,
} from 'lucide-react'
import { HealthcheckComposerDialog } from '@/components/compose/HealthcheckComposerDialog'
import { MigrateStackDialog } from '@/pages/docker/components/MigrateStackDialog'
import { StackProgressDialog, useStackProgress } from '@/pages/docker/components/StackProgressDialog'
import { toast } from 'sonner'
import ResourceStatus from '@/components/ResourceStatus'
import { Input } from '@/components/ui/input'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { useServerScope } from '@/hooks/useServerScope'
import { terminalDirHref } from '@/lib/serverLinks'
import { ContainerStateBadge } from '@/components/docker/ContainerStateBadge'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { isRiskyRefusal, riskLines } from '@/lib/composeRisk'
import { matchesStackFilter, stackCounts, type StackFilter } from '@/pages/docker/stackList'
import { useConfirm } from '@/components/ConfirmDialog'
import type { ComposeProjectWithStatus, ComposeService, StackUpdateCheck, RollbackInfo } from '@/types/api'
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

// Load-failure banner for the stack list (docker-family error-state pattern,
// narrow-panel layout).
function ListErrorBanner({ message, onRetry }: { message: string; onRetry: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="bg-destructive/10 text-destructive rounded-xl p-3 space-y-2">
      <div className="flex items-start gap-2">
        <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
        <div className="min-w-0 flex-1">
          <p className="text-[13px] font-medium">{t('docker.stacks.loadError', 'Failed to load stacks')}</p>
          <p className="text-[12px] opacity-80 mt-0.5 break-words">{message}</p>
        </div>
      </div>
      <Button variant="outline" size="sm" className="rounded-xl w-full" onClick={onRetry}>
        <RefreshCw className="h-3.5 w-3.5" />
        {t('common.retry')}
      </Button>
    </div>
  )
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
    <div className={cn('flex flex-wrap items-center gap-1', className)}>
      {svc.state === 'running' ? (
        <Button variant="ghost" size="icon" title={t('docker.stacks.stopService')} aria-label={t('docker.stacks.stopService')}
          disabled={busy}
          onClick={() => onAction('stop', svc.name)}>
          {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Square className="h-3.5 w-3.5" />}
        </Button>
      ) : (
        <Button variant="ghost" size="icon" title={t('docker.stacks.startService')} aria-label={t('docker.stacks.startService')}
          disabled={busy}
          onClick={() => onAction('start', svc.name)}>
          {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
        </Button>
      )}
      <Button variant="ghost" size="icon" title={t('docker.stacks.restartService')} aria-label={t('docker.stacks.restartService')}
        disabled={busy}
        onClick={() => onAction('restart', svc.name)}>
        {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RotateCw className="h-3.5 w-3.5" />}
      </Button>
      <Button variant="ghost" size="icon" title={t('compose.healthcheck.title', 'Healthcheck')} aria-label={t('compose.healthcheck.title', 'Healthcheck')}
        onClick={() => onHealthcheck(svc)}>
        <HeartPulse className={`h-3.5 w-3.5 ${svc.has_healthcheck ? 'text-success' : ''}`} />
      </Button>
      <Button variant="ghost" size="icon" title={t('docker.containers.inspect')} aria-label={t('docker.containers.inspect')}
        disabled={!svc.container_id}
        onClick={() => onInspect(svc)}>
        <Info className="h-3.5 w-3.5" />
      </Button>
      <Button variant="ghost" size="icon" title={t('docker.stacks.viewLogs')} aria-label={t('docker.stacks.viewLogs')}
        onClick={() => onLogs(svc)}>
        <ScrollText className="h-3.5 w-3.5" />
      </Button>
      {svc.container_id && svc.state === 'running' && (
        <Button variant="ghost" size="icon" title={t('docker.stacks.openShell')} aria-label={t('docker.stacks.openShell')}
          onClick={() => onShell(svc)}>
          <Terminal className="h-3.5 w-3.5" />
        </Button>
      )}
    </div>
  )
}

/**
 * Everything that carries the compose editor arrives when it is opened.
 *
 * All three of these pull in Monaco and its language workers — 3.5 MB — and
 * static imports put that in this page's chunk, so listing stacks fetched the
 * whole editor before knowing whether anything would be edited. Measured after
 * the same fix landed on the file manager: this page transferred 5.18 MB
 * against 1.19 MB for the file manager beside it.
 */
const CreateStackDialog = lazy(() =>
  import('@/pages/docker/components/CreateStackDialog').then((m) => ({ default: m.CreateStackDialog })),
)
const StackEditorPanel = lazy(() =>
  import('@/pages/docker/components/StackEditorPanel').then((m) => ({ default: m.StackEditorPanel })),
)
const DiffSheet = lazy(() =>
  import('@/components/compose/DiffSheet').then((m) => ({ default: m.DiffSheet })),
)

type StackDraft = { yaml: string; env: string; diskYaml: string; diskEnv: string }
const stackDrafts = new Map<string, StackDraft>()
let draftSession: string | null = null
function warnUnsavedDrafts(event: BeforeUnloadEvent) {
  if (stackDrafts.size > 0 && draftSession === api.getToken()) { event.preventDefault(); event.returnValue = '' }
}

export default function DockerStacks() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const { name: selectedName } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const [searchParams, setSearchParams] = useSearchParams()
  const { href } = useServerScope()
  const basePath = '/docker/stacks'

  const [updatedAt, setUpdatedAt] = useState<number | null>(null)
  const [projects, setProjects] = useState<ComposeProjectWithStatus[]>([])
  const [loading, setLoading] = useState(true)
  const [listError, setListError] = useState<string | null>(null)
  const [services, setServices] = useState<ComposeService[]>([])
  const [servicesLoading, setServicesLoading] = useState(false)
  const [actionLoading, setActionLoading] = useState<string | null>(null)

  // Create dialog
  const [createOpen, setCreateOpen] = useState(false)

  // Node-to-node migration, from the server the page is on.
  const [migrateOpen, setMigrateOpen] = useState(false)
  const [migrateProject, setMigrateProject] = useState('')
  const [clusterNodeCount, setClusterNodeCount] = useState(0)
  useEffect(() => {
    // local: cluster membership is the same from any node; don't proxy this to a
    // remote currentNode just to count nodes.
    api.getClusterStatus()
      .then((s) => setClusterNodeCount(s.enabled ? (s.node_count ?? 0) : 0))
      .catch(() => setClusterNodeCount(0))
  }, [])

  // The remote node the page is on, named in the header chip.
  const [currentNodeName, setCurrentNodeName] = useState<string | null>(null)
  useEffect(() => {
    const nid = api.currentNode
    if (!nid) { setCurrentNodeName(null); return }
    // local: node names are the same from any node — resolve locally instead of
    // proxying this lookup to the remote node we're trying to name.
    api.getClusterNodes()
      .then((d) => setCurrentNodeName(d.nodes.find((n) => n.id === nid)?.name ?? null))
      .catch(() => setCurrentNodeName(null))
  }, [])

  const openMigrate = (project: string) => {
    setMigrateProject(project)
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
  const [diskEnv, setDiskEnv] = useState('')
  const [editorLoading, setEditorLoading] = useState(true)
  const [editorError, setEditorError] = useState<string | null>(null)
  const [editorAttempt, setEditorAttempt] = useState(0)
  const [servicesError, setServicesError] = useState<string | null>(null)
  const [stackSearch, setStackSearch] = useState('')
  const [stackFilter, setStackFilter] = useState<StackFilter>('all')
  const [drafts] = useState(() => {
    if (draftSession !== api.getToken()) { stackDrafts.clear(); draftSession = api.getToken() }
    return { current: stackDrafts }
  })
  const [loadedDraftKey, setLoadedDraftKey] = useState('')
  const draftKey = `${api.currentNode ?? 'local'}:${selectedName ?? ''}`
  const editorDirty = editYaml !== diskYaml || editEnv !== diskEnv
  useEffect(() => {
    if (!selectedName || loadedDraftKey !== draftKey || editorLoading || editorError) return
    if (editorDirty) drafts.current.set(draftKey, { yaml: editYaml, env: editEnv, diskYaml, diskEnv })
    else drafts.current.delete(draftKey)
  }, [drafts, loadedDraftKey, draftKey, selectedName, editorLoading, editorError, editorDirty, editYaml, editEnv, diskYaml, diskEnv])
  useEffect(() => {
    // The listener remains while navigating to other menus: drafts live in memory
    // until saved/discarded, and a reload from any menu would lose them.
    if (drafts.current.size > 0) window.addEventListener('beforeunload', warnUnsavedDrafts)
    else window.removeEventListener('beforeunload', warnUnsavedDrafts)
  }, [editorDirty, drafts, loadedDraftKey])
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

  const selectedProject = projects.find(p => p.name === selectedName)

  const fetchProjects = useCallback(async (showLoading = true) => {
    try {
      if (showLoading) setLoading(true)
      setListError(null)
      const data = await api.getComposeProjects()
      setProjects(data || [])
      setUpdatedAt(Date.now())
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('docker.compose.fetchFailed')
      setListError(msg)
      toast.error(msg)
    } finally {
      // Any answer ends the loading state: the first list arrives in the
      // background, and the list starts out loading.
      setLoading(false)
    }
  }, [t])

  // The first list after mount says whether this server has the stack the
  // address names. Switching servers remounts the page, so a stack the new
  // server lacks opens the list instead of an empty detail. Only that list
  // judges, and only when it loaded: a stack opened right after it was created
  // is never sent back.
  const detailChecked = useRef(false)
  useEffect(() => {
    if (detailChecked.current || (updatedAt === null && listError === null)) return
    detailChecked.current = true
    if (listError !== null || !selectedName || projects.some((p) => p.name === selectedName)) return
    navigate({ pathname: basePath, search: location.search }, { replace: true })
  }, [updatedAt, listError, projects, selectedName, navigate, location.search])

  // Tracks the latest stack the user is looking at, so that in-flight fetches for
  // a previously-selected stack don't overwrite the services list when they resolve
  // late (rapid stack switching produced a visible mix of two stacks' services).
  const latestSelectedRef = useRef<string>('')
  const serviceRequest = useRef(0)

  const fetchServices = useCallback(async (name: string) => {
    const identity = latestSelectedRef.current
    if (!identity.endsWith(`:${name}`)) return
    const request = ++serviceRequest.current
    try {
      setServicesLoading(true)
      const data = await api.getComposeServices(name)
      if (latestSelectedRef.current !== identity || serviceRequest.current !== request) return
      setServices(data || [])
      setServicesError(null)
    } catch (err) {
      if (latestSelectedRef.current !== identity || serviceRequest.current !== request) return
      setServicesError(err instanceof Error ? err.message : String(err))
    } finally {
      if (latestSelectedRef.current === identity && serviceRequest.current === request) setServicesLoading(false)
    }
  }, [])

  useEffect(() => {
    if (searchParams.get('new') === '1') {
      setCreateOpen(true)
      // Only `new` goes: the server the address names stays.
      const next = new URLSearchParams(searchParams)
      next.delete('new')
      setSearchParams(next, { replace: true })
    }
  }, [searchParams, setSearchParams])

  useEffect(() => {
    let cancelled = false
    latestSelectedRef.current = draftKey
    setServices([])
    setServicesError(null)
    setEditYaml('')
    setEditEnv('')
    setDiskYaml('')
    setDiskEnv('')
    setEditorError(null)
    setEditorLoading(true)
    setLoadedDraftKey('')
    if (selectedName) {
      void fetchServices(selectedName)
      const draft = drafts.current.get(draftKey)
      if (draft) {
        setLoadedDraftKey(draftKey)
        setEditYaml(draft.yaml); setEditEnv(draft.env)
        setDiskYaml(draft.diskYaml); setDiskEnv(draft.diskEnv)
        setEditorLoading(false)
      } else {
        Promise.all([api.getComposeProject(selectedName), api.getComposeEnv(selectedName)]).then(([data, env]) => {
          if (cancelled) return
          setLoadedDraftKey(draftKey)
          setEditYaml(data.yaml); setDiskYaml(data.yaml)
          setEditEnv(env.content); setDiskEnv(env.content)
        }).catch(err => {
          if (!cancelled) setEditorError(err instanceof Error ? err.message : String(err))
        }).finally(() => { if (!cancelled) setEditorLoading(false) })
      }
    }
    return () => { cancelled = true; latestSelectedRef.current = '' }
  }, [selectedName, fetchServices, draftKey, editorAttempt, drafts])

  useVisibleInterval(() => {
    void fetchProjects(false)
    if (selectedName) void fetchServices(selectedName)
  }, 15000)

  const handleUp = async (name: string) => {
    try {
      await runProgressStream(t('docker.stacks.deploying'), (onEvent) => api.composeUpStream(name, onEvent))
      toast.success(t('docker.compose.upSuccess', { name }))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.compose.upFailed'))
    } finally {
      // Failed or not: a start that stopped halfway left some containers up.
      await Promise.all([
        fetchProjects(false),
        selectedName === name ? fetchServices(name) : Promise.resolve(),
      ])
    }
  }

  // The same confirmation the containers screen asks before stopping a stack.
  const handleDown = async (name: string) => {
    if (!await confirm({
      title: t('docker.containers.stackDownTitle', { name }),
      description: t('docker.containers.stackDownConfirm'),
      confirmLabel: t('docker.containers.stackDown'),
      danger: true,
    })) return
    setActionLoading(name)
    try {
      await api.composeStop(name)
      toast.success(t('docker.compose.downSuccess', { name }))
      await Promise.all([
        fetchProjects(false),
        selectedName === name ? fetchServices(name) : Promise.resolve(),
      ])
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.compose.downFailed'))
    } finally {
      setActionLoading(null)
    }
  }

  const handleTeardown = async () => {
    if (!selectedName || !await confirm({ title: t('docker.improvements.teardownTitle', { name: selectedName }), description: t('docker.improvements.teardownConfirm'), confirmLabel: t('docker.improvements.teardownAction'), danger: true })) return
    setActionLoading(selectedName)
    try {
      await api.composeDown(selectedName)
      await Promise.all([fetchProjects(false), fetchServices(selectedName)])
    } catch (err) { toast.error(err instanceof Error ? err.message : String(err)) }
    finally { setActionLoading(null) }
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
      await fetchProjects()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.compose.deleteFailed'))
    } finally {
      setActionLoading(null)
    }
  }

  const handleDeploy = async () => {
    if (!selectedName || !editYaml.trim()) return
    if (editEnv !== diskEnv) { toast.error(t('docker.improvements.saveEnvFirst')); setEditorTab('env'); return }
    const identity = latestSelectedRef.current
    setEditSaving(true)
    try {
      const validation = await api.validateCompose(selectedName, editYaml)
      if (latestSelectedRef.current !== identity) { setEditSaving(false); return }
      if (!validation.valid) { toast.error(validation.message); setEditSaving(false); return }
      await api.updateComposeProject(selectedName, editYaml)
    } catch (err: unknown) {
      // A risky refusal is the one the operator can lift: show what the stack
      // asks for once, save again with the acknowledgement, and go on to the
      // deploy stream exactly as a first-attempt save would have.
      if (!isRiskyRefusal(err)) {
        toast.error(err instanceof Error ? err.message : t('docker.stacks.saveFailed'))
        setEditSaving(false)
        return
      }
      const ok = await confirm({
        title: t('docker.stacks.risky.title'),
        description: (
          <span>
            {t('docker.stacks.risky.body')}
            <span className="mt-2 block space-y-1 font-mono text-[12px] text-muted-foreground">
              {riskLines(err).map((line, index) => (
                <span key={`${index}-${line}`} className="block">{line}</span>
              ))}
            </span>
          </span>
        ),
        confirmLabel: t('docker.stacks.risky.confirm'),
        danger: true,
      })
      if (!ok || latestSelectedRef.current !== identity) {
        setEditSaving(false)
        return
      }
      try {
        await api.updateComposeProject(selectedName, editYaml, true)
      } catch (retryErr: unknown) {
        toast.error(retryErr instanceof Error ? retryErr.message : t('docker.stacks.saveFailed'))
        setEditSaving(false)
        return
      }
    }
    if (latestSelectedRef.current !== identity) { setEditSaving(false); return }
    setDiskYaml(editYaml)
    setEditSaving(false)

    // Open progress modal and stream
    try {
      await runProgressStream(t('docker.stacks.deploying'), (onEvent) => api.composeUpStream(selectedName, onEvent))
      toast.success(t('docker.stacks.deploySuccess'))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.deployFailed'))
    } finally {
      await Promise.all([fetchProjects(false), fetchServices(selectedName)])
    }
  }

  // Reset update check and rollback when the stack changes
  useEffect(() => {
    let cancelled = false
    setUpdateCheck(null)
    setRollbackInfo(null)
    if (selectedName) {
      api.hasRollback(selectedName).then(r => { if (!cancelled) setRollbackInfo(r) }).catch(() => {})
    } else {
      setRollbackInfo(null)
    }
    return () => { cancelled = true }
  }, [selectedName])

  const handleCheckUpdates = async () => {
    if (!selectedName) return
    const identity = latestSelectedRef.current
    setCheckingUpdates(true)
    setUpdateCheck(null)
    try {
      const result = await api.checkStackUpdates(selectedName)
      if (latestSelectedRef.current !== identity) return
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
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.updateFailed'))
    } finally {
      // An update that failed has still written its rollback point (the server
      // records it before it pulls) and may have recreated some services: the
      // rollback button and the lists must show that, not the state before.
      setUpdateCheck(null)
      api.hasRollback(selectedName).then(r => setRollbackInfo(r)).catch(() => {})
      await Promise.all([fetchProjects(false), fetchServices(selectedName)])
    }
  }

  const handleRollback = async () => {
    if (!selectedName) return
    const detailLines = rollbackInfo?.details?.map(e => {
      const prev = e.prev_image_id.substring(7, 19)
      const curr = e.curr_image_id ? e.curr_image_id.substring(7, 19) : '?'
      return `  ${e.service}: ${curr} → ${prev}`
    }).join('\n') || ''
    if (!(await confirm({
      title: t('docker.stacks.rollbackTitle', { name: selectedName }),
      description: (
        <span>
          {t('docker.stacks.confirmRollback')}
          {detailLines && <span className="mt-2 block whitespace-pre-wrap font-mono text-[12px]">{detailLines}</span>}
        </span>
      ),
      confirmLabel: t('docker.stacks.rollback'),
      danger: true,
    }))) return
    setRollingBack(true)
    try {
      await api.rollbackStack(selectedName)
      toast.success(t('docker.stacks.rollbackSuccess'))
      setUpdateCheck(null)
      setRollbackInfo(null)
      await Promise.all([fetchProjects(false), fetchServices(selectedName)])
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.rollbackFailed'))
    } finally {
      setRollingBack(false)
    }
  }

  const overviewCounts = stackCounts(projects)
  const overviewNotRunning = projects.filter((p) => p.real_status !== 'running')

  const handleServiceAction = async (action: 'restart' | 'stop' | 'start', service: string) => {
    if (!selectedName) return
    setActionLoading(service)
    try {
      if (action === 'restart') await api.restartComposeService(selectedName, service)
      else if (action === 'stop') await api.stopComposeService(selectedName, service)
      else if (action === 'start') await api.startComposeService(selectedName, service)
      toast.success(t(`docker.stacks.${action}Success`))
      await Promise.all([fetchServices(selectedName), fetchProjects(false)])
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.actionFailed'))
    } finally {
      setActionLoading(null)
    }
  }

  return (
    <div className="flex flex-col md:flex-row gap-4 h-full">
      {/* Stack list (left panel) — hidden on mobile when a stack is selected. */}
      <div className={`md:w-[220px] shrink-0 space-y-2 ${selectedName ? 'hidden md:block' : ''}`}>
        <div className="flex items-center justify-between">
          <span className="text-[15px] font-semibold">{t('docker.stacks.title')}</span>
          <div className="flex gap-1">
            <Button variant="ghost" size="icon" aria-label={t('common.refresh')} onClick={() => fetchProjects()} disabled={loading}>
              <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
            </Button>
            <Button variant="ghost" size="icon" aria-label={t('docker.compose.createTitle')} onClick={() => setCreateOpen(true)}>
              <Plus className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>

        <ResourceStatus resource={{ loading, error: !!listError, updatedAt, retry: () => { void fetchProjects(false) } }} />
        <Input aria-label={t('docker.improvements.search')} placeholder={t('docker.improvements.search')} value={stackSearch} onChange={e => setStackSearch(e.target.value)} />
        <select className="min-h-11 w-full rounded-xl bg-secondary px-3 text-base md:text-sm" aria-label={t('common.status')} value={stackFilter} onChange={e => setStackFilter(e.target.value as StackFilter)}>
          <option value="all">{t('docker.improvements.all')}</option><option value="running">{t('docker.stacks.running')}</option><option value="partial">{t('docker.stacks.partial')}</option><option value="stopped">{t('docker.stacks.stopped')}</option>
        </select>
        {/* Load failure */}
        {listError && !loading && (
          <ListErrorBanner message={listError} onRetry={() => fetchProjects()} />
        )}
        {/* Desktop stack list */}
        <div className="hidden md:block space-y-1">
          {projects.length === 0 && !loading && !listError && (
            <p className="text-[13px] text-muted-foreground py-4 text-center">{t('docker.stacks.noStacks')}</p>
          )}
          {projects.filter(p => matchesStackFilter(p, stackSearch, stackFilter)).map(p => (
            <div
              key={p.name}
              role="button"
              tabIndex={0}
              aria-current={selectedName === p.name ? 'true' : undefined}
              className={`flex items-center gap-2 px-3 py-2 rounded-xl cursor-pointer transition duration-200 outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0 ${
                selectedName === p.name
                  ? 'bg-primary/10 ring-1 ring-primary'
                  : 'hover:bg-secondary/50'
              }`}
              onClick={() => navigate(`${basePath}/${p.name}`)}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate(`${basePath}/${p.name}`) } }}
            >
              {statusIcon(p.real_status)}
              <span className="text-[13px] font-medium truncate min-w-0 flex-1">{p.name}</span>
              <span className="text-[12px] text-muted-foreground shrink-0">
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
          {projects.filter(p => matchesStackFilter(p, stackSearch, stackFilter)).map(p => (
            <div
              key={p.name}
              className={`bg-card rounded-2xl p-4 card-shadow ${
                selectedName === p.name ? 'ring-1 ring-primary/20' : ''
              }`}
            >
              <div
                role="button"
                tabIndex={0}
                className="flex items-center gap-2 cursor-pointer rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0"
                onClick={() => navigate(`${basePath}/${p.name}`)}
                onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate(`${basePath}/${p.name}`) } }}
              >
                {statusIcon(p.real_status)}
                <span className="text-[13px] font-medium truncate min-w-0 flex-1">{p.name}</span>
                <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-medium shrink-0 ${
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
                    className="rounded-xl h-7 px-2 text-[12px] text-success"
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
                      className="rounded-xl h-7 px-2 text-[12px] text-destructive"
                      disabled={actionLoading === p.name}
                      onClick={() => handleDown(p.name)}
                    >
                      {actionLoading === p.name ? <Loader2 className="h-3 w-3 animate-spin" /> : <Square className="h-3 w-3" />}
                      {t('docker.improvements.stop')}
                    </Button>
                    <Button
                      size="sm" variant="ghost"
                      className="rounded-xl h-7 px-2 text-[12px]"
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
                  variant="ghost" size="icon"
                  aria-label={t('common.delete')}
                  onClick={() => setDeleteTarget(p)}
                >
                  <Trash2 className="h-3 w-3" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Stack detail (right panel) */}
      <div className="flex-1 min-w-0">
        {!selectedName ? (
          <section className="hidden md:block space-y-4 rounded-2xl bg-card p-5 card-shadow">
            <h2 className="text-lg font-semibold">{t('docker.improvements.overview')}</h2>
            <p className="text-sm text-muted-foreground">{t('docker.improvements.stackFlow')}</p>
            <div className="grid grid-cols-4 gap-3">{([
              ['all', t('docker.improvements.all')],
              ['running', t('docker.stacks.running')],
              ['partial', t('docker.stacks.partial')],
              ['stopped', t('docker.stacks.stopped')],
            ] as const).map(([key, label]) => <button key={key} type="button" aria-pressed={stackFilter === key} className={`rounded-xl p-4 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring ${stackFilter === key ? 'bg-primary/10 ring-1 ring-primary' : 'bg-secondary/50'}`} onClick={() => setStackFilter(key)}><span className="block text-2xl font-semibold">{overviewCounts[key]}</span><span className="text-sm">{label}</span></button>)}</div>
            {overviewNotRunning.slice(0, 5).map((p) => <Button key={p.name} variant="outline" onClick={() => navigate(`${basePath}/${encodeURIComponent(p.name)}`)}>{p.name} · {p.running_count}/{p.service_count}</Button>)}
          </section>
        ) : (
          <div className="space-y-4">
            {/* Stack header */}
            <div className="space-y-2">
              <div className="flex items-center gap-3 flex-wrap">
                <Button
                  variant="ghost" size="icon"
                  className="md:hidden"
                  aria-label={t('docker.stacks.backToList')}
                  onClick={() => navigate(basePath)}
                >
                  <ChevronLeft className="h-4 w-4" />
                </Button>
                <h2 className="text-[18px] font-bold truncate min-w-0 max-w-full">{selectedName}</h2>
                {currentNodeName && (
                  <span
                    className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[12px] font-medium bg-primary/10 text-primary shrink-0"
                    title={t('docker.stacks.onNode', { node: currentNodeName })}
                  >
                    <Monitor className="h-3 w-3" />
                    {currentNodeName}
                  </span>
                )}
                {selectedProject && (
                  <>
                    <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-medium shrink-0 ${
                      selectedProject.real_status === 'running' ? 'bg-success/10 text-success' :
                      selectedProject.real_status === 'partial' ? 'bg-warning/10 text-warning' :
                      'bg-secondary text-muted-foreground'
                    }`}>
                      {t(`docker.stacks.${selectedProject.real_status}`)}
                    </span>
                    <span className="text-[12px] text-muted-foreground font-mono hidden sm:inline truncate min-w-0" title={selectedProject.path}>
                      {selectedProject.path}
                    </span>
                  </>
                )}
              </div>
              <div className="flex items-center gap-2 flex-wrap">
                <Button variant="outline" size="sm" disabled={!!actionLoading} onClick={handleTeardown}>{t('docker.improvements.teardown')}</Button>
                {selectedProject?.real_status !== 'running' && (
                  <Button
                    size="sm"
                    className="rounded-xl"
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
                    {t('docker.improvements.stop')}
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
                {selectedProject && (
                  <Button asChild variant="outline" size="sm" className="rounded-xl">
                    <Link to={href(terminalDirHref(selectedProject.path))}>
                      <SquareTerminal className="h-3.5 w-3.5" />
                      {t('terminal.openHere')}
                    </Link>
                  </Button>
                )}
                {clusterNodeCount > 1 && (
                  <Button
                    variant="outline" size="sm" className="rounded-xl"
                    onClick={() => selectedName && openMigrate(selectedName)}
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
                      <span className="text-[12px] text-muted-foreground font-mono hidden md:inline">
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
                  variant="ghost" size="icon"
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
                      size="sm" className="rounded-xl bg-primary hover:bg-primary/90 text-primary-foreground"
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
                          <span className="text-[12px] text-destructive shrink-0">{t('docker.stacks.registryError')}</span>
                        </>
                      ) : img.has_update ? (
                        <div className="flex flex-col gap-0.5 min-w-0 flex-1">
                          <div className="flex items-center gap-2 min-w-0">
                            <span className="inline-block w-2 h-2 rounded-full bg-primary shrink-0" />
                            <span className="font-mono text-[12px] truncate min-w-0 flex-1" title={img.image}>{img.image}</span>
                            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-medium bg-primary/10 text-primary shrink-0">
                              {t('docker.stacks.updateAvailable')}
                            </span>
                          </div>
                          {img.current_digest && img.remote_digest && (
                            <span className="pl-4 font-mono text-[12px] text-muted-foreground truncate">
                              {img.current_digest} → {img.remote_digest}
                            </span>
                          )}
                        </div>
                      ) : (
                        <>
                          <CheckCircle2 className="h-3.5 w-3.5 text-success shrink-0" />
                          <span className="font-mono text-[12px] truncate min-w-0 flex-1" title={img.image}>{img.image}</span>
                          <span className="text-[12px] text-muted-foreground shrink-0">{t('docker.stacks.upToDate')}</span>
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
                <TabsTrigger value="services" className="rounded-lg text-[13px]">
                  <Play className="h-3.5 w-3.5 mr-1" />
                  {t('docker.stacks.services')}
                </TabsTrigger>
                <TabsTrigger value="editor" className="rounded-lg text-[13px]">
                  <FileCode className="h-3.5 w-3.5 mr-1" />
                  {t('docker.stacks.editor')}
                </TabsTrigger>
                <TabsTrigger value="logs" className="rounded-lg text-[13px]">
                  <ScrollText className="h-3.5 w-3.5 mr-1" />
                  {t('docker.stacks.logs')}
                </TabsTrigger>
              </TabsList>

              <TabsContent value="services">
                {servicesError && <ListErrorBanner message={servicesError} onRetry={() => selectedName && void fetchServices(selectedName)} />}
                {/* Desktop table */}
                <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden">
                  <Table>
                    <TableHeader>
                      <TableRow className="border-border/50">
                        <TableHead className="text-[12px]">{t('common.name')}</TableHead>
                        <TableHead className="text-[12px]">{t('docker.containers.image')}</TableHead>
                        <TableHead className="text-[12px]">{t('common.status')}</TableHead>
                        <TableHead className="text-[12px]">{t('docker.containers.ports')}</TableHead>
                        <TableHead className="text-right text-[12px]">{t('common.actions')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {servicesLoading && (
                        <TableRow>
                          <TableCell colSpan={5} className="text-center text-muted-foreground py-8">{t('common.loading')}</TableCell>
                        </TableRow>
                      )}
                      {!servicesLoading && !servicesError && services.length === 0 && (
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
                                className="text-left hover:text-primary hover:underline max-w-full truncate rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0">{svc.name}</button>
                            ) : svc.name}
                          </TableCell>
                          <TableCell className="text-muted-foreground text-xs font-mono">{svc.image}</TableCell>
                          <TableCell>{<ContainerStateBadge state={svc.state} status={svc.status} />}</TableCell>
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
                  {!servicesLoading && !servicesError && services.length === 0 && (
                    <p className="text-center text-muted-foreground py-8 text-[13px]">
                      {t('docker.stacks.noServices')}
                    </p>
                  )}
                  {services.map(svc => (
                    <div key={svc.name} className="bg-card rounded-2xl p-4 card-shadow">
                      <div className="flex items-center gap-2 mb-2 min-w-0">
                        {svc.container_id ? (
                          <button onClick={() => setInspectService(svc)} title={t('docker.containers.inspect')}
                            className="text-[13px] font-medium truncate min-w-0 flex-1 text-left hover:text-primary rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0">{svc.name}</button>
                        ) : (
                          <span className="text-[13px] font-medium truncate min-w-0 flex-1">{svc.name}</span>
                        )}
                        {<ContainerStateBadge state={svc.state} status={svc.status} />}
                      </div>
                      <div className="text-[12px] text-muted-foreground font-mono truncate mb-1" title={svc.image}>{svc.image}</div>
                      {svc.ports && (
                        <div className="text-[12px] text-muted-foreground font-mono truncate mb-2">{svc.ports}</div>
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
                <p className="mb-3 text-sm text-muted-foreground" role="status">{t(editorDirty ? 'docker.improvements.unsaved' : 'docker.improvements.savedHint')}</p>
                {editorDirty && <Button variant="outline" className="mb-3" onClick={async () => { if (await confirm({ title: t('docker.improvements.discard'), description: t('docker.improvements.discardConfirm'), confirmLabel: t('docker.improvements.discard'), danger: true })) { drafts.current.delete(draftKey); setEditYaml(diskYaml); setEditEnv(diskEnv); setEditorAttempt(n => n + 1) } }}>{t('docker.improvements.discard')}</Button>}
                {editorLoading ? <p>{t('common.loading')}</p> : editorError ? <ListErrorBanner message={editorError} onRetry={() => setEditorAttempt(n => n + 1)} /> : <>
                {/* Only once the editor tab is the active one — a TabsContent
                    that is not selected still mounts its children. */}
                <Suspense fallback={<div className="py-16 text-center text-[13px] text-muted-foreground">{t('common.loading')}</div>}>
                <StackEditorPanel
                  key={draftKey}
                  project={selectedName}
                  onYamlSaved={yaml => { if (latestSelectedRef.current === draftKey) setDiskYaml(yaml) }}
                  composeFileName={selectedProject?.compose_file || 'docker-compose.yml'}
                  yaml={editYaml}
                  onYamlChange={setEditYaml}
                  env={editEnv}
                  onEnvChange={setEditEnv}
                  tab={editorTab}
                  onTabChange={setEditorTab}
                  deploying={editSaving}
                  onDeploy={() => setDiffOpen(true)}
                  onOpenDiff={() => setDiffOpen(true)}
                  onEnvSaved={() => { if (latestSelectedRef.current === draftKey) setDiskEnv(editEnv); void fetchProjects(false) }}
                />
                </Suspense>
                </>}
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

      {/* Create project dialog. Mounted only when open, so the editor it
          carries is not fetched by the stack list. */}
      {createOpen && (
        <Suspense fallback={null}>
          <CreateStackDialog
            open={createOpen}
            onOpenChange={setCreateOpen}
            stacksRoot={projects[0]?.path ? projects[0].path.slice(0, projects[0].path.lastIndexOf('/')) || '/' : undefined}
            onCreated={async (projectName) => {
              await fetchProjects()
              navigate(`${basePath}/${projectName}`)
            }}
          />
        </Suspense>
      )}

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
            <DialogTitle className="break-all">{t('docker.compose.deleteTitle', { name: deleteTarget?.name ?? '' })}</DialogTitle>
            <DialogDescription>{t('docker.compose.deleteConfirm')}</DialogDescription>
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
                <p className="text-[12px] text-muted-foreground">{t('docker.compose.deleteImagesDesc')}</p>
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
                <p className="text-[12px] text-muted-foreground">{t('docker.compose.deleteVolumesDesc')}</p>
              </div>
            </label>
            {/* The checkbox above only ever governed named volumes, but it was
                the dialog's only mention of data — so unchecking it read as
                "keep my data" while the stack folder, bind mounts and all,
                went regardless. Say what actually happens. */}
            <p className="rounded-xl bg-secondary/60 px-3 py-2 text-[12px] leading-relaxed text-muted-foreground">
              {t('docker.stacks.deleteDataNote', { path: deleteTarget?.path ?? deleteTarget?.name ?? '' })}
            </p>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>{t('common.cancel')}</Button>
            <Button variant="destructive" onClick={handleDelete} disabled={actionLoading === deleteTarget?.name}>
              {t('docker.compose.deleteAction')}
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
      {selectedName && diffOpen && (
        <Suspense fallback={null}>
        <DiffSheet
          key={draftKey}
          allowUnchanged
          open={diffOpen}
          onOpenChange={setDiffOpen}
          projectName={selectedName}
          proposedYaml={editYaml}
          onApply={() => {
            setDiffOpen(false)
            handleDeploy()
          }}
        />
        </Suspense>
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
        onMigrated={() => {
          // Stack moved off this node — refresh the list, and if it was the open
          // stack clear the now-stale detail route (matches the delete flow).
          void fetchProjects(false)
          if (migrateProject && migrateProject === selectedName) navigate(basePath)
        }}
      />
    </div>
  )
}
