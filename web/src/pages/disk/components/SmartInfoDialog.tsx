import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { HardDrive, Activity, ThermometerSun, RefreshCw, Info, CheckCircle2, XCircle } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
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
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

import type { SmartInfo } from '@/types/api'

function getSmartAttrStatus(value: number, worst: number, threshold: number): string {
  if (threshold === 0) return 'ok'
  if (value <= threshold || worst <= threshold) return 'fail'
  const margin = (value - threshold) / threshold
  if (margin < 0.1) return 'warn'
  return 'ok'
}

function smartStatusStyle(status: string | undefined, value?: number, worst?: number, threshold?: number) {
  const computed = status || (value !== undefined && worst !== undefined && threshold !== undefined
    ? getSmartAttrStatus(value, worst, threshold)
    : 'ok')
  const base = 'inline-flex items-center px-1.5 py-0 rounded-full text-[10px] font-medium'
  switch (computed) {
    case 'ok':
    case 'passed':
      return { className: `${base} bg-success/10 text-success`, label: 'OK' }
    case 'warn':
      return { className: `${base} bg-warning/10 text-warning`, label: 'WARN' }
    default:
      return { className: `${base} bg-destructive/10 text-destructive`, label: 'FAIL' }
  }
}

/**
 * SMART detail viewer for a single disk: health summary, self-test controls
 * and log, and the raw attribute table. Loads its own data when a disk is
 * selected; a fetch failure toasts and closes the dialog (previous behaviour).
 */
