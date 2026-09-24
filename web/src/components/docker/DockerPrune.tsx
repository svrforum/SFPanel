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
  const [report, setReport] = useState<string[]>([])
  const [confirmOpen, setConfirmOpen] = useState(false)

  const toggleAll = (checked: boolean) => {
    setSelected({ containers: checked, images: checked, volumes: checked, networks: checked })
  }

  const allSelected = Object.values(selected).every(Boolean)
  const noneSelected = Object.values(selected).every(v => !v)

  const handlePrune = async () => {
    setConfirmOpen(false)
    setPruning(true)
    const results: string[] = []
    let failed = false
    const actions = { containers: () => api.pruneContainers(), images: () => api.pruneImages(), volumes: () => api.pruneVolumes(), networks: () => api.pruneNetworks() }
    try {
      for (const key of Object.keys(actions) as (keyof typeof actions)[]) {
        if (!selected[key]) continue
        try {
          const result = await actions[key]()
          results.push(`${t(`docker.sidebar.${key}`)}: ${result.deleted} · ${formatBytes(result.space_reclaimed || 0)}`)
        } catch (err) {
          failed = true
          results.push(`${t(`docker.sidebar.${key}`)}: ${err instanceof Error ? err.message : String(err)}`)
        }
      }
      setReport(results)
      if (failed) toast.error(t('docker.improvements.partialPrune'))
      else toast.success(t('docker.prune.success'))
    } finally {
      window.dispatchEvent(new Event('docker-resources-changed'))
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
      <Dialog open={open} onOpenChange={value => { if (!pruning) { setReport([]); onOpenChange(value) } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Trash2 className="h-5 w-5" />
              {t('docker.prune.title')}
            </DialogTitle>
            <DialogDescription>{t('docker.prune.description')}</DialogDescription>
          </DialogHeader>

          <div className="space-y-3">
            {report.length > 0 && <ul role="status" className="space-y-1 break-words text-sm">{report.map((line, index) => <li key={index}>{line}</li>)}</ul>}
            <label className="flex items-center gap-2 cursor-pointer">
              <input type="checkbox" disabled={pruning} checked={allSelected} onChange={(e) => toggleAll(e.target.checked)}
                className="rounded outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0" />
              <span className="text-[13px] font-medium">{t('docker.prune.selectAll')}</span>
            </label>
            <div className="space-y-2 pl-1">
              {items.map(item => (
                <label key={item.key} className="flex items-center gap-2 cursor-pointer">
                  <input type="checkbox" disabled={pruning} checked={selected[item.key]}
                    onChange={(e) => setSelected({ ...selected, [item.key]: e.target.checked })}
                    className="rounded outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0" />
                  <span className="text-[13px]">{item.label}</span>
                </label>
              ))}
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" disabled={pruning} onClick={() => { setReport([]); onOpenChange(false) }}>{t('common.cancel')}</Button>
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
            <DialogDescription>{t('docker.prune.confirmDescription')}<span className="mt-2 block">{items.filter(item => selected[item.key]).map(item => item.label).join(' · ')}</span></DialogDescription>
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
