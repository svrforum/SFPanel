import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Trash2, RefreshCw, Plus, Sparkles, Check } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatDate } from '@/lib/utils'
import type { DockerVolume } from '@/types/api'
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

export default function DockerVolumes() {
  const { t } = useTranslation()
  const [volumes, setVolumes] = useState<DockerVolume[]>([])
  const [loading, setLoading] = useState(true)
  const [createOpen, setCreateOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<DockerVolume | null>(null)
  const [actionLoading, setActionLoading] = useState(false)
  const [pruning, setPruning] = useState(false)

  const fetchVolumes = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getVolumes()
      setVolumes(data || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.volumes.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchVolumes()
  }, [fetchVolumes])

  const handleCreate = async () => {
    if (!newName.trim()) return
    setCreating(true)
    try {
      await api.createVolume(newName.trim())
      toast.success(t('docker.volumes.createSuccess', { name: newName }))
      setCreateOpen(false)
      setNewName('')
      await fetchVolumes()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.volumes.createFailed')
      toast.error(message)
    } finally {
      setCreating(false)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setActionLoading(true)
    try {
      await api.removeVolume(deleteTarget.Name)
      toast.success(t('docker.volumes.deleted'))
      setDeleteTarget(null)
      await fetchVolumes()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.volumes.deleteFailed')
      toast.error(message)
    } finally {
      setActionLoading(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('docker.volumes.count', { count: volumes.length })}
        </span>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={async () => {
            setPruning(true)
            try {
              const r = await api.pruneVolumes()
              toast.success(t('docker.prune.success') + (r.deleted > 0 ? `: ${r.deleted} deleted` : ''))
              fetchVolumes()
            } catch (err: unknown) { toast.error(err instanceof Error ? err.message : 'Prune failed') }
            finally { setPruning(false) }
          }} disabled={pruning}>
            <Sparkles className={pruning ? 'animate-spin' : ''} />
            {t('docker.sidebar.prune')}
          </Button>
          <Button variant="outline" size="sm" onClick={fetchVolumes} disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus />
            {t('docker.volumes.createVolume')}
          </Button>
        </div>
      </div>

      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('common.name')}</TableHead>
            <TableHead>{t('common.status')}</TableHead>
            <TableHead>{t('docker.volumes.driver')}</TableHead>
            <TableHead>{t('docker.volumes.mountpoint')}</TableHead>
            <TableHead>{t('common.created')}</TableHead>
            <TableHead className="text-right">{t('common.actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {volumes.length === 0 && !loading && (
            <TableRow>
              <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                {t('docker.volumes.empty')}
              </TableCell>
            </TableRow>
          )}
          {[...volumes].sort((a, b) => (a.in_use === b.in_use ? 0 : a.in_use ? -1 : 1)).map((v) => (
            <TableRow key={v.Name}>
              <TableCell className="font-medium font-mono text-sm">
                <div className="flex items-center gap-1.5">
                  {v.in_use && <Check className="h-3.5 w-3.5 text-[#00c471] shrink-0" />}
                  {v.Name}
                </div>
              </TableCell>
              <TableCell>
                {v.in_use ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]" title={v.used_by.join(', ')}>
                    {t('docker.volumes.inUse')}
                  </span>
                ) : (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">
                    {t('docker.volumes.unused')}
                  </span>
                )}
              </TableCell>
              <TableCell className="text-muted-foreground">{v.Driver}</TableCell>
              <TableCell className="text-muted-foreground text-xs font-mono max-w-[300px] truncate">
                {v.Mountpoint}
              </TableCell>
              <TableCell className="text-muted-foreground">{formatDate(v.CreatedAt)}</TableCell>
              <TableCell className="text-right">
                <Button
                  variant="ghost"
                  size="icon-xs"
                  title={t('common.delete')}
                  onClick={() => setDeleteTarget(v)}
                >
                  <Trash2 />
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      </div>

      {/* Create volume dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('docker.volumes.createVolume')}</DialogTitle>
            <DialogDescription>
              {t('docker.volumes.createDescription')}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="volume-name">{t('docker.volumes.volumeName')}</Label>
            <Input
              id="volume-name"
              placeholder="e.g., my-volume"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleCreate()}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreate} disabled={creating || !newName.trim()}>
              {creating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('docker.volumes.deleteTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="docker.volumes.deleteConfirm"
                values={{ name: deleteTarget?.Name ?? '' }}
                components={{ strong: <span className="font-semibold font-mono" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleDelete} disabled={actionLoading}>
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
