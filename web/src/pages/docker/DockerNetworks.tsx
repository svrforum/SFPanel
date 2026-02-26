import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Trash2, RefreshCw, Plus } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { DockerNetwork } from '@/types/api'
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

const PREDEFINED_NETWORKS = ['bridge', 'host', 'none']
const NETWORK_DRIVERS = ['bridge', 'overlay', 'host']

function shortId(id: string): string {
  return id.substring(0, 12)
}

export default function DockerNetworks() {
  const { t } = useTranslation()
  const [networks, setNetworks] = useState<DockerNetwork[]>([])
  const [loading, setLoading] = useState(true)
  const [createOpen, setCreateOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newDriver, setNewDriver] = useState('bridge')
  const [creating, setCreating] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<DockerNetwork | null>(null)
  const [actionLoading, setActionLoading] = useState(false)

  const fetchNetworks = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getNetworks()
      setNetworks(data || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.networks.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchNetworks()
  }, [fetchNetworks])

  const handleCreate = async () => {
    if (!newName.trim()) return
    setCreating(true)
    try {
      await api.createNetwork(newName.trim(), newDriver)
      toast.success(t('docker.networks.createSuccess', { name: newName }))
      setCreateOpen(false)
      setNewName('')
      setNewDriver('bridge')
      await fetchNetworks()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.networks.createFailed')
      toast.error(message)
    } finally {
      setCreating(false)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setActionLoading(true)
    try {
      await api.removeNetwork(deleteTarget.Id)
      toast.success(t('docker.networks.deleted'))
      setDeleteTarget(null)
      await fetchNetworks()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('docker.networks.deleteFailed')
      toast.error(message)
    } finally {
      setActionLoading(false)
    }
  }

  const isPredefined = (name: string): boolean => {
    return PREDEFINED_NETWORKS.includes(name.toLowerCase())
  }

  return (
    <div className="space-y-4 mt-4">
      <div className="flex items-center justify-between">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('docker.networks.count', { count: networks.length })}
        </span>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={fetchNetworks} disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus />
            {t('docker.networks.createNetwork')}
          </Button>
        </div>
      </div>

      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('common.name')}</TableHead>
            <TableHead>{t('docker.networks.id')}</TableHead>
            <TableHead>{t('docker.networks.driver')}</TableHead>
            <TableHead>{t('docker.networks.scope')}</TableHead>
            <TableHead className="text-right">{t('common.actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {networks.length === 0 && !loading && (
            <TableRow>
              <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                {t('docker.networks.empty')}
              </TableCell>
            </TableRow>
          )}
          {networks.map((n) => (
            <TableRow key={n.Id}>
              <TableCell className="font-medium">{n.Name}</TableCell>
              <TableCell className="text-muted-foreground font-mono text-xs">
                {shortId(n.Id)}
              </TableCell>
              <TableCell className="text-muted-foreground">{n.Driver}</TableCell>
              <TableCell className="text-muted-foreground">{n.Scope}</TableCell>
              <TableCell className="text-right">
                <Button
                  variant="ghost"
                  size="icon-xs"
                  title={isPredefined(n.Name) ? t('docker.networks.cannotDeletePredefined') : t('common.delete')}
                  disabled={isPredefined(n.Name)}
                  onClick={() => setDeleteTarget(n)}
                >
                  <Trash2 />
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      </div>

      {/* Create network dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('docker.networks.createNetwork')}</DialogTitle>
            <DialogDescription>
              {t('docker.networks.createDescription')}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="network-name">{t('docker.networks.networkName')}</Label>
              <Input
                id="network-name"
                placeholder="e.g., my-network"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && handleCreate()}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="network-driver">{t('docker.networks.driver')}</Label>
              <select
                id="network-driver"
                value={newDriver}
                onChange={(e) => setNewDriver(e.target.value)}
                className="flex h-9 w-full rounded-xl border-0 bg-secondary/50 px-3 py-1 text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/20"
              >
                {NETWORK_DRIVERS.map((d) => (
                  <option key={d} value={d}>
                    {d}
                  </option>
                ))}
              </select>
            </div>
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
            <DialogTitle>{t('docker.networks.deleteTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="docker.networks.deleteConfirm"
                values={{ name: deleteTarget?.Name ?? '' }}
                components={{ strong: <span className="font-semibold" /> }}
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
