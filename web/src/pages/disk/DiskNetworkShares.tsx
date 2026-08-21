import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Trash2, Network, Plug, Unplug, Search, Loader2, Lock, AlertTriangle } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes, cn } from '@/lib/utils'
import { useConfirm } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { TabLoading, RefreshButton } from './components/TabToolbar'

import type { NetworkShare, NetworkShareInput, NetworkShareTools } from '@/types/api'

const EMPTY_FORM: NetworkShareInput = {
  type: 'cifs',
  server: '',
  share: '',
  mount_point: '',
  username: '',
  password: '',
  domain: '',
  options: '',
  read_only: false,
}

export default function DiskNetworkShares() {
  const { t } = useTranslation()
  const confirm = useConfirm()

  const [shares, setShares] = useState<NetworkShare[]>([])
  const [tools, setTools] = useState<NetworkShareTools | null>(null)
  const [loading, setLoading] = useState(true)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState<NetworkShareInput>(EMPTY_FORM)
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)
  const [discovering, setDiscovering] = useState(false)
  const [discovered, setDiscovered] = useState<string[] | null>(null)
  const [rowBusy, setRowBusy] = useState<string | null>(null)

  const isSMB = form.type === 'cifs'
  // The mount helper for the selected type. Its absence is the difference
  // between a clear "install cifs-utils" and the kernel's opaque
  // "unknown filesystem type 'cifs'".
  const toolReady = tools ? (isSMB ? tools.cifs.installed : tools.nfs.installed) : true

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [list, toolInfo] = await Promise.all([
        api.getNetworkShares(),
        api.getNetworkShareTools().catch(() => null),
      ])
      setShares(list.shares ?? [])
      if (toolInfo) setTools(toolInfo)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t('disk.shares.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { void load() }, [load])

  const openDialog = () => {
    setForm(EMPTY_FORM)
    setDiscovered(null)
    setDialogOpen(true)
  }

  // Suggest a mount point from the share name so the common case needs no
  // typing, but never overwrite something the operator already edited.
  const suggestMountPoint = (share: string) => {
    if (!share) return
    const leaf = share.replace(/^\/+/, '').split('/').filter(Boolean).pop() ?? ''
    const slug = leaf.replace(/[^A-Za-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '').toLowerCase()
    if (slug) setForm((f) => (f.mount_point ? f : { ...f, mount_point: `/mnt/${slug}` }))
  }

  const handleDiscover = async () => {
    if (!form.server) {
      toast.error(t('disk.shares.serverRequired'))
      return
    }
    setDiscovering(true)
    try {
      const res = await api.discoverNetworkShares(form.type, form.server, form.username || undefined)
      setDiscovered(res.shares ?? [])
      if (!res.shares?.length) toast.info(t('disk.shares.noSharesFound'))
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t('disk.shares.discoverFailed'))
    } finally {
      setDiscovering(false)
    }
  }

  const handleTest = async () => {
    setTesting(true)
    try {
      const res = await api.testNetworkShare(form)
      if (res.warning) toast.warning(res.warning)
      else toast.success(res.message || t('disk.shares.testOk'))
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t('disk.shares.testFailed'))
    } finally {
      setTesting(false)
    }
  }

  const handleAdd = async () => {
    setBusy(true)
    try {
      await api.addNetworkShare(form)
      toast.success(t('disk.shares.added'))
      setDialogOpen(false)
      setForm(EMPTY_FORM)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t('disk.shares.addFailed'))
    } finally {
      setBusy(false)
    }
  }

  const handleInstallTool = async () => {
    setBusy(true)
    try {
      const res = await api.installNetworkShareTools(form.type)
      toast.success(res.message)
      setTools(await api.getNetworkShareTools())
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t('disk.shares.installFailed'))
    } finally {
      setBusy(false)
    }
  }

  const rowAction = async (mp: string, fn: () => Promise<{ message: string }>) => {
    setRowBusy(mp)
    try {
      const res = await fn()
      toast.success(res.message)
      await load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t('common.error'))
    } finally {
      setRowBusy(null)
    }
  }

  const handleRemove = async (s: NetworkShare) => {
    const ok = await confirm({
      title: t('disk.shares.removeTitle'),
      description: t('disk.shares.removeConfirm', { mount: s.mount_point }),
      confirmLabel: t('common.delete'),
      danger: true,
    })
    if (!ok) return
    await rowAction(s.mount_point, () => api.removeNetworkShare(s.mount_point))
  }

  if (loading) return <TabLoading />

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <p className="text-[13px] text-muted-foreground">{t('disk.shares.description')}</p>
        <div className="flex items-center gap-2">
          <RefreshButton onClick={() => void load()} loading={loading} />
          <Button size="sm" onClick={openDialog}>
            <Plus className="h-4 w-4 mr-1.5" />
            {t('disk.shares.add')}
          </Button>
        </div>
      </div>

      {shares.length === 0 ? (
        <div className="rounded-xl border border-border bg-card py-14 text-center">
          <Network className="h-8 w-8 mx-auto text-muted-foreground/50" />
          <p className="mt-3 text-[13px] font-medium">{t('disk.shares.emptyTitle')}</p>
          <p className="mt-1 text-[12px] text-muted-foreground">{t('disk.shares.emptyHint')}</p>
        </div>
      ) : (
        <div className="rounded-xl border border-border bg-card overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('disk.shares.colSource')}</TableHead>
                <TableHead>{t('disk.shares.colMountPoint')}</TableHead>
                <TableHead>{t('disk.shares.colType')}</TableHead>
                <TableHead>{t('disk.shares.colStatus')}</TableHead>
                <TableHead>{t('disk.shares.colUsage')}</TableHead>
                <TableHead className="text-right">{t('common.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shares.map((s) => (
                <TableRow key={s.mount_point}>
                  <TableCell className="font-mono text-[12px]">
                    <div className="flex items-center gap-1.5">
                      <span className="truncate max-w-[240px]">{s.source}</span>
                      {s.has_credentials && (
                        <Lock className="h-3 w-3 text-muted-foreground shrink-0" aria-label={t('disk.shares.authenticated')} />
                      )}
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-[12px]">{s.mount_point}</TableCell>
                  <TableCell className="text-[12px] uppercase">{s.type}</TableCell>
                  <TableCell>
                    <span className={cn(
                      'inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium',
                      s.mounted ? 'bg-success/10 text-success' : 'bg-secondary text-muted-foreground',
                    )}>
                      <span className={cn('h-1.5 w-1.5 rounded-full', s.mounted ? 'bg-success' : 'bg-muted-foreground/50')} />
                      {s.mounted ? t('disk.shares.mounted') : t('disk.shares.notMounted')}
                    </span>
                    {!s.managed && (
                      <span className="ml-1.5 inline-flex items-center gap-1 text-[10px] text-warning" title={t('disk.shares.unmanagedHint')}>
                        <AlertTriangle className="h-3 w-3" />
                        {t('disk.shares.unmanaged')}
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-[12px] text-muted-foreground">
                    {s.mounted && s.total_bytes
                      ? `${formatBytes(s.used_bytes ?? 0)} / ${formatBytes(s.total_bytes)}`
                      : '—'}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex items-center justify-end gap-1">
                      {s.mounted ? (
                        <Button
                          variant="ghost" size="sm"
                          disabled={rowBusy === s.mount_point}
                          onClick={() => void rowAction(s.mount_point, () => api.unmountNetworkShare(s.mount_point))}
                          title={t('disk.shares.unmount')}
                        >
                          <Unplug className="h-4 w-4" />
                        </Button>
                      ) : (
                        <Button
                          variant="ghost" size="sm"
                          disabled={rowBusy === s.mount_point}
                          onClick={() => void rowAction(s.mount_point, () => api.mountNetworkShare(s.mount_point))}
                          title={t('disk.shares.mount')}
                        >
                          <Plug className="h-4 w-4" />
                        </Button>
                      )}
                      <Button
                        variant="ghost" size="sm"
                        disabled={rowBusy === s.mount_point || !s.managed}
                        onClick={() => void handleRemove(s)}
                        title={s.managed ? t('common.delete') : t('disk.shares.unmanagedHint')}
                        className={s.managed ? 'text-destructive hover:text-destructive' : ''}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="sm:max-w-lg max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t('disk.shares.add')}</DialogTitle>
            <DialogDescription>{t('disk.shares.addDescription')}</DialogDescription>
          </DialogHeader>

          <div className="space-y-3">
            <div className="space-y-1.5">
              <Label>{t('disk.shares.type')}</Label>
              <div className="flex gap-2">
                {(['cifs', 'nfs'] as const).map((v) => (
                  <button
                    key={v}
                    type="button"
                    onClick={() => setForm((f) => ({ ...f, type: v, share: '' }))}
                    className={cn(
                      'flex-1 rounded-lg border px-3 py-2 text-[13px] font-medium transition-colors',
                      form.type === v
                        ? 'border-primary bg-primary/10 text-primary'
                        : 'border-border hover:bg-accent',
                    )}
                  >
                    {v === 'cifs' ? t('disk.shares.typeSmb') : t('disk.shares.typeNfs')}
                  </button>
                ))}
              </div>
            </div>

            {!toolReady && (
              <div className="rounded-lg border border-warning/40 bg-warning/10 p-3 space-y-2">
                <p className="text-[12px] text-foreground">
                  {t('disk.shares.toolMissing', {
                    pkg: isSMB ? tools?.cifs.package : tools?.nfs.package,
                  })}
                </p>
                <Button size="sm" variant="outline" onClick={() => void handleInstallTool()} disabled={busy}>
                  {busy && <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />}
                  {t('disk.shares.install')}
                </Button>
              </div>
            )}

            <div className="space-y-1.5">
              <Label htmlFor="share-server">{t('disk.shares.server')}</Label>
              <div className="flex gap-2">
                <Input
                  id="share-server"
                  value={form.server}
                  onChange={(e) => setForm((f) => ({ ...f, server: e.target.value }))}
                  placeholder="192.168.1.50"
                />
                <Button variant="outline" size="sm" onClick={() => void handleDiscover()} disabled={discovering}>
                  {discovering ? <Loader2 className="h-4 w-4 animate-spin" /> : <Search className="h-4 w-4" />}
                  <span className="ml-1.5 hidden sm:inline">{t('disk.shares.browse')}</span>
                </Button>
              </div>
            </div>

            {discovered !== null && discovered.length > 0 && (
              <div className="rounded-lg border border-border bg-secondary/40 p-2">
                <p className="text-[11px] text-muted-foreground mb-1.5 px-1">{t('disk.shares.pickShare')}</p>
                <div className="flex flex-wrap gap-1.5">
                  {discovered.map((name) => (
                    <button
                      key={name}
                      type="button"
                      onClick={() => { setForm((f) => ({ ...f, share: name })); suggestMountPoint(name) }}
                      className={cn(
                        'rounded-md border px-2 py-1 text-[12px] font-mono transition-colors',
                        form.share === name ? 'border-primary bg-primary/10 text-primary' : 'border-border hover:bg-accent',
                      )}
                    >
                      {name}
                    </button>
                  ))}
                </div>
              </div>
            )}

            <div className="space-y-1.5">
              <Label htmlFor="share-name">
                {isSMB ? t('disk.shares.shareName') : t('disk.shares.exportPath')}
              </Label>
              <Input
                id="share-name"
                value={form.share}
                onChange={(e) => setForm((f) => ({ ...f, share: e.target.value }))}
                onBlur={(e) => suggestMountPoint(e.target.value)}
                placeholder={isSMB ? 'photos' : '/export/media'}
              />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="share-mount">{t('disk.shares.mountPoint')}</Label>
              <Input
                id="share-mount"
                value={form.mount_point}
                onChange={(e) => setForm((f) => ({ ...f, mount_point: e.target.value }))}
                placeholder="/mnt/photos"
              />
            </div>

            {isSMB && (
              <div className="grid grid-cols-2 gap-2">
                <div className="space-y-1.5">
                  <Label htmlFor="share-user">{t('disk.shares.username')}</Label>
                  <Input
                    id="share-user"
                    value={form.username}
                    autoComplete="off"
                    onChange={(e) => setForm((f) => ({ ...f, username: e.target.value }))}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="share-pass">{t('disk.shares.password')}</Label>
                  <Input
                    id="share-pass"
                    type="password"
                    value={form.password}
                    autoComplete="new-password"
                    onChange={(e) => setForm((f) => ({ ...f, password: e.target.value }))}
                  />
                </div>
              </div>
            )}

            <div className="space-y-1.5">
              <Label htmlFor="share-opts">{t('disk.shares.options')}</Label>
              <Input
                id="share-opts"
                value={form.options}
                onChange={(e) => setForm((f) => ({ ...f, options: e.target.value }))}
                placeholder={isSMB ? 'uid=1000,gid=1000,vers=3.0' : 'rsize=131072,wsize=131072'}
              />
              <p className="text-[11px] text-muted-foreground">{t('disk.shares.optionsHint')}</p>
            </div>

            <label className="flex items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                checked={form.read_only}
                onChange={(e) => setForm((f) => ({ ...f, read_only: e.target.checked }))}
                className="h-4 w-4 rounded border-border"
              />
              {t('disk.shares.readOnly')}
            </label>

            <p className="text-[11px] text-muted-foreground">{t('disk.shares.bootSafetyNote')}</p>
          </div>

          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => void handleTest()} disabled={testing || busy || !toolReady}>
              {testing && <Loader2 className="h-4 w-4 mr-1.5 animate-spin" />}
              {t('disk.shares.test')}
            </Button>
            <Button onClick={() => void handleAdd()} disabled={busy || testing || !toolReady}>
              {busy && <Loader2 className="h-4 w-4 mr-1.5 animate-spin" />}
              {t('disk.shares.connect')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
