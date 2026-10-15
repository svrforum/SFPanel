import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Trash2, Maximize2, CornerDownRight } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { useApiAction } from '@/hooks/useApiAction'
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
import { TabsContent } from '@/components/ui/tabs'
import { TypeToConfirmDialog } from '@/components/TypeToConfirmDialog'
import { NativeSelect } from '../NativeSelect'
import { fillPercent, isThinPool, lvRows } from './lvRows'

import type { VolumeGroup, LogicalVolume } from '@/types/api'

// A pool that runs out of data or metadata space stops every volume in it.
function fillClass(pct: number): string {
  if (pct >= 90) return 'text-destructive'
  if (pct >= 80) return 'text-warning'
  return 'text-muted-foreground'
}

/** Logical Volumes tab: LV table + create/delete/resize dialogs. */
export function LVSection({ lvs, vgs, onChanged }: {
  lvs: LogicalVolume[]
  vgs: VolumeGroup[]
  /** Called after any successful mutation so the parent can refetch. */
  onChanged: () => void
}) {
  const { t } = useTranslation()

  // Create
  const [createOpen, setCreateOpen] = useState(false)
  const [lvName, setLvName] = useState('')
  const [lvVgName, setLvVgName] = useState('')
  const [lvSize, setLvSize] = useState('')

  // Delete
  const [deleteTarget, setDeleteTarget] = useState<LogicalVolume | null>(null)

  // Resize
  const [resizeOpen, setResizeOpen] = useState(false)
  const [resizeTarget, setResizeTarget] = useState<LogicalVolume | null>(null)
  const [resizeNewSize, setResizeNewSize] = useState('')
  const [resizing, setResizing] = useState(false)
  // The server answers a size smaller than the volume's with
  // LV_SHRINK_CONFIRM_REQUIRED; the shrink goes ahead only once confirmed.
  const [shrinkConfirm, setShrinkConfirm] = useState(false)

  // Single close-and-reset paths, used by onOpenChange and the cancel buttons.
  const closeCreate = () => {
    setCreateOpen(false)
    setLvName('')
    setLvVgName('')
    setLvSize('')
  }

  const closeResize = () => {
    setResizeOpen(false)
    setResizeTarget(null)
    setResizeNewSize('')
    setShrinkConfirm(false)
  }

  const { run: runCreate, loading: creating } = useApiAction(
    api.createLV.bind(api),
    {
      successMsg: t('disk.lvm.lv.createSuccess'),
      errorMsg: t('disk.lvm.lv.createFailed'),
      onSuccess: () => {
        closeCreate()
        onChanged()
      },
    },
  )

  const handleCreate = () => {
    if (!lvName.trim() || !lvVgName || !lvSize.trim()) return
    void runCreate(lvName.trim(), lvVgName, lvSize.trim())
  }

  const { run: runDelete, loading: deleting } = useApiAction(
    api.removeLV.bind(api),
    {
      successMsg: t('disk.lvm.lv.deleted'),
      errorMsg: t('disk.lvm.lv.deleteFailed'),
      onSuccess: () => {
        setDeleteTarget(null)
        onChanged()
      },
    },
  )

  const handleDelete = () => {
    if (deleteTarget) void runDelete(deleteTarget.vg_name, deleteTarget.name)
  }

  const handleResize = async (shrink = false) => {
    if (!resizeTarget || !resizeNewSize.trim()) return
    setResizing(true)
    try {
      await api.resizeLV({ vg: resizeTarget.vg_name, name: resizeTarget.name, size: resizeNewSize.trim(), shrink })
      toast.success(t('disk.lvm.lv.resizeSuccess'))
      closeResize()
      onChanged()
    } catch (err: unknown) {
      if (!shrink && (err as { code?: string }).code === 'LV_SHRINK_CONFIRM_REQUIRED') {
        setShrinkConfirm(true)
        return
      }
      toast.error(err instanceof Error ? err.message : t('disk.lvm.lv.resizeFailed'))
    } finally {
      setResizing(false)
    }
  }

  const openResize = (lv: LogicalVolume) => {
    setResizeTarget(lv)
    setResizeNewSize('')
    setResizeOpen(true)
  }

  return (
    <TabsContent value="lv">
      <div className="space-y-3 mt-3">
        <div className="flex items-center justify-end">
          <Button size="sm" onClick={() => { setCreateOpen(true); if (vgs.length > 0) setLvVgName(vgs[0].name) }} className="rounded-xl" disabled={vgs.length === 0}>
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
              {lvRows(lvs).map(({ lv, nested }) => {
                const pool = isThinPool(lv)
                const data = pool ? fillPercent(lv.data_percent) : null
                const meta = pool ? fillPercent(lv.metadata_percent) : null
                // Proxmox owns a guest's volume and a storage's pool: the
                // server refuses to remove either, and to resize a guest's.
                const canResize = !lv.owner || pool
                const canDelete = !lv.owner
                return (
                  <TableRow key={`${lv.vg_name}/${lv.name}`}>
                    <TableCell className="font-medium">
                      <div className={`flex items-center gap-1.5 min-w-0 ${nested ? 'pl-4' : ''}`}>
                        {nested && <CornerDownRight className="h-3 w-3 shrink-0 text-muted-foreground" aria-hidden="true" />}
                        <span className="truncate">{lv.name}</span>
                        {pool && (
                          <span className="shrink-0 inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium border border-border">
                            {t('disk.lvm.lv.thinPool')}
                          </span>
                        )}
                        {lv.owner && (
                          <span className="shrink-0 inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium bg-primary/10 text-primary" title={t('disk.lvm.lv.managedByProxmox')}>
                            {lv.owner}
                          </span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{lv.vg_name}</TableCell>
                    <TableCell className="text-muted-foreground">
                      {lv.size}
                      {(data !== null || meta !== null) && (
                        <div className="text-[11px] whitespace-nowrap">
                          {data !== null && <span className={fillClass(data)}>{t('disk.lvm.lv.dataFill', { value: data.toFixed(1) })}</span>}
                          {data !== null && meta !== null && <span className="text-muted-foreground"> · </span>}
                          {meta !== null && <span className={fillClass(meta)}>{t('disk.lvm.lv.metadataFill', { value: meta.toFixed(1) })}</span>}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">{lv.attr}</TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground max-w-[200px] truncate" title={lv.path}>
                      {lv.path}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        {canResize && (
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title={t('disk.lvm.lv.resize')}
                            aria-label={t('disk.lvm.lv.resize')}
                            onClick={() => openResize(lv)}
                          >
                            <Maximize2 />
                          </Button>
                        )}
                        {canDelete && (
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title={t('common.delete')}
                            aria-label={t('common.delete')}
                            onClick={() => setDeleteTarget(lv)}
                          >
                            <Trash2 />
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* Create LV Dialog */}
      <Dialog open={createOpen} onOpenChange={(open) => { if (open) setCreateOpen(true); else closeCreate() }}>
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
              <NativeSelect
                id="lv-vg"
                value={lvVgName}
                onChange={(e) => setLvVgName(e.target.value)}
                className="w-full"
              >
                {vgs.map((vg) => (
                  <option key={vg.name} value={vg.name}>
                    {vg.name} ({t('disk.lvm.free')}: {vg.free})
                  </option>
                ))}
              </NativeSelect>
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
            <Button variant="outline" onClick={closeCreate}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreate} disabled={creating || !lvName.trim() || !lvVgName || !lvSize.trim()}>
              {creating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete LV — lvremove destroys the filesystem, so require typing the
          LV name (same guard as partition/RAID delete and format). */}
      <TypeToConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t('disk.lvm.lv.deleteTitle')}
        description={t('disk.lvm.lv.deleteConfirmDesc', { name: deleteTarget?.name ?? '', vg: deleteTarget?.vg_name ?? '' })}
        confirmPhrase={deleteTarget?.name ?? ''}
        confirmLabel={t('common.delete')}
        loading={deleting}
        onConfirm={handleDelete}
      />

      {/* Resize LV Dialog */}
      <Dialog open={resizeOpen} onOpenChange={(open) => { if (open) setResizeOpen(true); else closeResize() }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.lvm.lv.resizeTitle')}</DialogTitle>
            <DialogDescription>
              {t('disk.lvm.lv.resizeDescription', { name: resizeTarget?.name ?? '' })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            {resizeTarget && (
              <div className="bg-muted/30 rounded-lg p-3 text-sm">
                <div className="grid grid-cols-2 gap-x-4 gap-y-1">
                  <span className="text-muted-foreground">{t('disk.lvm.lv.currentSize')}</span>
                  <span className="font-mono">{resizeTarget.size}</span>
                  <span className="text-muted-foreground">{t('disk.lvm.lv.vgName')}</span>
                  <span className="font-mono">{resizeTarget.vg_name}</span>
                </div>
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="lv-resize">{t('disk.lvm.lv.newSize')}</Label>
              <Input
                id="lv-resize"
                placeholder="e.g., 20G, +5G, +100%FREE"
                value={resizeNewSize}
                onChange={(e) => setResizeNewSize(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">{t('disk.lvm.lv.resizeHint')}</p>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={closeResize}>
              {t('common.cancel')}
            </Button>
            <Button onClick={() => void handleResize()} disabled={resizing || !resizeNewSize.trim()}>
              {resizing ? t('disk.lvm.lv.resizing') : t('disk.lvm.lv.resize')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* A smaller size cuts off the end of the volume, and whatever file
          system is on it with it. */}
      <TypeToConfirmDialog
        open={shrinkConfirm}
        onOpenChange={setShrinkConfirm}
        title={t('disk.lvm.lv.shrinkTitle', { name: resizeTarget?.name ?? '' })}
        description={t('disk.lvm.lv.shrinkDesc', { name: resizeTarget?.name ?? '', from: resizeTarget?.size ?? '', to: resizeNewSize.trim() })}
        confirmPhrase={resizeTarget?.name ?? ''}
        confirmLabel={t('disk.lvm.lv.shrink')}
        loading={resizing}
        onConfirm={() => void handleResize(true)}
      />
    </TabsContent>
  )
}
