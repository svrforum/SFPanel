import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import type { AuditLogEntry } from '@/types/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { ListLoadState } from '@/components/ListLoadState'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import PaginationRow from '@/components/PaginationRow'
import { Trash2 } from 'lucide-react'

const AUDIT_LIMIT = 20
// Deliberately a native <select>, not ui/select (Radix): the compact h-8
// filter bar wants plain inline dropdowns without popover chrome.
// text-base under md: under 16px, iOS zooms the page into a focused field.
const selectCls = 'h-8 rounded-lg border border-border bg-card px-2 text-base md:text-[12px] outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0'

export default function Audit() {
  const { t, i18n } = useTranslation()
  const confirm = useConfirm()
  const [auditLogs, setAuditLogs] = useState<AuditLogEntry[]>([])
  const [auditTotal, setAuditTotal] = useState(0)
  const [auditPage, setAuditPage] = useState(1)
  // Filters: method/status apply immediately; user applies on submit.
  const [userInput, setUserInput] = useState('')
  const [appliedUser, setAppliedUser] = useState('')
  const [filterMethod, setFilterMethod] = useState('')
  const [filterStatus, setFilterStatus] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  // An empty page under a filter is "nothing matches", not "nothing logged":
  // it said the latter, with no way to clear the filter, and hid Clear Logs.
  const filtered = !!(appliedUser || filterMethod || filterStatus)

  const loadAuditLogs = useCallback(async (page: number) => {
    try {
      const data = await api.getAuditLogs(page, AUDIT_LIMIT, {
        user: appliedUser || undefined,
        method: filterMethod || undefined,
        status: filterStatus || undefined,
      })
      setAuditLogs(data.logs)
      setAuditTotal(data.total)
      setLoaded(true)
      setLoadError(null)
    } catch (err: unknown) {
      // Surface load failures — especially ?node= remote lookups — instead of
      // letting them masquerade as an empty log.
      setLoadError(err instanceof Error ? err.message : String(err))
    }
  }, [appliedUser, filterMethod, filterStatus])

  function clearFilters() {
    setUserInput('')
    setAppliedUser('')
    setFilterMethod('')
    setFilterStatus('')
  }

  // Reload on mount and whenever an applied filter changes (resets to page 1).
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setAuditPage(1)
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
          {(auditLogs.length > 0 || filtered) && (
            <Button
              variant="outline"
              size="sm"
              className="rounded-xl text-destructive hover:text-destructive"
              onClick={async () => {
                if (!(await confirm({ title: t('settings.auditClearConfirm'), danger: true }))) return
                try {
                  await api.clearAuditLogs()
                  setAuditLogs([])
                  setAuditTotal(0)
                  setAuditPage(1)
                  toast.success(t('settings.auditCleared'))
                } catch (err: unknown) {
                  toast.error(err instanceof Error ? err.message : t('settings.auditClearFailed'))
                }
              }}
            >
              <Trash2 className="h-3.5 w-3.5 mr-1.5" />
              {t('settings.auditClear')}
            </Button>
          )}
        </div>
        {/* Filter bar */}
        <div className="flex flex-wrap items-center gap-2 mb-4">
          <form
            onSubmit={(e) => { e.preventDefault(); setAppliedUser(userInput.trim()) }}
            className="flex-1 min-w-[160px]"
          >
            <Input
              value={userInput}
              onChange={(e) => setUserInput(e.target.value)}
              placeholder={t('settings.auditUser')}
              aria-label={t('settings.auditUser')}
              className="h-8 rounded-lg text-base md:text-[12px]"
            />
          </form>
          {/* Named: the first option reads as a label, but only while it is
              the one chosen. */}
          <select aria-label={t('settings.auditMethod')} className={selectCls} value={filterMethod} onChange={(e) => setFilterMethod(e.target.value)}>
            <option value="">{t('settings.auditMethod')}</option>
            <option value="POST">POST</option>
            <option value="PUT">PUT</option>
            <option value="PATCH">PATCH</option>
            <option value="DELETE">DELETE</option>
          </select>
          <select aria-label={t('settings.auditStatus')} className={selectCls} value={filterStatus} onChange={(e) => setFilterStatus(e.target.value)}>
            <option value="">{t('settings.auditStatus')}</option>
            <option value="2xx">2xx</option>
            <option value="4xx">4xx</option>
            <option value="5xx">5xx</option>
            <option value="err">≥ 400</option>
          </select>
        </div>
        {loadError && (
          <ListLoadState loading={false} error={loadError} errorTitle={t('settings.auditLoadFailed')} onRetry={() => loadAuditLogs(auditPage)} />
        )}
        {!loaded ? (
          !loadError && <ListLoadState loading error={null} errorTitle={t('settings.auditLoadFailed')} onRetry={() => loadAuditLogs(auditPage)} skeletonCount={3} />
        ) : auditLogs.length === 0 ? (
          filtered ? (
            <div className="flex flex-wrap items-center gap-3 py-4">
              <p className="text-[13px] text-muted-foreground">{t('settings.auditNoMatch')}</p>
              <Button variant="outline" size="sm" className="rounded-xl" onClick={clearFilters}>{t('settings.auditClearFilters')}</Button>
            </div>
          ) : (
            <p className="text-[13px] text-muted-foreground py-4">{t('settings.auditLogEmpty')}</p>
          )
        ) : (
          <>
            {/* Desktop table */}
            <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="text-[12px]">{t('settings.auditTime')}</TableHead>
                    <TableHead className="text-[12px]">{t('settings.auditUser')}</TableHead>
                    <TableHead className="text-[12px]">{t('settings.auditMethod')}</TableHead>
                    <TableHead className="text-[12px]">{t('settings.auditPath')}</TableHead>
                    <TableHead className="text-[12px]">{t('settings.auditStatus')}</TableHead>
                    <TableHead className="text-[12px]">{t('settings.auditIP')}</TableHead>
                    <TableHead className="text-[12px]">{t('settings.alerts.history.colNode')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {auditLogs.map(log => (
                    <TableRow key={log.id}>
                      <TableCell className="text-[12px] text-muted-foreground whitespace-nowrap">
                        {new Date(log.created_at).toLocaleString(i18n.language)}
                      </TableCell>
                      <TableCell className="text-[12px]">{log.username || '-'}</TableCell>
                      <TableCell>
                        <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[12px] font-medium ${
                          log.method === 'DELETE' ? 'bg-destructive/10 text-destructive' :
                          log.method === 'POST' ? 'bg-primary/10 text-primary' :
                          'bg-warning/10 text-warning'
                        }`}>
                          {log.method}
                        </span>
                      </TableCell>
                      <TableCell className="text-[12px] font-mono max-w-[300px] truncate">{log.path.replace('/api/v1', '')}</TableCell>
                      <TableCell>
                        <span className={`text-[12px] ${log.status < 400 ? 'text-success' : 'text-destructive'}`}>
                          {log.status}
                        </span>
                      </TableCell>
                      <TableCell className="text-[12px] text-muted-foreground">{log.ip}</TableCell>
                      <TableCell className="text-[12px] text-muted-foreground">{log.node_id || '-'}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            {/* Mobile cards */}
            <div className="md:hidden space-y-2">
              {auditLogs.map(log => (
                <div key={log.id} className="bg-card rounded-xl p-3 card-shadow space-y-1">
                  <div className="flex justify-between text-[12px]">
                    <span className="text-muted-foreground">{t('settings.auditTime')}</span>
                    <span className="text-muted-foreground">{new Date(log.created_at).toLocaleString(i18n.language)}</span>
                  </div>
                  <div className="flex justify-between text-[12px]">
                    <span className="text-muted-foreground">{t('settings.auditUser')}</span>
                    <span>{log.username || '-'}</span>
                  </div>
                  <div className="flex justify-between text-[12px]">
                    <span className="text-muted-foreground">{t('settings.auditMethod')}</span>
                    <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[12px] font-medium ${
                      log.method === 'DELETE' ? 'bg-destructive/10 text-destructive' :
                      log.method === 'POST' ? 'bg-primary/10 text-primary' :
                      'bg-warning/10 text-warning'
                    }`}>
                      {log.method}
                    </span>
                  </div>
                  <div className="flex justify-between gap-2 text-[12px]">
                    <span className="text-muted-foreground shrink-0">{t('settings.auditPath')}</span>
                    <span className="font-mono text-right break-all">{log.path.replace('/api/v1', '')}</span>
                  </div>
                  <div className="flex justify-between text-[12px]">
                    <span className="text-muted-foreground">{t('settings.auditStatus')}</span>
                    <span className={log.status < 400 ? 'text-success' : 'text-destructive'}>{log.status}</span>
                  </div>
                  <div className="flex justify-between text-[12px]">
                    <span className="text-muted-foreground">{t('settings.auditIP')}</span>
                    <span className="text-muted-foreground">{log.ip}</span>
                  </div>
                  <div className="flex justify-between text-[12px]">
                    <span className="text-muted-foreground">{t('settings.alerts.history.colNode')}</span>
                    <span className="text-muted-foreground">{log.node_id || '-'}</span>
                  </div>
                </div>
              ))}
            </div>
            <PaginationRow
              page={auditPage}
              total={auditTotal}
              limit={AUDIT_LIMIT}
              label={t('settings.auditPage', { page: auditPage, total: Math.ceil(auditTotal / AUDIT_LIMIT) })}
              onPage={(p) => { setAuditPage(p); loadAuditLogs(p) }}
            />
          </>
        )}
      </div>
    </div>
  )
}
