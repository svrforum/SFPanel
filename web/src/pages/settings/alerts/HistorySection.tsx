import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { useApiAction } from '@/hooks/useApiAction'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import PaginationRow from '@/components/PaginationRow'
import { Trash2 } from 'lucide-react'
import type { AlertHistoryEntry } from '@/types/api'
import { RULE_TYPES, SEVERITY_OPTIONS, getSeverityStyle } from './shared'

const HISTORY_LIMIT = 20

// Parse the sent_channels JSON array.
//
// An empty list is a real state now, and the one worth seeing: the fire
// happened and reached nobody. Rows used to be written only on a successful
// send, so a rule whose channels had all failed — a rotated webhook, a deleted
// channel — left nothing here at all, and an empty history read as "nothing
// has gone wrong".
function parseSentChannels(raw: string): string[] {
  try {
    const v = JSON.parse(raw || '[]')
    return Array.isArray(v) ? v.map(String) : []
  } catch {
    return []
  }
}

export function HistorySection() {
  const { t } = useTranslation()
  const confirm = useConfirm()

  const [history, setHistory] = useState<AlertHistoryEntry[]>([])
  const [historyTotal, setHistoryTotal] = useState(0)
  const [historyPage, setHistoryPage] = useState(1)

  const loadHistory = useCallback((page: number) => {
    api.getAlertHistory(page, HISTORY_LIMIT)
      .then((data) => {
        setHistory(data.items || [])
        setHistoryTotal(data.total || 0)
      })
      .catch(() => { /* ignore */ })
  }, [])

  useEffect(() => {
    loadHistory(1)
  }, [loadHistory])

  const { run: runClearHistory } = useApiAction(
    api.clearAlertHistory.bind(api),
    {
      successMsg: t('settings.alerts.history.successCleared'),
      errorMsg: t('settings.alerts.history.errorClearFailed'),
      onSuccess: () => {
        setHistory([])
        setHistoryTotal(0)
        setHistoryPage(1)
      },
    },
  )

  async function handleClearHistory() {
    if (!(await confirm({ title: t('settings.alerts.history.confirmClear'), danger: true }))) return
    await runClearHistory()
  }

  function severityLabel(severity: string) {
    const entry = SEVERITY_OPTIONS.find(s => s.value === severity)
    return entry ? t(entry.i18nKey, { defaultValue: entry.fallback }) : severity
  }

  return (
    <div className="bg-card rounded-2xl p-6 card-shadow">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h3 className="text-[15px] font-semibold">{t('settings.alerts.history.title')}</h3>
          <p className="text-[13px] text-muted-foreground mt-1">{t('settings.alerts.history.description')}</p>
        </div>
        {history.length > 0 && (
          <Button
            variant="outline"
            size="sm"
            className="rounded-xl text-destructive hover:text-destructive"
            onClick={handleClearHistory}
          >
            <Trash2 className="h-3.5 w-3.5 mr-1.5" />
            {t('settings.alerts.history.clearButton')}
          </Button>
        )}
      </div>

      {history.length === 0 ? (
        <p className="text-[13px] text-muted-foreground py-4">{t('settings.alerts.history.empty')}</p>
      ) : (
        <>
          {/* Desktop table */}
          <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="text-[11px]">{t('settings.alerts.history.colTime')}</TableHead>
                  <TableHead className="text-[11px]">{t('settings.alerts.history.colType')}</TableHead>
                  <TableHead className="text-[11px]">{t('settings.alerts.history.colSeverity')}</TableHead>
                  <TableHead className="text-[11px]">{t('settings.alerts.history.colMessage')}</TableHead>
                  <TableHead className="text-[11px]">{t('settings.alerts.history.colNode')}</TableHead>
                  <TableHead className="text-[11px]">{t('settings.alerts.history.colStatus')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {history.map(entry => {
                  const ruleTypeEntry = RULE_TYPES.find(rt => rt.value === entry.type)
                  return (
                  <TableRow key={entry.id}>
                    <TableCell className="text-[12px] text-muted-foreground whitespace-nowrap">
                      {new Date(entry.created_at).toLocaleString()}
                    </TableCell>
                    <TableCell>
                      <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-secondary text-muted-foreground">
                        {ruleTypeEntry ? t(ruleTypeEntry.i18nKey) : entry.type}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${getSeverityStyle(entry.severity)}`}>
                        {severityLabel(entry.severity)}
                      </span>
                    </TableCell>
                    <TableCell className="text-[12px] max-w-[300px] truncate">{entry.message}</TableCell>
                    <TableCell className="text-[12px] text-muted-foreground">{entry.node_id || '-'}</TableCell>
                    <TableCell>
                      {(() => {
                        const chans = parseSentChannels(entry.sent_channels)
                        return chans.length > 0 ? (
                          <span className="text-[12px] text-success" title={chans.join(', ')}>
                            {t('settings.alerts.history.sent', { count: chans.length })}
                          </span>
                        ) : (
                          <span className="text-[12px] font-medium text-destructive">
                            {t('settings.alerts.history.undelivered')}
                          </span>
                        )
                      })()}
                    </TableCell>
                  </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
          {/* Mobile cards */}
          <div className="md:hidden space-y-2">
            {history.map(entry => {
              const ruleTypeEntry = RULE_TYPES.find(rt => rt.value === entry.type)
              const chans = parseSentChannels(entry.sent_channels)
              return (
                <div key={entry.id} className="bg-card rounded-xl p-3 card-shadow space-y-1">
                  <div className="flex justify-between text-[12px]">
                    <span className="text-muted-foreground">{t('settings.alerts.history.colTime')}</span>
                    <span className="text-muted-foreground">{new Date(entry.created_at).toLocaleString()}</span>
                  </div>
                  <div className="flex justify-between text-[12px]">
                    <span className="text-muted-foreground">{t('settings.alerts.history.colType')}</span>
                    <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-secondary text-muted-foreground">
                      {ruleTypeEntry ? t(ruleTypeEntry.i18nKey) : entry.type}
                    </span>
                  </div>
                  <div className="flex justify-between text-[12px]">
                    <span className="text-muted-foreground">{t('settings.alerts.history.colSeverity')}</span>
                    <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${getSeverityStyle(entry.severity)}`}>
                      {severityLabel(entry.severity)}
                    </span>
                  </div>
                  <div className="flex justify-between gap-2 text-[12px]">
                    <span className="text-muted-foreground shrink-0">{t('settings.alerts.history.colMessage')}</span>
                    <span className="text-right break-words">{entry.message}</span>
                  </div>
                  <div className="flex justify-between text-[12px]">
                    <span className="text-muted-foreground">{t('settings.alerts.history.colNode')}</span>
                    <span className="text-muted-foreground">{entry.node_id || '-'}</span>
                  </div>
                  <div className="flex justify-between text-[12px]">
                    <span className="text-muted-foreground">{t('settings.alerts.history.colStatus')}</span>
                    {chans.length > 0 ? (
                      <span className="text-success" title={chans.join(', ')}>
                        {t('settings.alerts.history.sent', { count: chans.length })}
                      </span>
                    ) : (
                      <span className="font-medium text-destructive">
                        {t('settings.alerts.history.undelivered')}
                      </span>
                    )}
                  </div>
                </div>
              )
            })}
          </div>
          <PaginationRow
            page={historyPage}
            total={historyTotal}
            limit={HISTORY_LIMIT}
            label={t('settings.alerts.history.pageIndicator', { page: historyPage, total: Math.ceil(historyTotal / HISTORY_LIMIT) })}
            onPage={(p) => { setHistoryPage(p); loadHistory(p) }}
          />
        </>
      )}
    </div>
  )
}
