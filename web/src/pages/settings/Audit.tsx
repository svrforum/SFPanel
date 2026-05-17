import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import type { AuditLogEntry } from '@/types/api'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Trash2, ChevronLeft, ChevronRight } from 'lucide-react'

const AUDIT_LIMIT = 20

export default function Audit() {
  const { t } = useTranslation()
  const [auditLogs, setAuditLogs] = useState<AuditLogEntry[]>([])
  const [auditTotal, setAuditTotal] = useState(0)
  const [auditPage, setAuditPage] = useState(1)

  const loadAuditLogs = useCallback(async (page: number) => {
    try {
      const data = await api.getAuditLogs(page, AUDIT_LIMIT)
      setAuditLogs(data.logs)
      setAuditTotal(data.total)
    } catch { /* ignore */ }
  }, [])

  // Initial fetch — loadAuditLogs is memoized via useCallback, so this
  // runs once on mount. setState happens inside loadAuditLogs (after the
  // await), which is exactly what set-state-in-effect is supposed to
  // allow, but the lint can't see through the async indirection.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    loadAuditLogs(1)
  }, [loadAuditLogs])

  return (
    <div className="space-y-6 mt-6">
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-[15px] font-semibold">{t('settings.auditLog')}</h3>
            <p className="text-[13px] text-muted-foreground mt-1">{t('settings.auditLogDescription')}</p>
          </div>
          {auditLogs.length > 0 && (
            <Button
              variant="outline"
              size="sm"
              className="rounded-xl text-[#f04452] hover:text-[#f04452]"
              onClick={async () => {
                if (!window.confirm(t('settings.auditClearConfirm'))) return
                try {
                  await api.clearAuditLogs()
                  setAuditLogs([])
                  setAuditTotal(0)
                  setAuditPage(1)
                  toast.success(t('settings.auditCleared'))
                } catch { /* ignore */ }
              }}
            >
              <Trash2 className="h-3.5 w-3.5 mr-1.5" />
              {t('settings.auditClear')}
            </Button>
          )}
        </div>
        {auditLogs.length === 0 ? (
          <p className="text-[13px] text-muted-foreground py-4">{t('settings.auditLogEmpty')}</p>
        ) : (
          <>
            <div className="bg-card rounded-2xl card-shadow overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="text-[11px]">{t('settings.auditTime')}</TableHead>
                    <TableHead className="text-[11px]">{t('settings.auditUser')}</TableHead>
                    <TableHead className="text-[11px]">{t('settings.auditMethod')}</TableHead>
                    <TableHead className="text-[11px]">{t('settings.auditPath')}</TableHead>
                    <TableHead className="text-[11px]">{t('settings.auditStatus')}</TableHead>
                    <TableHead className="text-[11px]">{t('settings.auditIP')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {auditLogs.map(log => (
                    <TableRow key={log.id}>
                      <TableCell className="text-[12px] text-muted-foreground whitespace-nowrap">
                        {new Date(log.created_at).toLocaleString()}
                      </TableCell>
                      <TableCell className="text-[12px]">{log.username || '-'}</TableCell>
                      <TableCell>
                        <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${
                          log.method === 'DELETE' ? 'bg-[#f04452]/10 text-[#f04452]' :
                          log.method === 'POST' ? 'bg-[#3182f6]/10 text-[#3182f6]' :
                          'bg-[#f59e0b]/10 text-[#f59e0b]'
                        }`}>
                          {log.method}
                        </span>
                      </TableCell>
                      <TableCell className="text-[12px] font-mono max-w-[300px] truncate">{log.path.replace('/api/v1', '')}</TableCell>
                      <TableCell>
                        <span className={`text-[12px] ${log.status < 400 ? 'text-[#00c471]' : 'text-[#f04452]'}`}>
                          {log.status}
                        </span>
                      </TableCell>
                      <TableCell className="text-[12px] text-muted-foreground">{log.ip}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            {auditTotal > AUDIT_LIMIT && (
              <div className="flex items-center justify-between mt-3">
                <span className="text-[12px] text-muted-foreground">
                  {t('settings.auditPage', { page: auditPage, total: Math.ceil(auditTotal / AUDIT_LIMIT) })}
                </span>
                <div className="flex gap-1.5">
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2"
                    disabled={auditPage <= 1}
                    onClick={() => { const p = auditPage - 1; setAuditPage(p); loadAuditLogs(p) }}
                  >
                    <ChevronLeft className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2"
                    disabled={auditPage >= Math.ceil(auditTotal / AUDIT_LIMIT)}
                    onClick={() => { const p = auditPage + 1; setAuditPage(p); loadAuditLogs(p) }}
                  >
                    <ChevronRight className="h-3.5 w-3.5" />
                  </Button>
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}
