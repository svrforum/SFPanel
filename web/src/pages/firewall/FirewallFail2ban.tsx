import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { ShieldAlert, Download, Power, Unlock, Loader2, RefreshCw, ChevronDown, ChevronUp, Info, Settings, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Fail2banAboutDialog } from './components/Fail2banAboutDialog'
import { AddJailDialog } from './components/AddJailDialog'
import { EditJailConfigDialog } from './components/EditJailConfigDialog'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface Fail2banStatus {
  installed: boolean
  running: boolean
  version: string
}

// Shared by the jail list and the detail endpoint (identical shapes).
interface Fail2banJail {
  name: string
  enabled: boolean
  filter: string
  banned_count: number
  total_banned: number
  banned_ips: string[]
  max_retry: number
  ban_time: string
  find_time: string
  ignoreip: string
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function FirewallFail2ban() {
  const { t } = useTranslation()
  const confirm = useConfirm()

  // Status
  const [status, setStatus] = useState<Fail2banStatus | null>(null)
  const [statusLoading, setStatusLoading] = useState(true)
  const [installLoading, setInstallLoading] = useState(false)

  // Jails
  const [jails, setJails] = useState<Fail2banJail[]>([])
  const [jailsLoading, setJailsLoading] = useState(false)

  // Selected jail detail
  const [selectedJail, setSelectedJail] = useState<string | null>(null)
  const [jailDetail, setJailDetail] = useState<Fail2banJail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)

  // Toggle loading per jail
  const [togglingJail, setTogglingJail] = useState<string | null>(null)

  // About dialog
  const [aboutOpen, setAboutOpen] = useState(false)

  // Edit config dialog target (form state lives in EditJailConfigDialog)
  const [editTarget, setEditTarget] = useState<Fail2banJail | null>(null)

  // Add jail dialog (form state lives in AddJailDialog)
  const [addJailOpen, setAddJailOpen] = useState(false)

  // ---------------------------------------------------------------------------
  // Data fetching
  // ---------------------------------------------------------------------------

  const fetchStatus = useCallback(async () => {
    try {
      setStatusLoading(true)
      const data = await api.getFail2banStatus()
      setStatus(data as Fail2banStatus)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to get Fail2ban status'
      toast.error(message)
    } finally {
      setStatusLoading(false)
    }
  }, [])

