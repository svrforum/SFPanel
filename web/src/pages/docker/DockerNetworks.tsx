import { Link } from 'react-router-dom'
import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Trash2, RefreshCw, Plus, Sparkles, Check, Info, AlertCircle } from 'lucide-react'
import { toast } from 'sonner'
import { ResourceFilters } from '@/pages/docker/components/ResourceFilters'
import { useDockerResource } from '@/hooks/useDockerResource'
import ResourceStatus from '@/components/ResourceStatus'
import { api } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { UsagePill } from '@/pages/docker/components/UsagePill'
import type { DockerNetwork, NetworkInspectDetail, Container } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
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

const loadNetworks = () => api.getNetworks()

export default function DockerNetworks() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const { data: networks, loading, error, refresh: fetchNetworks, updatedAt } = useDockerResource(loadNetworks, 15000)
  const [query, setQuery] = useState('')
  const [usage, setUsage] = useState('all')
  const [sort, setSort] = useState('name')
  const [createOpen, setCreateOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newDriver, setNewDriver] = useState('bridge')
  const [creating, setCreating] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<DockerNetwork | null>(null)
  const [actionLoading, setActionLoading] = useState(false)
  const [pruning, setPruning] = useState(false)
  const [inspectTarget, setInspectTarget] = useState<NetworkInspectDetail | null>(null)
  const [availableContainers, setAvailableContainers] = useState<Container[]>([])
  const [connectContainer, setConnectContainer] = useState('')
  const [connecting, setConnecting] = useState(false)
  const [inspecting, setInspecting] = useState(false)

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
      setConnectContainer('')
      const containers = await api.getContainers()
      setAvailableContainers(containers || [])
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.networks.inspectFailed'))
    } finally {
      setInspecting(false)
    }
  }

  const changeConnection = async (container: string, operation: 'connect' | 'disconnect') => {
    if (!inspectTarget || !container) return
    if (operation === 'disconnect' && !await confirm({ title: t('docker.improvements.disconnect'), description: t('docker.improvements.disconnectConfirm'), danger: true })) return
    setConnecting(true)
    try {
      await api.changeNetworkConnection(inspectTarget.id, container, operation)
      await handleInspect(inspectTarget.id)
      await fetchNetworks()
    } catch (err) { toast.error(err instanceof Error ? err.message : String(err)) }
    finally { setConnecting(false) }
  }

  const isPredefined = (name: string): boolean => {
    return PREDEFINED_NETWORKS.includes(name.toLowerCase())
  }

  const handlePrune = async () => {
    const ok = await confirm({
      title: t('docker.prune.title'),
      description: t('docker.prune.networksConfirm'),
      confirmLabel: t('docker.prune.confirm'),
      danger: true,
    })
    if (!ok) return
    setPruning(true)
    try {
      const r = await api.pruneNetworks()
      toast.success(t('docker.prune.success') + (r.deleted > 0 ? `: ${r.deleted} deleted` : ''))
      fetchNetworks()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : 'Prune failed')
    } finally {
      setPruning(false)
    }
  }

  // In-use networks first; single sorted list keeps mobile and desktop in sync.
  const sortedNetworks = networks.filter(item => [item.Name, item.Driver, ...(item.used_by || [])].join(' ').toLowerCase().includes(query.toLowerCase()) && (usage === 'all' || (usage === 'used' ? item.in_use : !item.in_use))).sort((a, b) => a.Name.localeCompare(b.Name))

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('docker.networks.count', { count: networks.length })}
        </span>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={handlePrune} disabled={pruning}>
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

      <ResourceFilters sizeSort={false} query={query} onQuery={setQuery} usage={usage} onUsage={setUsage} sort={sort} onSort={setSort} count={sortedNetworks.length} />
      {sortedNetworks.length === 0 && networks.length > 0 && <p className="py-8 text-center text-muted-foreground">{t('docker.improvements.noResults')}</p>}
      <ResourceStatus resource={{ loading, error: !!error, updatedAt, retry: fetchNetworks }} />
      {/* Load error / loading skeleton (first load only) */}
      {error ? (
        <div className="bg-destructive/10 text-destructive rounded-xl p-3 flex items-start gap-2">
          <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
          <div className="min-w-0 flex-1">
            <p className="text-[13px] font-medium">{t('docker.networks.loadError')}</p>
            <p className="text-[12px] opacity-80 mt-0.5 break-words">{error}</p>
          </div>
          <Button variant="outline" size="sm" className="rounded-xl shrink-0" onClick={fetchNetworks}>
            <RefreshCw className="h-3.5 w-3.5" />
            {t('common.retry')}
          </Button>
        </div>
      ) : loading && networks.length === 0 ? (
        <div className="bg-card rounded-2xl p-3 card-shadow space-y-2">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-10 w-full rounded-xl" />
          ))}
        </div>
      ) : null}

      {/* Mobile card view */}
      <div className={`md:hidden space-y-2 ${(error || loading) && networks.length === 0 ? 'hidden' : ''}`}>
        {networks.length === 0 && !loading && !error && (
          <div className="text-center text-muted-foreground py-8 text-[13px]">
            {t('docker.networks.empty')}
          </div>
        )}
        {sortedNetworks.map((n) => (
          <div key={n.Id} className="bg-card rounded-2xl p-4 card-shadow">
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0 flex-1">
                <p className="text-[13px] font-medium truncate">{n.Name}</p>
                <div className="flex items-center gap-2 mt-1">
                  <span className="text-[11px] text-muted-foreground font-mono">{shortId(n.Id)}</span>
                  <span className="text-[11px] text-muted-foreground">{n.Driver}</span>
                  <span className="text-[11px] text-muted-foreground">{n.Scope}</span>
                </div>
                <div className="mt-1.5">
                  <UsagePill inUse={n.in_use} usedBy={n.used_by} />
                </div>
              </div>
              <div className="flex items-center gap-1 shrink-0">
                <Button
                  variant="ghost"
                  size="icon"
                  title={t('docker.containers.inspect')}
                  aria-label={t('docker.containers.inspect')}
                  disabled={inspecting}
                  onClick={() => handleInspect(n.Id)}
                >
                  <Info className="h-4 w-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  title={isPredefined(n.Name) ? t('docker.networks.cannotDeletePredefined') : t('common.delete')}
                  aria-label={isPredefined(n.Name) ? t('docker.networks.cannotDeletePredefined') : t('common.delete')}
                  disabled={isPredefined(n.Name)}
                  onClick={() => setDeleteTarget(n)}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* Desktop table */}
      <div className={`bg-card rounded-2xl card-shadow overflow-hidden ${(error || loading) && networks.length === 0 ? 'hidden' : 'hidden md:block'}`}>
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
          {networks.length === 0 && !loading && !error && (
            <TableRow>
              <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                {t('docker.networks.empty')}
              </TableCell>
            </TableRow>
          )}
          {sortedNetworks.map((n) => (
            <TableRow key={n.Id}>
              <TableCell className="font-medium">
                <div className="flex items-center gap-1.5">
                  {n.in_use && <Check className="h-3.5 w-3.5 text-success shrink-0" />}
                  {n.Name}
                </div>
              </TableCell>
              <TableCell className="text-muted-foreground font-mono text-xs">
                {shortId(n.Id)}
              </TableCell>
              <TableCell>
                <UsagePill inUse={n.in_use} usedBy={n.used_by} />
              </TableCell>
              <TableCell className="text-muted-foreground">{n.Driver}</TableCell>
              <TableCell className="text-muted-foreground">{n.Scope}</TableCell>
              <TableCell className="text-right">
                <div className="flex items-center justify-end gap-1">
                  <Button
                    variant="ghost"
                    size="icon"
                    title={t('docker.containers.inspect')}
                    aria-label={t('docker.containers.inspect')}
                    disabled={inspecting}
                    onClick={() => handleInspect(n.Id)}
                  >
                    <Info />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    title={isPredefined(n.Name) ? t('docker.networks.cannotDeletePredefined') : t('common.delete')}
                    aria-label={isPredefined(n.Name) ? t('docker.networks.cannotDeletePredefined') : t('common.delete')}
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
            <DialogTitle className="truncate">{inspectTarget?.name}</DialogTitle>
            <DialogDescription>{inspectTarget?.driver} · {inspectTarget?.scope}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 min-w-0">
            <div className="grid grid-cols-2 gap-3">
              <div className="min-w-0">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('docker.improvements.subnet')}</p>
                <p className="text-[13px] font-mono break-all">{inspectTarget?.subnet || '-'}</p>
              </div>
              <div className="min-w-0">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('docker.improvements.gateway')}</p>
                <p className="text-[13px] font-mono break-all">{inspectTarget?.gateway || '-'}</p>
              </div>
            </div>
            {inspectTarget && !isPredefined(inspectTarget.name) && <div className="flex flex-wrap gap-2">
              <select className="min-h-11 min-w-0 flex-1 rounded-xl bg-secondary px-2" aria-label={t('docker.improvements.connect')} value={connectContainer} onChange={e => setConnectContainer(e.target.value)} disabled={connecting}>
                <option value="">{t('docker.improvements.connect')}</option>
                {availableContainers.filter(c => !inspectTarget.containers?.some(endpoint => c.Id.startsWith(endpoint.id))).map(c => <option key={c.Id} value={c.Id}>{c.Names?.[0]?.replace(/^\//, '') || c.Id.slice(0, 12)}</option>)}
              </select>
              <Button disabled={!connectContainer || connecting} onClick={() => changeConnection(connectContainer, 'connect')}>{t('docker.improvements.connect')}</Button>
            </div>}
            {inspectTarget?.containers && inspectTarget.containers.length > 0 && (
              <div>
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider mb-2">{t('docker.networks.connectedContainers')}</p>
                <div className="bg-card rounded-xl card-shadow overflow-hidden overflow-x-auto">
                  <Table>
                    <TableHeader>
                      <TableRow className="border-border/50">
                        <TableHead className="text-[11px]">{t('common.name')}</TableHead>
                        <TableHead className="text-[11px]">IPv4</TableHead>
                        <TableHead className="text-[11px]">MAC</TableHead><TableHead>{t('common.actions')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {inspectTarget.containers.map(c => (
                        <TableRow key={c.id}>
                          <TableCell className="text-[13px] font-medium"><Link className="text-primary underline" to={`/docker/containers?container=${encodeURIComponent(c.id)}`}>{c.name}</Link></TableCell>
                          <TableCell className="text-[13px] font-mono text-muted-foreground">{c.ipv4_address || '-'}</TableCell>
                          <TableCell className="text-[13px] font-mono text-muted-foreground">{c.mac_address || '-'}</TableCell>
                          <TableCell>{!isPredefined(inspectTarget.name) && <Button variant="outline" disabled={connecting} onClick={() => changeConnection(c.id, 'disconnect')}>{t('docker.improvements.disconnect')}</Button>}</TableCell>
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
