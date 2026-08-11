import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Trash2, Maximize2 } from 'lucide-react'
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

import type { VolumeGroup, LogicalVolume } from '@/types/api'

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

  const { run: runResize, loading: resizing } = useApiAction(
    api.resizeLV.bind(api),
    {
      successMsg: t('disk.lvm.lv.resizeSuccess'),
      errorMsg: t('disk.lvm.lv.resizeFailed'),
      onSuccess: () => {
        closeResize()
        onChanged()
      },
    },
  )

  const handleResize = () => {
    if (!resizeTarget || !resizeNewSize.trim()) return
    void runResize({ vg: resizeTarget.vg_name, name: resizeTarget.name, size: resizeNewSize.trim() })
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
              {lvs.map((lv) => (
                <TableRow key={lv.path}>
                  <TableCell className="font-medium">{lv.name}</TableCell>
                  <TableCell className="text-muted-foreground">{lv.vg_name}</TableCell>
                  <TableCell className="text-muted-foreground">{lv.size}</TableCell>
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
                        aria-label={t('disk.lvm.lv.resize')}
                        onClick={() => openResize(lv)}
                      >
                        <Maximize2 />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={t('common.delete')}
                        aria-label={t('common.delete')}
                        onClick={() => setDeleteTarget(lv)}
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
                placeholder="e.g., 20G, +5G, 100%FREE"
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
            <Button onClick={handleResize} disabled={resizing || !resizeNewSize.trim()}>
              {resizing ? t('disk.lvm.lv.resizing') : t('disk.lvm.lv.resize')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </TabsContent>
  )
}
