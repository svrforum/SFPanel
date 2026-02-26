import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Trash2, RefreshCw, Plus, ArrowUp, ArrowDown, Pencil } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { ComposeProject } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import ComposeEditor from '@/components/ComposeEditor'

const DEFAULT_COMPOSE = `version: "3.8"
services:
  app:
    image: nginx:latest
    ports:
      - "8080:80"
`

function formatDate(dateStr: string): string {
  if (!dateStr) return '-'
  try {
    return new Date(dateStr).toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })
  } catch {
    return dateStr
  }
}

function statusBadge(status: string) {
  const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
  switch (status.toLowerCase()) {
    case 'running':
      return <span className={`${base} bg-[#00c471]/10 text-[#00c471]`}>running</span>
    case 'stopped':
      return <span className={`${base} bg-secondary text-muted-foreground`}>stopped</span>
    default:
      return <span className={`${base} bg-secondary text-muted-foreground`}>{status}</span>
  }
}

export default function DockerCompose() {
  const { t } = useTranslation()
  const [projects, setProjects] = useState<ComposeProject[]>([])
  const [loading, setLoading] = useState(true)
  const [actionLoading, setActionLoading] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newYaml, setNewYaml] = useState(DEFAULT_COMPOSE)
  const [creating, setCreating] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ComposeProject | null>(null)
  const [editTarget, setEditTarget] = useState<ComposeProject | null>(null)
  const [editYaml, setEditYaml] = useState('')
  const [editLoading, setEditLoading] = useState(false)
  const [editSaving, setEditSaving] = useState(false)

  const fetchProjects = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getComposeProjects()
      setProjects(data || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.compose.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchProjects()
  }, [fetchProjects])

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
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.compose.createFailed')
      toast.error(message)
    } finally {
      setCreating(false)
    }
  }

  const handleUp = async (project: ComposeProject) => {
    setActionLoading(project.name)
    try {
      await api.composeUp(project.name)
      toast.success(t('docker.compose.upSuccess', { name: project.name }))
      await fetchProjects()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.compose.upFailed')
      toast.error(message)
    } finally {
      setActionLoading(null)
    }
  }

  const handleDown = async (project: ComposeProject) => {
    setActionLoading(project.name)
    try {
      await api.composeDown(project.name)
      toast.success(t('docker.compose.downSuccess', { name: project.name }))
      await fetchProjects()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.compose.downFailed')
      toast.error(message)
    } finally {
      setActionLoading(null)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setActionLoading(deleteTarget.name)
    try {
      await api.deleteComposeProject(deleteTarget.name)
      toast.success(t('docker.compose.deleted'))
      setDeleteTarget(null)
      await fetchProjects()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.compose.deleteFailed')
      toast.error(message)
    } finally {
      setActionLoading(null)
    }
  }

  const handleEdit = async (project: ComposeProject) => {
    setEditTarget(project)
    setEditLoading(true)
    try {
      const data = await api.getComposeProject(project.name)
      setEditYaml(data.yaml)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.compose.fetchFailed')
      toast.error(message)
      setEditTarget(null)
    } finally {
      setEditLoading(false)
    }
  }

  const handleEditSave = async () => {
    if (!editTarget || !editYaml.trim()) return
    setEditSaving(true)
    try {
      await api.updateComposeProject(editTarget.name, editYaml)
      toast.success(t('docker.compose.updateSuccess', { name: editTarget.name }))
      setEditTarget(null)
      setEditYaml('')
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.compose.updateFailed')
      toast.error(message)
    } finally {
      setEditSaving(false)
    }
  }

  return (
    <div className="space-y-4 mt-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          {t('docker.compose.count', { count: projects.length })}
        </p>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={fetchProjects} disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus />
            {t('docker.compose.newProject')}
          </Button>
        </div>
      </div>

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('common.name')}</TableHead>
            <TableHead>{t('common.status')}</TableHead>
            <TableHead>{t('common.created')}</TableHead>
            <TableHead className="text-right">{t('common.actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {projects.length === 0 && !loading && (
            <TableRow>
              <TableCell colSpan={4} className="text-center text-muted-foreground py-8">
                {t('docker.compose.empty')}
              </TableCell>
            </TableRow>
          )}
          {projects.map((p) => (
            <TableRow key={p.id}>
              <TableCell className="font-medium">{p.name}</TableCell>
              <TableCell>{statusBadge(p.status)}</TableCell>
              <TableCell className="text-muted-foreground">{formatDate(p.created_at)}</TableCell>
              <TableCell className="text-right">
                <div className="flex items-center justify-end gap-1">
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    title={t('docker.compose.up')}
                    disabled={actionLoading === p.name}
                    onClick={() => handleUp(p)}
                  >
                    <ArrowUp />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    title={t('docker.compose.down')}
                    disabled={actionLoading === p.name}
                    onClick={() => handleDown(p)}
                  >
                    <ArrowDown />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    title={t('common.edit')}
                    onClick={() => handleEdit(p)}
                  >
                    <Pencil />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    title={t('common.delete')}
                    disabled={actionLoading === p.name}
                    onClick={() => setDeleteTarget(p)}
                  >
                    <Trash2 />
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {/* Create project dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t('docker.compose.createTitle')}</DialogTitle>
            <DialogDescription>
              {t('docker.compose.createDescription')}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="project-name">{t('docker.compose.projectName')}</Label>
              <Input
                id="project-name"
                placeholder="e.g., my-project"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label>{t('docker.compose.composeFile')}</Label>
              <div className="rounded-md overflow-hidden border">
                <ComposeEditor value={newYaml} onChange={setNewYaml} />
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button
              onClick={handleCreate}
              disabled={creating || !newName.trim() || !newYaml.trim()}
            >
              {creating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit project dialog */}
      <Dialog open={!!editTarget} onOpenChange={(open) => { if (!open) { setEditTarget(null); setEditYaml('') } }}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t('docker.compose.editTitle', { name: editTarget?.name })}</DialogTitle>
            <DialogDescription>
              {t('docker.compose.editDescription')}
            </DialogDescription>
          </DialogHeader>
          {editLoading ? (
            <div className="py-8 text-center text-muted-foreground">{t('common.loading')}</div>
          ) : (
            <div className="space-y-2">
              <Label>{t('docker.compose.composeFile')}</Label>
              <div className="rounded-md overflow-hidden border">
                <ComposeEditor value={editYaml} onChange={setEditYaml} />
              </div>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => { setEditTarget(null); setEditYaml('') }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleEditSave} disabled={editSaving || editLoading || !editYaml.trim()}>
              {editSaving ? t('common.saving') : t('common.save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('docker.compose.deleteTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="docker.compose.deleteConfirm"
                values={{ name: deleteTarget?.name ?? '' }}
                components={{ strong: <span className="font-semibold" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={actionLoading === deleteTarget?.name}
            >
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