  const fetchJails = useCallback(async () => {
    try {
      setJailsLoading(true)
      const data = await api.getFail2banJails() as { jails: Fail2banJail[]; total: number }
      setJails(data.jails || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to get jails'
      toast.error(message)
    } finally {
      setJailsLoading(false)
    }
  }, [])

  const fetchJailDetail = useCallback(async (name: string) => {
    try {
      setDetailLoading(true)
      const data = await api.getFail2banJailDetail(name) as Fail2banJail
      setJailDetail(data)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to get jail detail'
      toast.error(message)
    } finally {
      setDetailLoading(false)
    }
  }, [])

  // ---------------------------------------------------------------------------
  // Actions
  // ---------------------------------------------------------------------------

  const handleInstall = useCallback(async () => {
    try {
      setInstallLoading(true)
      await api.installFail2ban()
      toast.success('Fail2ban installed successfully')
      await fetchStatus()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to install Fail2ban'
      toast.error(message)
    } finally {
      setInstallLoading(false)
    }
  }, [fetchStatus])

  const handleToggleJail = useCallback(async (name: string, currentlyEnabled: boolean) => {
    try {
      setTogglingJail(name)
      if (currentlyEnabled) {
        await api.disableFail2banJail(name)
      } else {
        await api.enableFail2banJail(name)
      }
      await fetchJails()
      // Refresh detail if the toggled jail is currently selected
      if (selectedJail === name) {
        await fetchJailDetail(name)
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to toggle jail'
      toast.error(message)
    } finally {
      setTogglingJail(null)
    }
  }, [fetchJails, fetchJailDetail, selectedJail])

  const handleSelectJail = useCallback((name: string) => {
    if (selectedJail === name) {
      setSelectedJail(null)
      setJailDetail(null)
    } else {
      setSelectedJail(name)
      fetchJailDetail(name)
    }
  }, [selectedJail, fetchJailDetail])

  const handleUnban = useCallback(async (jail: string, ip: string) => {
    if (!(await confirm({
      title: t('firewall.fail2ban.unban'),
      description: t('firewall.fail2ban.unbanConfirm', { ip }),
      confirmLabel: t('firewall.fail2ban.unban'),
      danger: true,
    }))) return
    try {
      await api.unbanFail2banIP(jail, ip)
      toast.success(`${ip} unbanned`)
      // Refresh jail detail
      if (selectedJail) {
        await fetchJailDetail(selectedJail)
      }
      await fetchJails()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to unban IP'
      toast.error(message)
    }
  }, [confirm, t, selectedJail, fetchJailDetail, fetchJails])

  const handleRefresh = useCallback(async () => {
    await fetchStatus()
    if (status?.installed && status?.running) {
      await fetchJails()
      if (selectedJail) {
        await fetchJailDetail(selectedJail)
      }
    }
  }, [fetchStatus, fetchJails, fetchJailDetail, status, selectedJail])

  const formatBanTime = (val: string): string => {
    const num = parseInt(val, 10)
    if (isNaN(num)) return val
    if (num === -1) return t('firewall.fail2ban.permanent')
    if (num < 60) return `${num}${t('firewall.fail2ban.seconds')}`
    if (num < 3600) return `${Math.floor(num / 60)}${t('firewall.fail2ban.minutes')}`
    if (num < 86400) return `${Math.floor(num / 3600)}${t('firewall.fail2ban.hours')}`
    return `${Math.floor(num / 86400)}${t('firewall.fail2ban.days')}`
  }

  const handleDeleteJail = useCallback(async (name: string) => {
    if (!(await confirm({
      title: t('firewall.fail2ban.deleteJail'),
      description: t('firewall.fail2ban.deleteJailConfirm', { name }),
      confirmLabel: t('common.delete'),
      danger: true,
    }))) return
    try {
      await api.deleteFail2banJail(name)
      toast.success(t('firewall.fail2ban.jailDeleted'))
      if (selectedJail === name) {
        setSelectedJail(null)
        setJailDetail(null)
      }
      await fetchJails()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('firewall.fail2ban.jailDeleteFailed')
      toast.error(message)
    }
  }, [confirm, t, fetchJails, selectedJail])

  // Refresh jail list + detail after a config save from the edit dialog.
  const handleConfigSaved = useCallback(async (name: string) => {
    await fetchJails()
    if (selectedJail === name) {
      await fetchJailDetail(name)
    }
  }, [fetchJails, fetchJailDetail, selectedJail])

  // ---------------------------------------------------------------------------
  // Effects
  // ---------------------------------------------------------------------------

  useEffect(() => {
    fetchStatus()
  }, [fetchStatus])

  useEffect(() => {
    if (status?.installed && status?.running) {
      fetchJails()
    }
  }, [status, fetchJails])

  // ---------------------------------------------------------------------------
  // Render: Loading
  // ---------------------------------------------------------------------------

  if (statusLoading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin mr-2" />
        {t('common.loading')}
      </div>
    )
  }

  // ---------------------------------------------------------------------------
  // Render: Not installed
  // ---------------------------------------------------------------------------

  if (!status?.installed) {
    return (
      <div className="space-y-4 mt-4">
        <div className="bg-card rounded-2xl p-6 card-shadow">
          <div className="flex flex-col items-center justify-center py-8 gap-4">
            <div className="p-3 rounded-2xl bg-destructive/10">
              <ShieldAlert className="h-8 w-8 text-destructive" />
            </div>
            <div className="text-center space-y-1">
              <h3 className="text-[15px] font-semibold">{t('firewall.fail2ban.notInstalled')}</h3>
              <p className="text-[13px] text-muted-foreground">{t('firewall.fail2ban.notInstalledDesc')}</p>
            </div>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setAboutOpen(true)}
                className="rounded-xl"
              >
                <Info className="h-3.5 w-3.5" />
                {t('firewall.fail2ban.learnMore')}
              </Button>
              <Button
                onClick={handleInstall}
                disabled={installLoading}
                className="rounded-xl"
              >
                {installLoading ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin" />
                    {t('firewall.fail2ban.installing')}
                  </>
                ) : (
                  <>
                    <Download className="h-4 w-4" />
                    {t('firewall.fail2ban.install')}
                  </>
                )}
              </Button>
            </div>
          </div>
        </div>

        <Fail2banAboutDialog open={aboutOpen} onOpenChange={setAboutOpen} />
      </div>
    )
  }

  // ---------------------------------------------------------------------------
  // Render: Installed
  // ---------------------------------------------------------------------------

  return (
    <div className="space-y-4 mt-4">
      {/* Status Card */}
      <div className="bg-card rounded-2xl p-5 card-shadow">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-xl bg-primary/10">
              <ShieldAlert className="h-5 w-5 text-primary" />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <span className="text-[15px] font-semibold">{t('firewall.fail2ban.title')}</span>
                {status.running ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-success/10 text-success">
                    {t('firewall.fail2ban.running')}
                  </span>
                ) : (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-destructive/10 text-destructive">
                    {t('firewall.fail2ban.stopped')}
                  </span>
                )}
              </div>
              {status.version && (
                <span className="text-[11px] text-muted-foreground">
                  {t('firewall.fail2ban.version')}: {status.version}
                </span>
              )}
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={() => setAboutOpen(true)}
              title={t('firewall.fail2ban.learnMore')}
              aria-label={t('firewall.fail2ban.learnMore')}
            >
              <Info className="h-4 w-4" />
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={handleRefresh}
              disabled={statusLoading || jailsLoading}
              className="rounded-xl"
            >
              <RefreshCw className={(statusLoading || jailsLoading) ? 'h-3.5 w-3.5 animate-spin' : 'h-3.5 w-3.5'} />
              {t('common.refresh')}
            </Button>
          </div>
        </div>
      </div>

      {/* Jail List (only when running) */}
      {status.running && (
        <>
          {/* Header */}
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
              {t('firewall.fail2ban.jailCount', { count: jails.length })}
            </span>
            <Button size="sm" onClick={() => setAddJailOpen(true)} className="rounded-xl">
              <Plus className="h-3.5 w-3.5" />
              {t('firewall.fail2ban.addJail')}
            </Button>
          </div>

          {/* Jails Table */}
          {jailsLoading ? (
            <div className="flex items-center justify-center py-12 text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin mr-2" />
              {t('common.loading')}
            </div>
          ) : jails.length === 0 ? (
            <div className="bg-card rounded-2xl card-shadow p-8 text-center text-muted-foreground text-[13px]">
              {t('firewall.fail2ban.noJails')}
            </div>
          ) : (
            <div className="bg-card rounded-2xl card-shadow overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow className="border-border/50">
                    <TableHead className="text-[11px] text-muted-foreground uppercase tracking-wider w-8" />
                    <TableHead className="text-[11px] text-muted-foreground uppercase tracking-wider">
                      {t('firewall.fail2ban.name')}
                    </TableHead>
                    <TableHead className="text-[11px] text-muted-foreground uppercase tracking-wider">
                      {t('firewall.fail2ban.status')}
                    </TableHead>
                    <TableHead className="text-[11px] text-muted-foreground uppercase tracking-wider">
                      {t('firewall.fail2ban.bannedCount')}
                    </TableHead>
                    <TableHead className="text-[11px] text-muted-foreground uppercase tracking-wider">
                      {t('firewall.fail2ban.totalBanned')}
                    </TableHead>
                    <TableHead className="text-[11px] text-muted-foreground uppercase tracking-wider text-right">
                      {t('common.actions')}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {jails.map((jail) => (
                    <TableRow
                      key={jail.name}
                      role="button"
                      tabIndex={0}
                      onClick={() => handleSelectJail(jail.name)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault()
                          handleSelectJail(jail.name)
                        }
                      }}
                      className={`cursor-pointer transition-colors outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40 ${
                        selectedJail === jail.name ? 'bg-primary/5' : 'hover:bg-muted/50'
                      }`}
                    >
                      <TableCell className="text-muted-foreground">
                        {selectedJail === jail.name ? (
                          <ChevronUp className="h-4 w-4" />
                        ) : (
                          <ChevronDown className="h-4 w-4" />
                        )}
                      </TableCell>
                      <TableCell className="text-[13px] font-medium font-mono">
                        {jail.name}
                      </TableCell>
                      <TableCell>
                        {jail.enabled ? (
                          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-success/10 text-success">
                            {t('firewall.fail2ban.enabled')}
                          </span>
                        ) : (
                          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-destructive/10 text-destructive">
                            {t('firewall.fail2ban.disabled')}
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="text-[13px] font-mono">
                        {jail.banned_count}
                      </TableCell>
                      <TableCell className="text-[13px] font-mono text-muted-foreground">
                        {jail.total_banned}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex items-center justify-end gap-1">
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={(e) => {
                              e.stopPropagation()
                              handleToggleJail(jail.name, jail.enabled)
                            }}
                            disabled={togglingJail === jail.name}
                            className="rounded-xl text-[12px]"
                          >
                            {togglingJail === jail.name ? (
                              <Loader2 className="h-3 w-3 animate-spin" />
                            ) : (
                              <Power className="h-3 w-3" />
                            )}
                            {jail.enabled
                              ? t('firewall.fail2ban.disable')
                              : t('firewall.fail2ban.enable')}
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={(e) => {
                              e.stopPropagation()
                              handleDeleteJail(jail.name)
                            }}
                            aria-label={t('firewall.fail2ban.deleteJail')}
                            className="rounded-xl text-[12px] text-destructive hover:text-destructive hover:bg-destructive/10"
                          >
                            <Trash2 className="h-3 w-3" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>

              {/* Expanded Jail Detail: Config + Banned IPs */}
              {selectedJail && (
                <div className="border-t border-border/50 bg-muted/20 px-5 py-4 space-y-4">
                  {/* Jail Configuration */}
                  {detailLoading ? (
                    <div className="flex items-center gap-2 py-4 text-muted-foreground text-[13px]">
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      {t('common.loading')}
                    </div>
                  ) : jailDetail && (
                    <>
                      {/* Config Section */}
                      <div>
                        <div className="flex items-center justify-between mb-3">
                          <h4 className="text-[13px] font-semibold flex items-center gap-2">
                            <Settings className="h-3.5 w-3.5" />
                            {t('firewall.fail2ban.config')} — <span className="font-mono">{selectedJail}</span>
                          </h4>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => setEditTarget(jailDetail)}
                            className="rounded-xl text-[12px]"
                          >
                            <Settings className="h-3 w-3" />
                            {t('common.edit')}
                          </Button>
                        </div>
                        <div className="grid grid-cols-2 md:grid-cols-3 gap-3">
                          <div className="bg-card rounded-xl px-4 py-3 card-shadow">
                            <div className="text-[11px] text-muted-foreground uppercase tracking-wider">
                              {t('firewall.fail2ban.maxRetry')}
                            </div>
                            <div className="text-[15px] font-semibold font-mono mt-1">
                              {jailDetail.max_retry}
                            </div>
                          </div>
                          <div className="bg-card rounded-xl px-4 py-3 card-shadow">
                            <div className="text-[11px] text-muted-foreground uppercase tracking-wider">
                              {t('firewall.fail2ban.banTime')}
                            </div>
                            <div className="text-[15px] font-semibold font-mono mt-1">
                              {formatBanTime(jailDetail.ban_time)}
                            </div>
                            <div className="text-[11px] text-muted-foreground mt-0.5">
                              {jailDetail.ban_time}{t('firewall.fail2ban.seconds')}
                            </div>
                          </div>
                          <div className="bg-card rounded-xl px-4 py-3 card-shadow">
                            <div className="text-[11px] text-muted-foreground uppercase tracking-wider">
                              {t('firewall.fail2ban.findTime')}
                            </div>
                            <div className="text-[15px] font-semibold font-mono mt-1">
                              {formatBanTime(jailDetail.find_time)}
                            </div>
                            <div className="text-[11px] text-muted-foreground mt-0.5">
                              {jailDetail.find_time}{t('firewall.fail2ban.seconds')}
                            </div>
                          </div>
                        </div>
                        {jailDetail.filter && (
                          <div className="mt-2 text-[11px] text-muted-foreground">
                            {t('firewall.fail2ban.logFile')}: <span className="font-mono">{jailDetail.filter}</span>
                          </div>
                        )}
                      </div>

                      {/* Banned IPs Section */}
                      <div>
                        <h4 className="text-[13px] font-semibold mb-3 flex items-center gap-2">
                          <ShieldAlert className="h-3.5 w-3.5" />
                          {t('firewall.fail2ban.bannedIPs')}
                        </h4>
                        {jailDetail.banned_ips.length === 0 ? (
                          <div className="py-4 text-center text-muted-foreground text-[13px]">
                            {t('firewall.fail2ban.noBannedIPs')}
                          </div>
                        ) : (
                          <div className="space-y-1.5">
                            {jailDetail.banned_ips.map((ip) => (
                              <div
                                key={ip}
                                className="flex items-center justify-between bg-card rounded-xl px-4 py-2.5 card-shadow"
                              >
                                <span className="text-[13px] font-mono font-medium">{ip}</span>
                                <Button
                                  variant="outline"
                                  size="sm"
                                  onClick={() => handleUnban(selectedJail, ip)}
                                  className="rounded-xl text-[12px] text-destructive hover:text-destructive hover:bg-destructive/10"
                                >
                                  <Unlock className="h-3 w-3" />
                                  {t('firewall.fail2ban.unban')}
                                </Button>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>
                    </>
                  )}
                </div>
              )}
            </div>
          )}
        </>
      )}

      {/* About Dialog */}
      <Fail2banAboutDialog open={aboutOpen} onOpenChange={setAboutOpen} />

      {/* Edit Jail Config Dialog */}
      <EditJailConfigDialog
        jail={editTarget}
        onClose={() => setEditTarget(null)}
        onSaved={handleConfigSaved}
      />

      {/* Add Jail Dialog */}
      <AddJailDialog
        open={addJailOpen}
        onOpenChange={setAddJailOpen}
        activeJails={jails.map((j) => j.name)}
        onCreated={fetchJails}
      />
    </div>
  )
}
