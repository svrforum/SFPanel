import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Trash2, RefreshCw, Plus, Sparkles, Check, Info } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { DockerNetwork, NetworkInspectDetail } from '@/types/api'
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
  const [pruning, setPruning] = useState(false)
  const [pruneConfirmOpen, setPruneConfirmOpen] = useState(false)
  const [inspectTarget, setInspectTarget] = useState<NetworkInspectDetail | null>(null)
  const [inspecting, setInspecting] = useState(false)

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

  const handleInspect = async (id: string) => {
    setInspecting(true)
    try {
      const detail = await api.inspectNetwork(id)
      setInspectTarget(detail)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.networks.inspectFailed'))
    } finally {
      setInspecting(false)
    }
  }

  const isPredefined = (name: string): boolean => {
    return PREDEFINED_NETWORKS.includes(name.toLowerCase())
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('docker.networks.count', { count: networks.length })}
        </span>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => setPruneConfirmOpen(true)} disabled={pruning}>
            <Sparkles className={pruning ? 'animate-spin' : ''} />
            {t('docker.sidebar.prune')}
          </Button>
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
            <TableHead>{t('common.status')}</TableHead>
            <TableHead>{t('docker.networks.driver')}</TableHead>
            <TableHead>{t('docker.networks.scope')}</TableHead>
            <TableHead className="text-right">{t('common.actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {networks.length === 0 && !loading && (
            <TableRow>
              <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                {t('docker.networks.empty')}
              </TableCell>
            </TableRow>
          )}
          {[...networks].sort((a, b) => (a.in_use === b.in_use ? 0 : a.in_use ? -1 : 1)).map((n) => (
            <TableRow key={n.Id}>
              <TableCell className="font-medium">
                <div className="flex items-center gap-1.5">
                  {n.in_use && <Check className="h-3.5 w-3.5 text-[#00c471] shrink-0" />}
                  {n.Name}
                </div>
              </TableCell>
              <TableCell className="text-muted-foreground font-mono text-xs">
                {shortId(n.Id)}
              </TableCell>
              <TableCell>
                {n.in_use ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]" title={n.used_by.join(', ')}>
                    {t('docker.networks.inUse')}
                  </span>
                ) : (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">
                    {t('docker.networks.unused')}
                  </span>
                )}
              </TableCell>
              <TableCell className="text-muted-foreground">{n.Driver}</TableCell>
              <TableCell className="text-muted-foreground">{n.Scope}</TableCell>
              <TableCell className="text-right">
                <div className="flex items-center justify-end gap-1">
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    title="Inspect"
                    disabled={inspecting}
                    onClick={() => handleInspect(n.Id)}
                  >
                    <Info />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    title={isPredefined(n.Name) ? t('docker.networks.cannotDeletePredefined') : t('common.delete')}
                    disabled={isPredefined(n.Name)}
                    onClick={() => setDeleteTarget(n)}
                  >
                    <Trash2 />
                  </Button>
                </div>
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

      {/* Network inspect dialog */}
      <Dialog open={!!inspectTarget} onOpenChange={(open) => !open && setInspectTarget(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{inspectTarget?.name}</DialogTitle>
            <DialogDescription>{inspectTarget?.driver} · {inspectTarget?.scope}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-3">
              <div>
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">Subnet</p>
                <p className="text-[13px] font-mono">{inspectTarget?.subnet || '-'}</p>
              </div>
              <div>
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">Gateway</p>
                <p className="text-[13px] font-mono">{inspectTarget?.gateway || '-'}</p>
              </div>
            </div>
            {inspectTarget?.containers && inspectTarget.containers.length > 0 && (
              <div>
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider mb-2">{t('docker.networks.connectedContainers')}</p>
                <div className="bg-card rounded-xl card-shadow overflow-hidden">
                  <Table>
                    <TableHeader>
                      <TableRow className="border-border/50">
                        <TableHead className="text-[11px]">{t('common.name')}</TableHead>
                        <TableHead className="text-[11px]">IPv4</TableHead>
                        <TableHead className="text-[11px]">MAC</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {inspectTarget.containers.map(c => (
                        <TableRow key={c.id}>
                          <TableCell className="text-[13px] font-medium">{c.name}</TableCell>
                          <TableCell className="text-[13px] font-mono text-muted-foreground">{c.ipv4_address || '-'}</TableCell>
                          <TableCell className="text-[13px] font-mono text-muted-foreground">{c.mac_address || '-'}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>

      {/* Prune confirmation dialog */}
      <Dialog open={pruneConfirmOpen} onOpenChange={setPruneConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('docker.prune.title')}</DialogTitle>
            <DialogDescription>{t('docker.prune.networksConfirm')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPruneConfirmOpen(false)}>{t('common.cancel')}</Button>
            <Button variant="destructive" disabled={pruning} onClick={async () => {
              setPruning(true)
              try {
                const r = await api.pruneNetworks()
                toast.success(t('docker.prune.success') + (r.deleted > 0 ? `: ${r.deleted} deleted` : ''))
                fetchNetworks()
              } catch (err: unknown) { toast.error(err instanceof Error ? err.message : 'Prune failed') }
              finally { setPruning(false); setPruneConfirmOpen(false) }
            }}>
              {pruning ? t('docker.prune.pruning') : t('docker.prune.confirm')}
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
