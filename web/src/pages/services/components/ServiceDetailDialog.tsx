import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import type { ServiceDeps } from '@/types/api'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

// Logs / unit-file / dependency viewer for a single systemd service. Opens
// (and fetches) whenever serviceName is set; the parent only tracks which
// service is being inspected.
export function ServiceDetailDialog({
  serviceName,
  onClose,
}: {
  serviceName: string | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [logs, setLogs] = useState('')
  const [logsLoading, setLogsLoading] = useState(false)
  const [serviceDeps, setServiceDeps] = useState<ServiceDeps | null>(null)
  const [unitFile, setUnitFile] = useState('')
  const [dialogView, setDialogView] = useState<'logs' | 'unit'>('logs')
  const logContainerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!serviceName) return
    setLogs('')
    setUnitFile('')
    setServiceDeps(null)
    setDialogView('logs')
    setLogsLoading(true)
    ;(async () => {
      try {
        const [logsData, depsData, unitData] = await Promise.all([
          api.getServiceLogs(serviceName, 200),
          api.getServiceDeps(serviceName),
          api.getServiceUnit(serviceName).catch(() => ({ unit: '' })),
        ])
        setLogs(logsData.logs || '')
        setServiceDeps(depsData)
        setUnitFile(unitData.unit || '')
        setTimeout(() => {
          if (logContainerRef.current) {
            logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
          }
        }, 0)
      } catch {
        setLogs(t('services.logsLoadFailed'))
      } finally {
        setLogsLoading(false)
      }
    })()
    // Fetch is keyed on the target service only; t is stable per language.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serviceName])

  return (
    <Dialog open={!!serviceName} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-3xl max-h-[80vh]">
        <DialogHeader>
          <DialogTitle>{t('services.logsFor', { name: serviceName })}</DialogTitle>
        </DialogHeader>
        {/* Logs / Unit file toggle */}
        <div className="flex gap-1 rounded-xl bg-muted p-1 w-fit">
          <button
            onClick={() => setDialogView('logs')}
            className={`px-3 py-1 text-[12px] rounded-lg transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${dialogView === 'logs' ? 'bg-card font-semibold shadow-sm' : 'text-muted-foreground'}`}
          >
            {t('services.logsTab')}
          </button>
          <button
            onClick={() => setDialogView('unit')}
            disabled={!unitFile}
            className={`px-3 py-1 text-[12px] rounded-lg transition-colors disabled:opacity-40 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${dialogView === 'unit' ? 'bg-card font-semibold shadow-sm' : 'text-muted-foreground'}`}
          >
            {t('services.unitTab')}
          </button>
        </div>
        {/* Dependency info */}
        {serviceDeps && (serviceDeps.required_by?.length || serviceDeps.requires?.length || serviceDeps.wanted_by?.length) ? (
          <div className="space-y-2">
            {serviceDeps.required_by && serviceDeps.required_by.length > 0 && (
              <div className="p-3 bg-amber-500/10 rounded-xl">
                <p className="text-[11px] font-medium text-amber-500">{t('services.dependents')}</p>
                <p className="text-[13px] mt-1">{serviceDeps.required_by.join(', ')}</p>
              </div>
            )}
            {serviceDeps.requires && serviceDeps.requires.length > 0 && (
              <div className="p-3 bg-primary/10 rounded-xl">
                <p className="text-[11px] font-medium text-primary">{t('services.requires')}</p>
                <p className="text-[13px] mt-1">{serviceDeps.requires.join(', ')}</p>
              </div>
            )}
            {serviceDeps.wanted_by && serviceDeps.wanted_by.length > 0 && (
              <div className="p-3 bg-muted rounded-xl">
                <p className="text-[11px] font-medium text-muted-foreground">{t('services.wantedBy')}</p>
                <p className="text-[13px] mt-1">{serviceDeps.wanted_by.join(', ')}</p>
              </div>
            )}
          </div>
        ) : null}

        <div ref={logContainerRef} className="bg-zinc-950 rounded-xl p-4 overflow-auto max-h-[60vh]">
          {logsLoading ? (
            <div className="flex items-center justify-center py-8">
              <Loader2 className="h-5 w-5 animate-spin text-gray-400" />
            </div>
          ) : (
            <pre className="text-[12px] leading-5 text-gray-300 font-mono whitespace-pre-wrap break-all">
              {dialogView === 'unit' ? unitFile || t('services.noUnit') : logs || t('services.noLogs')}
            </pre>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
