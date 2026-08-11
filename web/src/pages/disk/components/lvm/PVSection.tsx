import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Plus, Trash2 } from 'lucide-react'
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

import type { PhysicalVolume } from '@/types/api'

/** Physical Volumes tab: PV table + create/delete dialogs. */
export function PVSection({ pvs, onChanged }: {
  pvs: PhysicalVolume[]
  /** Called after any successful mutation so the parent can refetch. */
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const [createOpen, setCreateOpen] = useState(false)
  const [device, setDevice] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<PhysicalVolume | null>(null)

  // Single close-and-reset path, used by onOpenChange and the cancel button.
  const closeCreate = () => {
    setCreateOpen(false)
    setDevice('')
  }

  const { run: runCreate, loading: creating } = useApiAction(
    api.createPV.bind(api),
    {
      successMsg: t('disk.lvm.pv.createSuccess'),
      errorMsg: t('disk.lvm.pv.createFailed'),
      onSuccess: () => {
        closeCreate()
        onChanged()
      },
    },
  )

  const handleCreate = () => {
    if (device.trim()) void runCreate(device.trim())
  }

  const { run: runDelete, loading: deleting } = useApiAction(
    api.removePV.bind(api),
    {
      successMsg: t('disk.lvm.pv.deleted'),
      errorMsg: t('disk.lvm.pv.deleteFailed'),
      onSuccess: () => {
        setDeleteTarget(null)
        onChanged()
      },
    },
  )

  const handleDelete = () => {
    if (deleteTarget) void runDelete(deleteTarget.name)
  }

  return (
    <TabsContent value="pv">
      <div className="space-y-3 mt-3">
        <div className="flex items-center justify-end">
          <Button size="sm" onClick={() => setCreateOpen(true)} className="rounded-xl">
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
                  <TableCell className="text-muted-foreground">{pv.size}</TableCell>
                  <TableCell className="text-muted-foreground">{pv.free}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{pv.attr}</TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={t('common.delete')}
                      aria-label={t('common.delete')}
                      onClick={() => setDeleteTarget(pv)}
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

      {/* Create PV Dialog */}
      <Dialog open={createOpen} onOpenChange={(open) => { if (open) setCreateOpen(true); else closeCreate() }}>
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
              value={device}
              onChange={(e) => setDevice(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleCreate()}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={closeCreate}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreate} disabled={creating || !device.trim()}>
              {creating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete PV Dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.lvm.pv.deleteTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="disk.lvm.pv.deleteConfirm"
                values={{ name: deleteTarget?.name ?? '' }}
                components={{ strong: <span className="font-semibold font-mono" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleDelete} disabled={deleting}>
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </TabsContent>
  )
}
