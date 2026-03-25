import { useState, useEffect, useCallback, useRef } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useParams, useNavigate, useSearchParams } from 'react-router-dom'
import {
  Plus, Play, Square, RotateCw, ArrowUp, RefreshCw,
  Trash2, Terminal, ScrollText, FileText, FileCode, Save, Loader2,
  CheckCircle2, XCircle, Download, Undo2, Search, ChevronLeft,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { ComposeProjectWithStatus, ComposeService, StackUpdateCheck } from '@/types/api'
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

const DEFAULT_COMPOSE = `services:
  app:
    image: nginx:latest
    ports:
      - "8080:80"
`

function statusIcon(status: string) {
  switch (status) {
    case 'running':
      return <span className="inline-block w-2 h-2 rounded-full bg-[#00c471]" />
    case 'partial':
      return <span className="inline-block w-2 h-2 rounded-full bg-[#f59e0b]" />
    default:
      return <span className="inline-block w-2 h-2 rounded-full bg-muted-foreground/40" />
  }
}

function serviceBadge(state: string) {
  const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
  switch (state?.toLowerCase()) {
    case 'running':
      return <span className={`${base} bg-[#00c471]/10 text-[#00c471]`}>running</span>
    case 'exited':
      return <span className={`${base} bg-[#f04452]/10 text-[#f04452]`}>exited</span>
    case 'paused':
      return <span className={`${base} bg-[#f59e0b]/10 text-[#f59e0b]`}>paused</span>
    default:
      return <span className={`${base} bg-secondary text-muted-foreground`}>{state || 'unknown'}</span>
  }
}

export default function DockerStacks() {
  const { t } = useTranslation()
  const { name: selectedName } = useParams()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()

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

  // Editor state
  const [editYaml, setEditYaml] = useState('')
  const [editEnv, setEditEnv] = useState('')
  const [editorTab, setEditorTab] = useState<'compose' | 'env'>('compose')
  const [editSaving, setEditSaving] = useState(false)
  const [envSaving, setEnvSaving] = useState(false)
  const [validating, setValidating] = useState(false)
  const [validationResult, setValidationResult] = useState<{ valid: boolean; message: string } | null>(null)

  // Delete dialog
  const [deleteTarget, setDeleteTarget] = useState<ComposeProjectWithStatus | null>(null)
  const [deleteImages, setDeleteImages] = useState(false)
  const [deleteVolumes, setDeleteVolumes] = useState(false)

  // Image update check
  const [updateCheck, setUpdateCheck] = useState<StackUpdateCheck | null>(null)
  const [checkingUpdates, setCheckingUpdates] = useState(false)
  const [rollingBack, setRollingBack] = useState(false)
  const [hasRollbackData, setHasRollbackData] = useState(false)

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

  useEffect(() => {
    progressEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [progressLines])

  const selectedProject = projects.find(p => p.name === selectedName)

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

  const fetchServices = useCallback(async (name: string) => {
    try {
      setServicesLoading(true)
      const data = await api.getComposeServices(name)
      setServices(data || [])
    } catch {
      setServices([])
    } finally {
      setServicesLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchProjects()
  }, [fetchProjects])

  useEffect(() => {
    if (searchParams.get('new') === '1') {
      setCreateOpen(true)
      setSearchParams({}, { replace: true })
    }
  }, [searchParams, setSearchParams])

  useEffect(() => {
    if (selectedName) {
      fetchServices(selectedName)
      // Load YAML
      api.getComposeProject(selectedName).then(data => {
        setEditYaml(data.yaml)
      }).catch(() => {})
      // Load .env
      api.getComposeEnv(selectedName).then(data => {
        setEditEnv(data.content)
      }).catch(() => {})
    }
  }, [selectedName, fetchServices])

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
      navigate(`/docker/stacks/${newName.trim()}`)
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
        fetchProjects(false),
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
        fetchProjects(false),
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
      if (selectedName === deleteTarget.name) navigate('/docker/stacks')
      await fetchProjects()
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
      await Promise.all([fetchProjects(false), fetchServices(selectedName)])
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
      fetchProjects()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.envSaveFailed'))
    } finally {
      setEnvSaving(false)
    }
  }

  // Reset update check and rollback when stack changes
  useEffect(() => {
    setUpdateCheck(null)
    if (selectedName) {
      api.hasRollback(selectedName).then(r => setHasRollbackData(r.has_rollback)).catch(() => setHasRollbackData(false))
    } else {
      setHasRollbackData(false)
    }
  }, [selectedName])

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
      setHasRollbackData(true)
      await Promise.all([fetchProjects(false), fetchServices(selectedName)])
    } catch (err: unknown) {
      setProgressError(true)
      setProgressDone(true)
      toast.error(err instanceof Error ? err.message : t('docker.stacks.updateFailed'))
    }
  }

  const handleRollback = async () => {
    if (!selectedName) return
    setRollingBack(true)
    try {
      await api.rollbackStack(selectedName)
      toast.success(t('docker.stacks.rollbackSuccess'))
      setUpdateCheck(null)
      setHasRollbackData(false)
      await Promise.all([fetchProjects(false), fetchServices(selectedName)])
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
      await Promise.all([fetchServices(selectedName), fetchProjects(false)])
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.stacks.actionFailed'))
    } finally {
      setActionLoading(null)
    }
  }

  return (
    <div className="flex flex-col md:flex-row gap-4 h-full">
      {/* Stack list (left panel) — hidden on mobile when a stack is selected */}
      <div className={`md:w-[220px] shrink-0 space-y-2 ${selectedName ? 'hidden md:block' : ''}`}>
        <div className="flex items-center justify-between">
          <span className="text-[15px] font-semibold">{t('docker.stacks.title')}</span>
          <div className="flex gap-1">
            <Button variant="ghost" size="icon-xs" onClick={() => fetchProjects()} disabled={loading}>
              <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
            </Button>
            <Button variant="ghost" size="icon-xs" onClick={() => setCreateOpen(true)}>
              <Plus className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>

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
              onClick={() => navigate(`/docker/stacks/${p.name}`)}
            >
              {statusIcon(p.real_status)}
              <span className="text-[13px] font-medium truncate flex-1">{p.name}</span>
              <span className="text-[11px] text-muted-foreground">
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
                onClick={() => navigate(`/docker/stacks/${p.name}`)}
              >
                {statusIcon(p.real_status)}
                <span className="text-[13px] font-medium truncate flex-1">{p.name}</span>
                <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium ${
                  p.real_status === 'running' ? 'bg-[#00c471]/10 text-[#00c471]' :
                  p.real_status === 'partial' ? 'bg-[#f59e0b]/10 text-[#f59e0b]' :
                  'bg-secondary text-muted-foreground'
                }`}>
                  {p.running_count}/{p.service_count}
                </span>
              </div>
              <div className="flex items-center gap-1 mt-2 pt-2 border-t border-border/50">
                {p.real_status !== 'running' && (
                  <Button
                    size="sm" variant="ghost"
                    className="rounded-xl h-7 px-2 text-[11px] text-[#00c471]"
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
                      className="rounded-xl h-7 px-2 text-[11px] text-[#f04452]"
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
                  onClick={() => navigate(`/docker/stacks/${p.name}`)}
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
                  onClick={() => navigate('/docker/stacks')}
                >
                  <ChevronLeft className="h-4 w-4" />
                </Button>
                <h2 className="text-[18px] font-bold">{selectedName}</h2>
                {selectedProject && (
                  <>
                    <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium ${
                      selectedProject.real_status === 'running' ? 'bg-[#00c471]/10 text-[#00c471]' :
                      selectedProject.real_status === 'partial' ? 'bg-[#f59e0b]/10 text-[#f59e0b]' :
                      'bg-secondary text-muted-foreground'
                    }`}>
                      {t(`docker.stacks.${selectedProject.real_status}`)}
                    </span>
                    <span className="text-[11px] text-muted-foreground font-mono hidden sm:inline">
                      {selectedProject.path}
                    </span>
                  </>
                )}
              </div>
              <div className="flex items-center gap-2 flex-wrap">
                {selectedProject?.real_status !== 'running' && (
                  <Button
                    size="sm"
                    className="rounded-xl bg-[#00c471] hover:bg-[#00c471]/90 text-white"
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
                    className="rounded-xl border-[#f04452]/30 text-[#f04452] hover:bg-[#f04452]/10 hover:text-[#f04452]"
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
                {hasRollbackData && (
                  <Button
                    variant="outline" size="sm"
                    className="rounded-xl border-[#f59e0b]/30 text-[#f59e0b] hover:bg-[#f59e0b]/10 hover:text-[#f59e0b]"
                    disabled={rollingBack}
                    onClick={handleRollback}
                  >
                    {rollingBack ? (
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                      <Undo2 className="h-3.5 w-3.5" />
                    )}
                    {t('docker.stacks.rollback')}
                  </Button>
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
                  ? 'bg-[#3182f6]/5 ring-1 ring-[#3182f6]/20'
                  : 'bg-[#00c471]/5 ring-1 ring-[#00c471]/20'
              }`}>
                <div className="flex items-center justify-between mb-2">
                  <span className="text-[13px] font-semibold">
                    {t('docker.stacks.imageUpdates')}
                  </span>
                  {updateCheck.has_updates && (
                    <Button
                      size="sm" className="rounded-xl bg-[#3182f6] hover:bg-[#3182f6]/90 text-white"
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
                    <div key={img.image} className="flex items-center gap-2 text-[13px]">
                      {img.error ? (
                        <>
                          <XCircle className="h-3.5 w-3.5 text-[#f04452] shrink-0" />
                          <span className="font-mono text-[12px] truncate">{img.image}</span>
                          <span className="text-[11px] text-[#f04452]">{t('docker.stacks.registryError')}</span>
                        </>
                      ) : img.has_update ? (
                        <>
                          <span className="inline-block w-2 h-2 rounded-full bg-[#3182f6] shrink-0" />
                          <span className="font-mono text-[12px] truncate">{img.image}</span>
                          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#3182f6]/10 text-[#3182f6]">
                            {t('docker.stacks.updateAvailable')}
                          </span>
                        </>
                      ) : (
                        <>
                          <CheckCircle2 className="h-3.5 w-3.5 text-[#00c471] shrink-0" />
                          <span className="font-mono text-[12px] truncate">{img.image}</span>
                          <span className="text-[11px] text-muted-foreground">{t('docker.stacks.upToDate')}</span>
                        </>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Tabs */}
            <Tabs defaultValue="services">
              <TabsList className="bg-secondary/50 rounded-xl p-1">
                <TabsTrigger value="services" className="rounded-lg text-[13px] data-[state=active]:text-[#00c471]">
                  <Play className="h-3.5 w-3.5 mr-1" />
                  {t('docker.stacks.services')}
                </TabsTrigger>
                <TabsTrigger value="logs" className="rounded-lg text-[13px] data-[state=active]:text-[#f59e0b]">
                  <ScrollText className="h-3.5 w-3.5 mr-1" />
                  {t('docker.stacks.logs')}
                </TabsTrigger>
                <TabsTrigger value="editor" className="rounded-lg text-[13px] data-[state=active]:text-primary">
                  <FileCode className="h-3.5 w-3.5 mr-1" />
                  {t('docker.stacks.editor')}
                </TabsTrigger>
              </TabsList>

              <TabsContent value="services">
                {/* Desktop table */}
                <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden border-t-2 border-t-[#00c471]">
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
                          <TableCell className="font-medium text-[13px]">{svc.name}</TableCell>
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
                      <div className="flex items-center gap-2 mb-2">
                        <span className="text-[13px] font-medium truncate flex-1">{svc.name}</span>
                        {serviceBadge(svc.state)}
                      </div>
                      <div className="text-[11px] text-muted-foreground font-mono truncate mb-1">{svc.image}</div>
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

              <TabsContent value="logs">
                {selectedName && (
                  <ComposeLogs
                    project={selectedName}
                    serviceNames={services.map(s => s.name)}
                  />
                )}
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
                        editorTab === 'env' ? 'bg-[#f59e0b]/10 text-[#f59e0b] card-shadow' : 'text-muted-foreground hover:text-foreground'
                      }`}
                      onClick={() => setEditorTab('env')}
                    >
                      <FileText className={`h-3.5 w-3.5 ${editorTab === 'env' ? 'text-[#f59e0b]' : ''}`} />
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
                            ? 'bg-[#00c471]/10 text-[#00c471]'
                            : 'bg-[#f04452]/10 text-[#f04452]'
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
                          className="rounded-xl bg-[#00c471] hover:bg-[#00c471]/90"
                        >
                          <ArrowUp className="h-3.5 w-3.5" />
                          {editSaving ? t('common.saving') : t('docker.stacks.deploy')}
                        </Button>
                      </div>
                    </>
                  ) : (
                    <>
                      <div className="rounded-2xl overflow-hidden border-t-2 border-t-[#f59e0b] card-shadow">
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
          <div className="space-y-4">
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
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>{t('common.cancel')}</Button>
            <Button onClick={handleCreate} disabled={creating || !newName.trim() || !newYaml.trim()}>
              {creating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
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
                className="h-4 w-4 rounded border-border accent-[#f04452]"
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
                className="h-4 w-4 rounded border-border accent-[#f04452]"
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
            <DialogTitle>{logService?.name} — {t('docker.stacks.logs')}</DialogTitle>
          </DialogHeader>
          {logService?.container_id && <ContainerLogs containerId={logService.container_id} />}
        </DialogContent>
      </Dialog>

      {/* Service shell dialog */}
      <Dialog open={!!shellService} onOpenChange={(open) => !open && setShellService(null)}>
        <DialogContent className="w-[calc(100vw-2rem)] md:w-full sm:max-w-3xl h-[90vh] md:h-[80vh]">
          <DialogHeader>
            <DialogTitle>{shellService?.name} — Shell</DialogTitle>
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
              {progressDone && !progressError && <CheckCircle2 className="h-4 w-4 text-[#00c471]" />}
              {progressDone && progressError && <XCircle className="h-4 w-4 text-[#f04452]" />}
              {progressTitle}
            </DialogTitle>
          </DialogHeader>
          <div className="bg-[#0d1117] rounded-xl p-4 max-h-[400px] overflow-y-auto font-mono text-[12px] text-[#c9d1d9] leading-5">
            {progressLines.map((line, i) => (
              <div key={i} className={
                line.startsWith('✅') ? 'text-[#00c471]' :
                line.startsWith('❌') ? 'text-[#f04452]' :
                line.startsWith('[pull]') ? 'text-[#3182f6]' :
                line.startsWith('[recreate]') ? 'text-[#f59e0b]' :
                ''
              }>
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
    </div>
  )
}
