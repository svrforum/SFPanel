import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface DockerPruneProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export default function DockerPrune({ open, onOpenChange }: DockerPruneProps) {
  const { t } = useTranslation()
  const [selected, setSelected] = useState({ containers: true, images: true, volumes: false, networks: true })
  const [pruning, setPruning] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)

  const toggleAll = (checked: boolean) => {
    setSelected({ containers: checked, images: checked, volumes: checked, networks: checked })
  }

  const allSelected = Object.values(selected).every(Boolean)
  const noneSelected = Object.values(selected).every(v => !v)

  const handlePrune = async () => {
    setConfirmOpen(false)
    setPruning(true)
    try {
      if (allSelected) {
        const report = await api.pruneAll()
        const parts: string[] = []
        if (report.containers.deleted > 0) parts.push(`${report.containers.deleted} containers`)
        if (report.images.deleted > 0) parts.push(`${report.images.deleted} images`)
        if (report.volumes.deleted > 0) parts.push(`${report.volumes.deleted} volumes`)
        if (report.networks.deleted > 0) parts.push(`${report.networks.deleted} networks`)
        const totalSpace = (report.containers.space_reclaimed || 0) + (report.images.space_reclaimed || 0) + (report.volumes.space_reclaimed || 0)
        const msg = parts.length > 0
          ? `${t('docker.prune.success')}: ${parts.join(', ')}${totalSpace > 0 ? ` (${formatBytes(totalSpace)})` : ''}`
          : t('docker.prune.success')
        toast.success(msg)
      } else {
        const results: string[] = []
        if (selected.containers) {
          const r = await api.pruneContainers()
          if (r.deleted > 0) results.push(`${r.deleted} containers`)
        }
        if (selected.images) {
          const r = await api.pruneImages()
          if (r.deleted > 0) results.push(`${r.deleted} images`)
        }
        if (selected.volumes) {
          const r = await api.pruneVolumes()
          if (r.deleted > 0) results.push(`${r.deleted} volumes`)
        }
        if (selected.networks) {
          const r = await api.pruneNetworks()
          if (r.deleted > 0) results.push(`${r.deleted} networks`)
        }
        toast.success(results.length > 0 ? `${t('docker.prune.success')}: ${results.join(', ')}` : t('docker.prune.success'))
      }
      onOpenChange(false)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : 'Prune failed')
    } finally {
      setPruning(false)
    }
  }

  const items = [
    { key: 'containers' as const, label: t('docker.prune.containers') },
    { key: 'images' as const, label: t('docker.prune.images') },
    { key: 'volumes' as const, label: t('docker.prune.volumes') },
    { key: 'networks' as const, label: t('docker.prune.networks') },
  ]

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Trash2 className="h-5 w-5" />
              {t('docker.prune.title')}
            </DialogTitle>
            <DialogDescription>{t('docker.prune.description')}</DialogDescription>
          </DialogHeader>

          <div className="space-y-3">
            <label className="flex items-center gap-2 cursor-pointer">
              <input type="checkbox" checked={allSelected} onChange={(e) => toggleAll(e.target.checked)}
                className="rounded" />
              <span className="text-[13px] font-medium">{t('docker.prune.selectAll')}</span>
            </label>
            <div className="space-y-2 pl-1">
              {items.map(item => (
                <label key={item.key} className="flex items-center gap-2 cursor-pointer">
                  <input type="checkbox" checked={selected[item.key]}
                    onChange={(e) => setSelected({ ...selected, [item.key]: e.target.checked })}
                    className="rounded" />
                  <span className="text-[13px]">{item.label}</span>
                </label>
              ))}
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => onOpenChange(false)}>{t('common.cancel')}</Button>
            <Button variant="destructive" disabled={noneSelected || pruning}
              onClick={() => setConfirmOpen(true)}>
              {pruning ? t('docker.prune.pruning') : t('docker.prune.pruneSelected')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('docker.prune.confirmTitle')}</DialogTitle>
            <DialogDescription>{t('docker.prune.confirmDescription')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmOpen(false)}>{t('common.cancel')}</Button>
            <Button variant="destructive" onClick={handlePrune} disabled={pruning}>
              {pruning ? t('docker.prune.pruning') : t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
