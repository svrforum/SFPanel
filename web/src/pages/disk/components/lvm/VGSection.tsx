import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus } from 'lucide-react'
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

import type { PhysicalVolume, VolumeGroup } from '@/types/api'

/** Volume Groups tab: VG table + create dialog (picks from free PVs). */
export function VGSection({ vgs, pvs, onChanged }: {
  vgs: VolumeGroup[]
  pvs: PhysicalVolume[]
  /** Called after any successful mutation so the parent can refetch. */
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const [createOpen, setCreateOpen] = useState(false)
  const [vgName, setVgName] = useState('')
  const [selectedPvs, setSelectedPvs] = useState<string[]>([])

  // Unassigned PVs for VG creation
  const freePvs = pvs.filter((pv) => !pv.vg_name)

  // Single close-and-reset path, used by onOpenChange and the cancel button.
  const closeCreate = () => {
    setCreateOpen(false)
    setVgName('')
    setSelectedPvs([])
  }

  const { run: runCreate, loading: creating } = useApiAction(
    api.createVG.bind(api),
    {
      successMsg: t('disk.lvm.vg.createSuccess'),
      errorMsg: t('disk.lvm.vg.createFailed'),
      onSuccess: () => {
        closeCreate()
        onChanged()
      },
    },
  )

  const handleCreate = () => {
    if (!vgName.trim() || selectedPvs.length === 0) return
    void runCreate(vgName.trim(), selectedPvs)
  }

  const togglePvSelection = (pvName: string) => {
    setSelectedPvs((prev) =>
      prev.includes(pvName) ? prev.filter((p) => p !== pvName) : [...prev, pvName]
    )
  }

  return (
    <TabsContent value="vg">
      <div className="space-y-3 mt-3">
        <div className="flex items-center justify-end">
          <Button size="sm" onClick={() => setCreateOpen(true)} className="rounded-xl" disabled={freePvs.length === 0}>
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
                  <TableCell className="text-muted-foreground">{vg.size}</TableCell>
                  <TableCell className="text-muted-foreground">{vg.free}</TableCell>
                  <TableCell className="text-muted-foreground">{vg.pv_count}</TableCell>
                  <TableCell className="text-muted-foreground">{vg.lv_count}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{vg.attr}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* Create VG Dialog */}
      <Dialog open={createOpen} onOpenChange={(open) => { if (open) setCreateOpen(true); else closeCreate() }}>
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
                        selectedPvs.includes(pv.name)
                          ? 'bg-primary/10 ring-1 ring-primary/30'
                          : 'bg-muted/30 hover:bg-muted/50'
                      }`}
                    >
                      <input
                        type="checkbox"
                        checked={selectedPvs.includes(pv.name)}
                        onChange={() => togglePvSelection(pv.name)}
                        className="rounded"
                      />
                      <span className="font-mono text-sm">{pv.name}</span>
                      <span className="text-xs text-muted-foreground ml-auto">{pv.size}</span>
                    </label>
                  ))}
                </div>
              )}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={closeCreate}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreate} disabled={creating || !vgName.trim() || selectedPvs.length === 0}>
              {creating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </TabsContent>
  )
}
