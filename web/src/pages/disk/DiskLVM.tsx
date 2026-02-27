import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Plus, Trash2, RefreshCw, Maximize2, HardDrive, Layers, Box } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

interface PhysicalVolume {
  name: string
  vg_name: string
  size: number
  free: number
  attr: string
  fmt: string
}

interface VolumeGroup {
  name: string
  size: number
  free: number
  pv_count: number
  lv_count: number
  attr: string
}

interface LogicalVolume {
  name: string
  vg_name: string
  size: number
  attr: string
  path: string
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return `${size.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export default function DiskLVM() {
  const { t } = useTranslation()
  const [pvs, setPvs] = useState<PhysicalVolume[]>([])
  const [vgs, setVgs] = useState<VolumeGroup[]>([])
  const [lvs, setLvs] = useState<LogicalVolume[]>([])
  const [loading, setLoading] = useState(true)

  // PV Create
  const [pvCreateOpen, setPvCreateOpen] = useState(false)
  const [pvDevice, setPvDevice] = useState('')
  const [pvCreating, setPvCreating] = useState(false)

  // PV Delete
  const [pvDeleteTarget, setPvDeleteTarget] = useState<PhysicalVolume | null>(null)
  const [pvDeleting, setPvDeleting] = useState(false)

  // VG Create
  const [vgCreateOpen, setVgCreateOpen] = useState(false)
  const [vgName, setVgName] = useState('')
  const [vgSelectedPvs, setVgSelectedPvs] = useState<string[]>([])
  const [vgCreating, setVgCreating] = useState(false)

  // LV Create
  const [lvCreateOpen, setLvCreateOpen] = useState(false)
  const [lvName, setLvName] = useState('')
  const [lvVgName, setLvVgName] = useState('')
  const [lvSize, setLvSize] = useState('')
  const [lvCreating, setLvCreating] = useState(false)

  // LV Delete
  const [lvDeleteTarget, setLvDeleteTarget] = useState<LogicalVolume | null>(null)
  const [lvDeleting, setLvDeleting] = useState(false)

  // LV Resize
  const [lvResizeOpen, setLvResizeOpen] = useState(false)
  const [lvResizeTarget, setLvResizeTarget] = useState<LogicalVolume | null>(null)
  const [lvResizeNewSize, setLvResizeNewSize] = useState('')
  const [lvResizing, setLvResizing] = useState(false)

  const fetchLVM = useCallback(async () => {
    try {
      setLoading(true)
      const [pvData, vgData, lvData] = await Promise.all([
        api.getLVMPVs(),
        api.getLVMVGs(),
        api.getLVMLVs(),
      ])
      setPvs(pvData || [])
      setVgs(vgData || [])
      setLvs(lvData || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.lvm.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchLVM()
  }, [fetchLVM])

  // PV handlers
  const handleCreatePV = async () => {
    if (!pvDevice.trim()) return
    setPvCreating(true)
    try {
      await api.createPV(pvDevice.trim())
      toast.success(t('disk.lvm.pv.createSuccess'))
      setPvCreateOpen(false)
      setPvDevice('')
      await fetchLVM()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.lvm.pv.createFailed')
      toast.error(message)
    } finally {
      setPvCreating(false)
    }
  }

  const handleDeletePV = async () => {
    if (!pvDeleteTarget) return
    setPvDeleting(true)
    try {
      await api.removePV(pvDeleteTarget.name)
      toast.success(t('disk.lvm.pv.deleted'))
      setPvDeleteTarget(null)
      await fetchLVM()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.lvm.pv.deleteFailed')
      toast.error(message)
    } finally {
      setPvDeleting(false)
    }
  }

  // VG handlers
  const handleCreateVG = async () => {
    if (!vgName.trim() || vgSelectedPvs.length === 0) return
    setVgCreating(true)
    try {
      await api.createVG(vgName.trim(), vgSelectedPvs)
      toast.success(t('disk.lvm.vg.createSuccess'))
      setVgCreateOpen(false)
      setVgName('')
      setVgSelectedPvs([])
      await fetchLVM()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.lvm.vg.createFailed')
      toast.error(message)
    } finally {
      setVgCreating(false)
    }
  }

  const togglePvSelection = (pvName: string) => {
    setVgSelectedPvs((prev) =>
      prev.includes(pvName) ? prev.filter((p) => p !== pvName) : [...prev, pvName]
    )
  }

  // LV handlers
  const handleCreateLV = async () => {
    if (!lvName.trim() || !lvVgName || !lvSize.trim()) return
    setLvCreating(true)
    try {
      await api.createLV(lvName.trim(), lvVgName, lvSize.trim())
      toast.success(t('disk.lvm.lv.createSuccess'))
      setLvCreateOpen(false)
      setLvName('')
      setLvVgName('')
      setLvSize('')
      await fetchLVM()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.lvm.lv.createFailed')
      toast.error(message)
    } finally {
      setLvCreating(false)
    }
  }

  const handleDeleteLV = async () => {
    if (!lvDeleteTarget) return
    setLvDeleting(true)
    try {
      await api.removeLV(lvDeleteTarget.vg_name, lvDeleteTarget.name)
      toast.success(t('disk.lvm.lv.deleted'))
      setLvDeleteTarget(null)
      await fetchLVM()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.lvm.lv.deleteFailed')
      toast.error(message)
    } finally {
      setLvDeleting(false)
    }
  }

  const handleResizeLV = async () => {
    if (!lvResizeTarget || !lvResizeNewSize.trim()) return
    setLvResizing(true)
    try {
      await api.resizeLV({ vg: lvResizeTarget.vg_name, name: lvResizeTarget.name, size: lvResizeNewSize.trim() })
      toast.success(t('disk.lvm.lv.resizeSuccess'))
      setLvResizeOpen(false)
      setLvResizeTarget(null)
      setLvResizeNewSize('')
      await fetchLVM()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.lvm.lv.resizeFailed')
      toast.error(message)
    } finally {
      setLvResizing(false)
    }
  }

  const openLvResize = (lv: LogicalVolume) => {
    setLvResizeTarget(lv)
    setLvResizeNewSize('')
    setLvResizeOpen(true)
  }

  // Unassigned PVs for VG creation
  const freePvs = pvs.filter((pv) => !pv.vg_name)

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        {t('common.loading')}
      </div>
    )
  }

  return (
    <div className="space-y-4 mt-4">
      {/* Toolbar */}
      <div className="flex items-center justify-end">
        <Button variant="outline" size="sm" onClick={fetchLVM} disabled={loading} className="rounded-xl">
          <RefreshCw className={loading ? 'animate-spin' : ''} />
          {t('common.refresh')}
        </Button>
      </div>

      {/* LVM Sub-tabs */}
      <Tabs defaultValue="pv">
        <TabsList className="bg-secondary/50 rounded-xl p-1">
          <TabsTrigger value="pv" className="rounded-lg text-[13px]">
            <HardDrive className="h-3.5 w-3.5 mr-1" />
            {t('disk.lvm.pv.title')} ({pvs.length})
          </TabsTrigger>
          <TabsTrigger value="vg" className="rounded-lg text-[13px]">
            <Layers className="h-3.5 w-3.5 mr-1" />
            {t('disk.lvm.vg.title')} ({vgs.length})
          </TabsTrigger>
          <TabsTrigger value="lv" className="rounded-lg text-[13px]">
            <Box className="h-3.5 w-3.5 mr-1" />
            {t('disk.lvm.lv.title')} ({lvs.length})
          </TabsTrigger>
        </TabsList>

        {/* Physical Volumes */}
        <TabsContent value="pv">
          <div className="space-y-3 mt-3">
            <div className="flex items-center justify-end">
              <Button size="sm" onClick={() => setPvCreateOpen(true)} className="rounded-xl">
                <Plus />
                {t('disk.lvm.pv.create')}
              </Button>
            </div>
            <div className="bg-card rounded-2xl card-shadow overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow className="border-border/50">
                    <TableHead className="text-[11px]">{t('common.name')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.pv.vgName')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.size')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.free')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.attr')}</TableHead>
                    <TableHead className="text-right text-[11px]">{t('common.actions')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {pvs.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                        {t('disk.lvm.pv.empty')}
                      </TableCell>
                    </TableRow>
                  )}
                  {pvs.map((pv) => (
                    <TableRow key={pv.name}>
                      <TableCell className="font-medium font-mono text-sm">{pv.name}</TableCell>
                      <TableCell className="text-muted-foreground">{pv.vg_name || '-'}</TableCell>
                      <TableCell className="text-muted-foreground">{formatBytes(pv.size)}</TableCell>
                      <TableCell className="text-muted-foreground">{formatBytes(pv.free)}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">{pv.attr}</TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          title={t('common.delete')}
                          onClick={() => setPvDeleteTarget(pv)}
                          disabled={!!pv.vg_name}
                        >
                          <Trash2 />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        </TabsContent>

        {/* Volume Groups */}
        <TabsContent value="vg">
          <div className="space-y-3 mt-3">
            <div className="flex items-center justify-end">
              <Button size="sm" onClick={() => setVgCreateOpen(true)} className="rounded-xl" disabled={freePvs.length === 0}>
                <Plus />
                {t('disk.lvm.vg.create')}
              </Button>
            </div>
            <div className="bg-card rounded-2xl card-shadow overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow className="border-border/50">
                    <TableHead className="text-[11px]">{t('common.name')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.size')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.free')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.vg.pvCount')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.vg.lvCount')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.attr')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {vgs.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                        {t('disk.lvm.vg.empty')}
                      </TableCell>
                    </TableRow>
                  )}
                  {vgs.map((vg) => (
                    <TableRow key={vg.name}>
                      <TableCell className="font-medium">{vg.name}</TableCell>
                      <TableCell className="text-muted-foreground">{formatBytes(vg.size)}</TableCell>
                      <TableCell className="text-muted-foreground">{formatBytes(vg.free)}</TableCell>
                      <TableCell className="text-muted-foreground">{vg.pv_count}</TableCell>
                      <TableCell className="text-muted-foreground">{vg.lv_count}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">{vg.attr}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        </TabsContent>

        {/* Logical Volumes */}
        <TabsContent value="lv">
          <div className="space-y-3 mt-3">
            <div className="flex items-center justify-end">
              <Button size="sm" onClick={() => { setLvCreateOpen(true); if (vgs.length > 0) setLvVgName(vgs[0].name) }} className="rounded-xl" disabled={vgs.length === 0}>
                <Plus />
                {t('disk.lvm.lv.create')}
              </Button>
            </div>
            <div className="bg-card rounded-2xl card-shadow overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow className="border-border/50">
                    <TableHead className="text-[11px]">{t('common.name')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.lv.vgName')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.size')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.attr')}</TableHead>
                    <TableHead className="text-[11px]">{t('disk.lvm.lv.path')}</TableHead>
                    <TableHead className="text-right text-[11px]">{t('common.actions')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {lvs.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                        {t('disk.lvm.lv.empty')}
                      </TableCell>
                    </TableRow>
                  )}
                  {lvs.map((lv) => (
                    <TableRow key={lv.path}>
                      <TableCell className="font-medium">{lv.name}</TableCell>
                      <TableCell className="text-muted-foreground">{lv.vg_name}</TableCell>
                      <TableCell className="text-muted-foreground">{formatBytes(lv.size)}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">{lv.attr}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground max-w-[200px] truncate" title={lv.path}>
                        {lv.path}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex items-center justify-end gap-1">
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title={t('disk.lvm.lv.resize')}
                            onClick={() => openLvResize(lv)}
                          >
                            <Maximize2 />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title={t('common.delete')}
                            onClick={() => setLvDeleteTarget(lv)}
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
          </div>
        </TabsContent>
      </Tabs>

      {/* Create PV Dialog */}
      <Dialog open={pvCreateOpen} onOpenChange={(open) => { setPvCreateOpen(open); if (!open) setPvDevice('') }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.lvm.pv.create')}</DialogTitle>
            <DialogDescription>{t('disk.lvm.pv.createDescription')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="pv-device">{t('disk.lvm.pv.device')}</Label>
            <Input
              id="pv-device"
              placeholder="e.g., /dev/sdb1"
              value={pvDevice}
              onChange={(e) => setPvDevice(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleCreatePV()}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setPvCreateOpen(false); setPvDevice('') }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreatePV} disabled={pvCreating || !pvDevice.trim()}>
              {pvCreating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete PV Dialog */}
      <Dialog open={!!pvDeleteTarget} onOpenChange={(open) => !open && setPvDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.lvm.pv.deleteTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="disk.lvm.pv.deleteConfirm"
                values={{ name: pvDeleteTarget?.name ?? '' }}
                components={{ strong: <span className="font-semibold font-mono" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPvDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleDeletePV} disabled={pvDeleting}>
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Create VG Dialog */}
      <Dialog open={vgCreateOpen} onOpenChange={(open) => { setVgCreateOpen(open); if (!open) { setVgName(''); setVgSelectedPvs([]) } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.lvm.vg.create')}</DialogTitle>
            <DialogDescription>{t('disk.lvm.vg.createDescription')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="vg-name">{t('disk.lvm.vg.vgName')}</Label>
              <Input
                id="vg-name"
                placeholder="e.g., my-vg"
                value={vgName}
                onChange={(e) => setVgName(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label>{t('disk.lvm.vg.selectPVs')}</Label>
              {freePvs.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t('disk.lvm.vg.noFreePVs')}</p>
              ) : (
                <div className="space-y-1.5">
                  {freePvs.map((pv) => (
                    <label
                      key={pv.name}
                      className={`flex items-center gap-3 rounded-lg px-3 py-2 cursor-pointer transition-colors ${
                        vgSelectedPvs.includes(pv.name)
                          ? 'bg-primary/10 ring-1 ring-primary/30'
                          : 'bg-muted/30 hover:bg-muted/50'
                      }`}
                    >
                      <input
                        type="checkbox"
                        checked={vgSelectedPvs.includes(pv.name)}
                        onChange={() => togglePvSelection(pv.name)}
                        className="rounded"
                      />
                      <span className="font-mono text-sm">{pv.name}</span>
                      <span className="text-xs text-muted-foreground ml-auto">{formatBytes(pv.size)}</span>
                    </label>
                  ))}
                </div>
              )}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setVgCreateOpen(false); setVgName(''); setVgSelectedPvs([]) }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreateVG} disabled={vgCreating || !vgName.trim() || vgSelectedPvs.length === 0}>
              {vgCreating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Create LV Dialog */}
      <Dialog open={lvCreateOpen} onOpenChange={(open) => { setLvCreateOpen(open); if (!open) { setLvName(''); setLvVgName(''); setLvSize('') } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.lvm.lv.create')}</DialogTitle>
            <DialogDescription>{t('disk.lvm.lv.createDescription')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="lv-name">{t('disk.lvm.lv.lvName')}</Label>
              <Input
                id="lv-name"
                placeholder="e.g., my-lv"
                value={lvName}
                onChange={(e) => setLvName(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="lv-vg">{t('disk.lvm.lv.selectVG')}</Label>
              <select
                id="lv-vg"
                value={lvVgName}
                onChange={(e) => setLvVgName(e.target.value)}
                className="flex h-9 w-full rounded-xl border-0 bg-secondary/50 px-3 py-1 text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/20"
              >
                {vgs.map((vg) => (
                  <option key={vg.name} value={vg.name}>
                    {vg.name} ({t('disk.lvm.free')}: {formatBytes(vg.free)})
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="lv-size">{t('disk.lvm.lv.lvSize')}</Label>
              <Input
                id="lv-size"
                placeholder="e.g., 10G, 100%FREE"
                value={lvSize}
                onChange={(e) => setLvSize(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">{t('disk.lvm.lv.sizeHint')}</p>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setLvCreateOpen(false); setLvName(''); setLvVgName(''); setLvSize('') }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreateLV} disabled={lvCreating || !lvName.trim() || !lvVgName || !lvSize.trim()}>
              {lvCreating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete LV Dialog */}
      <Dialog open={!!lvDeleteTarget} onOpenChange={(open) => !open && setLvDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.lvm.lv.deleteTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="disk.lvm.lv.deleteConfirm"
                values={{ name: lvDeleteTarget?.name ?? '', vg: lvDeleteTarget?.vg_name ?? '' }}
                components={{ strong: <span className="font-semibold font-mono" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setLvDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleDeleteLV} disabled={lvDeleting}>
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Resize LV Dialog */}
      <Dialog open={lvResizeOpen} onOpenChange={(open) => { setLvResizeOpen(open); if (!open) { setLvResizeTarget(null); setLvResizeNewSize('') } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.lvm.lv.resizeTitle')}</DialogTitle>
            <DialogDescription>
              {t('disk.lvm.lv.resizeDescription', { name: lvResizeTarget?.name ?? '' })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            {lvResizeTarget && (
              <div className="bg-muted/30 rounded-lg p-3 text-sm">
                <div className="grid grid-cols-2 gap-x-4 gap-y-1">
                  <span className="text-muted-foreground">{t('disk.lvm.lv.currentSize')}</span>
                  <span className="font-mono">{formatBytes(lvResizeTarget.size)}</span>
                  <span className="text-muted-foreground">{t('disk.lvm.lv.vgName')}</span>
                  <span className="font-mono">{lvResizeTarget.vg_name}</span>
                </div>
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="lv-resize">{t('disk.lvm.lv.newSize')}</Label>
              <Input
                id="lv-resize"
                placeholder="e.g., 20G, +5G, 100%FREE"
                value={lvResizeNewSize}
                onChange={(e) => setLvResizeNewSize(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">{t('disk.lvm.lv.resizeHint')}</p>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setLvResizeOpen(false); setLvResizeTarget(null); setLvResizeNewSize('') }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleResizeLV} disabled={lvResizing || !lvResizeNewSize.trim()}>
              {lvResizing ? t('disk.lvm.lv.resizing') : t('disk.lvm.lv.resize')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
