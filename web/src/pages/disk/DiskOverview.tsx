import { useState, useEffect, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { HardDrive, Activity, RefreshCw, ChevronRight, AlertTriangle, Download, Terminal, CheckCircle2, XCircle } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { DockerVolumeUsageCard } from '@/components/disk/DockerVolumeUsageCard'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { SmartInfoDialog } from './components/SmartInfoDialog'
import { TabLoading, CountPill, RefreshButton } from './components/TabToolbar'

import type { BlockDevice, IOStat } from '@/types/api'

function diskTypeBadge(type_: string, rotational: boolean) {
  const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
  if (!rotational || type_ === 'ssd') {
    return <span className={`${base} bg-primary/10 text-primary`}>SSD</span>
  }
  return <span className={`${base} bg-secondary text-muted-foreground`}>HDD</span>
}

export default function DiskOverview() {
  const { t } = useTranslation()
  const [disks, setDisks] = useState<BlockDevice[]>([])
  const [iostats, setIostats] = useState<IOStat[]>([])
  const [loading, setLoading] = useState(true)
  const [smartDisk, setSmartDisk] = useState<string | null>(null)

  // Smartmontools status
  const [smartmontoolsInstalled, setSmartmontoolsInstalled] = useState<boolean | null>(null)
  const [installingSmartmontools, setInstallingSmartmontools] = useState(false)
  const [installModalOpen, setInstallModalOpen] = useState(false)
  const [installOutput, setInstallOutput] = useState('')
  const [installSuccess, setInstallSuccess] = useState<boolean | null>(null)

  // O(1) IOStat lookup map
  const iostatMap = useMemo(() => {
    const map = new Map<string, IOStat>()
    for (const stat of iostats) {
      map.set(stat.device, stat)
    }
    return map
  }, [iostats])

  const getIOStatForDevice = useCallback((deviceName: string): IOStat | undefined => {
    const name = deviceName.replace('/dev/', '')
    return iostatMap.get(name) || iostatMap.get(deviceName)
  }, [iostatMap])

  const fetchData = useCallback(async () => {
    try {
      setLoading(true)
      const [overviewData, iostatData, smartmontoolsStatus] = await Promise.all([
        api.getDiskOverview(),
        api.getDiskIOStats(),
        api.checkSmartmontools(),
      ])
      setDisks(overviewData || [])
      setIostats(iostatData || [])
      setSmartmontoolsInstalled(smartmontoolsStatus?.installed ?? false)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.overview.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchData()
  }, [fetchData])

  const handleInstallSmartmontools = async () => {
    setInstallModalOpen(true)
    setInstallingSmartmontools(true)
    setInstallOutput('')
    setInstallSuccess(null)
    try {
      const result = await api.installSmartmontools()
      setInstallOutput(result.output || t('disk.overview.installSuccess'))
      setInstallSuccess(true)
      setSmartmontoolsInstalled(true)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.overview.installFailed')
      setInstallOutput(message)
      setInstallSuccess(false)
    } finally {
      setInstallingSmartmontools(false)
    }
  }

  if (loading) {
    return <TabLoading />
  }

  return (
    <div className="space-y-4 mt-4">
      <DockerVolumeUsageCard />
      {/* Smartmontools Install Banner */}
      {smartmontoolsInstalled === false && (
        <div className="flex items-center gap-3 bg-warning/10 border border-warning/30 rounded-2xl px-5 py-3.5" role="alert">
          <AlertTriangle className="h-5 w-5 text-warning shrink-0" aria-hidden="true" />
          <div className="flex-1">
            <p className="text-[13px] font-medium">{t('disk.overview.smartmontoolsNotInstalled')}</p>
            <p className="text-[12px] text-muted-foreground mt-0.5">{t('disk.overview.smartmontoolsHint')}</p>
          </div>
          <Button
            size="sm"
            onClick={handleInstallSmartmontools}
            disabled={installingSmartmontools}
            className="rounded-xl shrink-0"
          >
            <Download className="h-3.5 w-3.5" />
            {installingSmartmontools ? t('disk.overview.installing') : t('disk.overview.installSmartmontools')}
          </Button>
        </div>
      )}

      {/* Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <CountPill>{t('disk.overview.diskCount', { count: disks.length })}</CountPill>
        <RefreshButton onClick={fetchData} loading={loading} />
      </div>

      {/* Disk Cards */}
      {disks.length === 0 ? (
        <div className="bg-card rounded-2xl card-shadow p-8 text-center text-muted-foreground">
          {t('disk.overview.noDisks')}
        </div>
      ) : (
        <div className="space-y-4">
          {disks.map((disk) => {
            const iostat = getIOStatForDevice(disk.name)
            return (
              <div key={disk.name} className="bg-card rounded-2xl card-shadow overflow-hidden" role="region" aria-label={`${disk.name} ${formatBytes(disk.size)}`}>
                {/* Disk Header */}
                <div className="p-5 border-b border-border/50">
                  <div className="flex items-center justify-between gap-3">
                    <div className="flex items-center gap-3 min-w-0">
                      <div className="p-2 rounded-xl bg-primary/10">
                        <HardDrive className="h-5 w-5 text-primary" aria-hidden="true" />
                      </div>
                      <div className="min-w-0">
                        <div className="flex items-center gap-2 min-w-0">
                          <h3 className="font-semibold text-[15px] truncate min-w-0">{disk.name}</h3>
                          {diskTypeBadge(disk.type, disk.rotational)}
                        </div>
                        <div className="text-[13px] text-muted-foreground mt-0.5 truncate" title={disk.model || undefined}>
                          {disk.model || t('disk.overview.unknownModel')}
                        </div>
                      </div>
                    </div>
                    <div className="flex items-center gap-4 shrink-0">
                      <div className="text-right">
                        <div className="text-lg font-bold">{formatBytes(disk.size)}</div>
                        {disk.transport && (
                          <div className="text-[11px] text-muted-foreground">{disk.transport.toUpperCase()}</div>
                        )}
                      </div>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setSmartDisk(disk.name)}
                        className="rounded-xl"
                      >
                        <Activity className="h-3.5 w-3.5" />
                        {t('disk.smart.title')}
                      </Button>
                    </div>
                  </div>

                  {/* Disk Meta */}
                  {disk.serial && (
                    <div className="mt-3 flex items-center gap-4 text-[12px] text-muted-foreground">
                      <span>{t('disk.overview.serial')}: <span className="font-mono">{disk.serial}</span></span>
                    </div>
                  )}
                </div>

                {/* Partition Tree */}
                {disk.children && disk.children.length > 0 && (
                  <div className="px-5 py-3">
                    <h4 className="text-[12px] font-medium text-muted-foreground mb-2">
                      {t('disk.overview.partitions')} ({disk.children.length})
                    </h4>
                    <div className="space-y-1">
                      {disk.children.map((child) => (
                        <div
                          key={child.name}
                          className="flex items-center gap-3 bg-muted/30 rounded-lg px-3 py-2 text-[13px] min-w-0"
                        >
                          <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" aria-hidden="true" />
                          <span className="font-mono font-medium w-28 shrink-0 truncate">{child.name}</span>
                          <span className="text-muted-foreground w-20 shrink-0">{formatBytes(child.size)}</span>
                          {child.fstype && (
                            <span className="inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium border border-border shrink-0">
                              {child.fstype}
                            </span>
                          )}
                          {child.mountpoint && (
                            <span className="text-muted-foreground font-mono text-xs truncate min-w-0 flex-1" title={child.mountpoint}>
                              {child.mountpoint}
                            </span>
                          )}
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {/* I/O Stats */}
                {iostat && (
                  <div className="px-5 py-3 border-t border-border/50 bg-muted/20">
                    <h4 className="text-[12px] font-medium text-muted-foreground mb-2 flex items-center gap-1.5">
                      <Activity className="h-3 w-3" aria-hidden="true" />
                      {t('disk.overview.ioStats')}
                    </h4>
                    <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 text-[13px]">
                      <div>
                        <div className="text-muted-foreground text-[11px]">{t('disk.overview.readOps')}</div>
                        <div className="font-medium">{iostat.read_ops.toLocaleString()}</div>
                      </div>
                      <div>
                        <div className="text-muted-foreground text-[11px]">{t('disk.overview.writeOps')}</div>
                        <div className="font-medium">{iostat.write_ops.toLocaleString()}</div>
                      </div>
                      <div>
                        <div className="text-muted-foreground text-[11px]">{t('disk.overview.readBytes')}</div>
                        <div className="font-medium">{formatBytes(iostat.read_bytes)}</div>
                      </div>
                      <div>
                        <div className="text-muted-foreground text-[11px]">{t('disk.overview.writeBytes')}</div>
                        <div className="font-medium">{formatBytes(iostat.write_bytes)}</div>
                      </div>
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {/* Smartmontools Install Modal */}
      <Dialog open={installModalOpen} onOpenChange={(open) => { if (!installingSmartmontools) setInstallModalOpen(open) }}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Terminal className="h-4 w-4" />
              {t('disk.overview.installSmartmontools')}
            </DialogTitle>
            <DialogDescription>apt-get install -y smartmontools</DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            {installingSmartmontools && (
              <div className="flex items-center gap-2 text-[13px] text-muted-foreground">
                <RefreshCw className="h-3.5 w-3.5 animate-spin" />
                {t('disk.overview.installing')}
              </div>
            )}
            {installOutput && (
              <pre className="bg-zinc-950 text-zinc-200 rounded-xl p-4 text-[12px] font-mono max-h-[300px] overflow-y-auto whitespace-pre-wrap leading-relaxed">
                {installOutput}
              </pre>
            )}
            {installSuccess !== null && (
              <div className={`flex items-center gap-2 text-[13px] font-medium ${
                installSuccess ? 'text-success' : 'text-destructive'
              }`}>
                {installSuccess
                  ? <><CheckCircle2 className="h-4 w-4" />{t('disk.overview.installSuccess')}</>
                  : <><XCircle className="h-4 w-4" />{t('disk.overview.installFailed')}</>
                }
              </div>
            )}
          </div>
          {!installingSmartmontools && (
            <div className="flex justify-end">
              <Button variant="outline" size="sm" onClick={() => setInstallModalOpen(false)} className="rounded-xl">
                {t('common.close')}
              </Button>
            </div>
          )}
        </DialogContent>
      </Dialog>

      {/* SMART Data Dialog */}
      <SmartInfoDialog diskName={smartDisk} onOpenChange={(open) => { if (!open) setSmartDisk(null) }} />
    </div>
  )
}