export function SmartInfoDialog({ diskName, onOpenChange }: {
  /** Device to inspect; null keeps the dialog closed. */
  diskName: string | null
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const [smartData, setSmartData] = useState<SmartInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const [runningTest, setRunningTest] = useState(false)

  // Reset before paint when a new disk opens (adjust-state-during-render, same
  // pattern as TypeToConfirmDialog) so stale data never flashes.
  const [prevDisk, setPrevDisk] = useState<string | null>(diskName)
  if (prevDisk !== diskName) {
    setPrevDisk(diskName)
    if (diskName) {
      setSmartData(null)
      setLoading(true)
    }
  }

  const fetchSmart = async (name: string) => {
    setLoading(true)
    try {
      const data = await api.getDiskSmart(name)
      setSmartData(data)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.smart.fetchFailed')
      toast.error(message)
      onOpenChange(false)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (diskName) void fetchSmart(diskName)
    // fetchSmart is not memoized; the load is keyed to the selected disk only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [diskName])

  const handleRunSmartTest = async (type: 'short' | 'long') => {
    if (!diskName) return
    setRunningTest(true)
    try {
      const result = await api.runSmartTest(diskName, type)
      toast.success(result.output?.trim() || t('disk.smart.selfTest.started'))
      await fetchSmart(diskName)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      setRunningTest(false)
    }
  }

  return (
    <Dialog open={!!diskName} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl max-h-[85vh]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Activity className="h-4 w-4" />
            {t('disk.smart.title')} - {diskName}
          </DialogTitle>
          <DialogDescription>{t('disk.smart.description')}</DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="flex items-center justify-center py-8 text-muted-foreground">
            {t('common.loading')}
          </div>
        ) : smartData ? (
          <div className="space-y-4 max-h-[500px] overflow-y-auto pr-1">
            {/* Health Summary */}
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              <div className="bg-secondary/30 rounded-xl py-3 px-4 text-center">
                <div className="flex items-center justify-center gap-1.5 mb-1">
                  <Info className="h-3.5 w-3.5 text-primary" aria-hidden="true" />
                  <span className="text-[12px] text-muted-foreground">{t('disk.smart.health')}</span>
                </div>
                <span className={`text-lg font-bold ${
                  smartData.healthy === null
                    ? 'text-muted-foreground'
                    : smartData.healthy
                      ? 'text-success'
                      : 'text-destructive'
                }`}>
                  {smartData.healthy === null
                    ? t('disk.smart.notSupported')
                    : smartData.healthy
                      ? t('disk.smart.healthy')
                      : t('disk.smart.unhealthy')}
                </span>
              </div>
              <div className="bg-secondary/30 rounded-xl py-3 px-4 text-center">
                <div className="flex items-center justify-center gap-1.5 mb-1">
                  <ThermometerSun className="h-3.5 w-3.5 text-warning" aria-hidden="true" />
                  <span className="text-[12px] text-muted-foreground">{t('disk.smart.temperature')}</span>
                </div>
                <span className={`text-lg font-bold ${smartData.temperature > 50 ? 'text-destructive' : smartData.temperature > 40 ? 'text-warning' : ''}`}>
                  {smartData.temperature}&deg;C
                </span>
              </div>
              <div className="bg-secondary/30 rounded-xl py-3 px-4 text-center">
                <div className="flex items-center justify-center gap-1.5 mb-1">
                  <HardDrive className="h-3.5 w-3.5 text-primary" aria-hidden="true" />
                  <span className="text-[12px] text-muted-foreground">{t('disk.smart.powerOnHours')}</span>
                </div>
                <span className="text-lg font-bold">
                  {smartData.power_on_hours.toLocaleString()}h
                </span>
              </div>
            </div>

            {/* Self-Test Controls */}
            <div className="bg-secondary/30 rounded-xl p-4 space-y-2.5">
              <div className="flex items-center justify-between gap-3 flex-wrap">
                <h4 className="text-[13px] font-semibold">{t('disk.smart.selfTest.runTitle')}</h4>
                <div className="flex items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => handleRunSmartTest('short')}
                    disabled={runningTest}
                    className="rounded-xl"
                  >
                    {runningTest && <RefreshCw className="h-3.5 w-3.5 animate-spin" />}
                    {t('disk.smart.selfTest.short')}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => handleRunSmartTest('long')}
                    disabled={runningTest}
                    className="rounded-xl"
                  >
                    {runningTest && <RefreshCw className="h-3.5 w-3.5 animate-spin" />}
                    {t('disk.smart.selfTest.long')}
                  </Button>
                </div>
              </div>
              <p className="text-[12px] text-muted-foreground">
                {runningTest ? t('disk.smart.selfTest.running') : t('disk.smart.selfTest.hint')}
              </p>
            </div>

            {/* Self-Test Log */}
            <div>
              <h4 className="text-[13px] font-semibold mb-2">{t('disk.smart.selfTest.logTitle')}</h4>
              {smartData.self_tests && smartData.self_tests.length > 0 ? (
                <div className="bg-card rounded-2xl card-shadow overflow-hidden overflow-x-auto">
                  <Table>
                    <TableHeader>
                      <TableRow className="border-border/50">
                        <TableHead className="text-[11px]">{t('disk.smart.selfTest.colType')}</TableHead>
                        <TableHead className="text-[11px]">{t('disk.smart.selfTest.colStatus')}</TableHead>
                        <TableHead className="text-[11px]">{t('disk.smart.selfTest.colResult')}</TableHead>
                        <TableHead className="text-[11px]">{t('disk.smart.selfTest.colWhen')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {smartData.self_tests.map((test, idx) => (
                        <TableRow key={`${test.type}-${test.lifetime_hours}-${idx}`}>
                          <TableCell className="text-xs">{test.type}</TableCell>
                          <TableCell className="text-xs text-muted-foreground">{test.status}</TableCell>
                          <TableCell>
                            {test.passed ? (
                              <span className="inline-flex items-center gap-1 text-[12px] font-medium text-success">
                                <CheckCircle2 className="h-3.5 w-3.5" />
                                {t('disk.smart.selfTest.passed')}
                              </span>
                            ) : (
                              <span className="inline-flex items-center gap-1 text-[12px] font-medium text-destructive">
                                <XCircle className="h-3.5 w-3.5" />
                                {t('disk.smart.selfTest.failed')}
                              </span>
                            )}
                          </TableCell>
                          <TableCell className="font-mono text-xs">@ {test.lifetime_hours.toLocaleString()} h</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              ) : (
                <p className="text-[12px] text-muted-foreground">{t('disk.smart.selfTest.empty')}</p>
              )}
            </div>

            {/* SMART Attributes Table */}
            {smartData.attributes && smartData.attributes.length > 0 && (
              <div>
                <h4 className="text-[13px] font-semibold mb-2">{t('disk.smart.attributes')}</h4>
                <div className="bg-card rounded-2xl card-shadow overflow-hidden overflow-x-auto">
                  <Table>
                    <TableHeader>
                      <TableRow className="border-border/50">
                        <TableHead className="text-[11px]">ID</TableHead>
                        <TableHead className="text-[11px]">{t('disk.smart.attrName')}</TableHead>
                        <TableHead className="text-[11px]">{t('disk.smart.attrValue')}</TableHead>
                        <TableHead className="text-[11px]">{t('disk.smart.attrWorst')}</TableHead>
                        <TableHead className="text-[11px]">{t('disk.smart.attrThreshold')}</TableHead>
                        <TableHead className="text-[11px]">{t('disk.smart.attrRaw')}</TableHead>
                        <TableHead className="text-[11px]">{t('disk.smart.attrStatus')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {smartData.attributes.map((attr) => {
                        const statusInfo = smartStatusStyle(attr.status, attr.value, attr.worst, attr.threshold)
                        return (
                          <TableRow key={attr.id}>
                            <TableCell className="font-mono text-xs">{attr.id}</TableCell>
                            <TableCell className="text-xs">{attr.name}</TableCell>
                            <TableCell className="font-mono text-xs">{attr.value}</TableCell>
                            <TableCell className="font-mono text-xs">{attr.worst}</TableCell>
                            <TableCell className="font-mono text-xs">{attr.threshold}</TableCell>
                            <TableCell className="font-mono text-xs">{attr.raw_value}</TableCell>
                            <TableCell>
                              <span className={statusInfo.className}>
                                {statusInfo.label}
                              </span>
                            </TableCell>
                          </TableRow>
                        )
                      })}
                    </TableBody>
                  </Table>
                </div>
              </div>
            )}
          </div>
        ) : (
          <div className="flex items-center justify-center py-8 text-muted-foreground">
            {t('disk.smart.noData')}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
