import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { HardDrive, Layers, Box, AlertTriangle } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { PVSection } from './components/lvm/PVSection'
import { VGSection } from './components/lvm/VGSection'
import { LVSection } from './components/lvm/LVSection'
import { TabLoading, RefreshButton } from './components/TabToolbar'

import type { PhysicalVolume, VolumeGroup, LogicalVolume } from '@/types/api'

export default function DiskLVM() {
  const { t } = useTranslation()
  const [pvs, setPvs] = useState<PhysicalVolume[]>([])
  const [vgs, setVgs] = useState<VolumeGroup[]>([])
  const [lvs, setLvs] = useState<LogicalVolume[]>([])
  // What LVM printed beside its reports — an old PV header, a missing PV.
  // It emptied the whole tab when the server could not parse past it.
  const [warnings, setWarnings] = useState<string[]>([])
  const [loading, setLoading] = useState(true)

  const fetchLVM = useCallback(async () => {
    try {
      setLoading(true)
      const [pvData, vgData, lvData] = await Promise.all([
        api.getLVMPVs(),
        api.getLVMVGs(),
        api.getLVMLVs(),
      ])
      setPvs(pvData.items)
      setVgs(vgData.items)
      setLvs(lvData.items)
      setWarnings([...new Set([...pvData.warnings, ...vgData.warnings, ...lvData.warnings])])
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

  if (loading) {
    return <TabLoading />
  }

  return (
    <div className="space-y-4 mt-4">
      {/* Toolbar */}
      <div className="flex items-center justify-end">
        <RefreshButton onClick={fetchLVM} loading={loading} />
      </div>

      {warnings.length > 0 && (
        <div className="flex gap-3 bg-warning/10 border border-warning/30 rounded-2xl px-5 py-3.5" role="alert">
          <AlertTriangle className="h-5 w-5 text-warning shrink-0" aria-hidden="true" />
          <div className="min-w-0">
            <p className="text-[13px] font-medium">{t('disk.lvm.warningsTitle')}</p>
            <ul className="mt-1 space-y-0.5">
              {warnings.map((w) => (
                <li key={w} className="text-[12px] font-mono text-muted-foreground break-all">{w}</li>
              ))}
            </ul>
          </div>
        </div>
      )}

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

        <PVSection pvs={pvs} onChanged={fetchLVM} />
        <VGSection vgs={vgs} pvs={pvs} onChanged={fetchLVM} />
        <LVSection lvs={lvs} vgs={vgs} onChanged={fetchLVM} />
      </Tabs>
    </div>
  )
}
