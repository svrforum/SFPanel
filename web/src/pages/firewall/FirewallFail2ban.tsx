import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { ShieldAlert, Download, Power, Unlock, Loader2, RefreshCw, ChevronDown, ChevronUp } from 'lucide-react'
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
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface Fail2banStatus {
  installed: boolean
  running: boolean
  version: string
}

interface Fail2banJail {
  name: string
  enabled: boolean
  banned_count: number
  total_banned: number
  banned_ips: string[]
}

interface JailDetail {
  name: string
  enabled: boolean
  banned_count: number
  total_banned: number
  banned_ips: string[]
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function FirewallFail2ban() {
  const { t } = useTranslation()

  // Status
  const [status, setStatus] = useState<Fail2banStatus | null>(null)
  const [statusLoading, setStatusLoading] = useState(true)
  const [installLoading, setInstallLoading] = useState(false)

  // Jails
  const [jails, setJails] = useState<Fail2banJail[]>([])
  const [jailsLoading, setJailsLoading] = useState(false)

  // Selected jail detail
  const [selectedJail, setSelectedJail] = useState<string | null>(null)
  const [jailDetail, setJailDetail] = useState<JailDetail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)

  // Toggle loading per jail
  const [togglingJail, setTogglingJail] = useState<string | null>(null)

  // Unban dialog
  const [unbanDialog, setUnbanDialog] = useState<{ open: boolean; jail: string; ip: string }>({
    open: false,
    jail: '',
    ip: '',
  })
  const [unbanLoading, setUnbanLoading] = useState(false)

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
      const data = await api.getFail2banJailDetail(name) as JailDetail
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

  const handleUnban = useCallback(async () => {
    try {
      setUnbanLoading(true)
      await api.unbanFail2banIP(unbanDialog.jail, unbanDialog.ip)
      toast.success(`${unbanDialog.ip} unbanned`)
      setUnbanDialog({ open: false, jail: '', ip: '' })
      // Refresh jail detail
      if (selectedJail) {
        await fetchJailDetail(selectedJail)
      }
      await fetchJails()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to unban IP'
      toast.error(message)
    } finally {
      setUnbanLoading(false)
    }
  }, [unbanDialog, selectedJail, fetchJailDetail, fetchJails])

  const handleRefresh = useCallback(async () => {
    await fetchStatus()
    if (status?.installed && status?.running) {
      await fetchJails()
      if (selectedJail) {
        await fetchJailDetail(selectedJail)
      }
    }
  }, [fetchStatus, fetchJails, fetchJailDetail, status, selectedJail])

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
            <div className="p-3 rounded-2xl bg-[#f04452]/10">
              <ShieldAlert className="h-8 w-8 text-[#f04452]" />
            </div>
            <div className="text-center">
              <h3 className="text-[15px] font-semibold">{t('firewall.fail2ban.notInstalled')}</h3>
            </div>
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
    )
  }

  // ---------------------------------------------------------------------------
  // Render: Installed
  // ---------------------------------------------------------------------------

  return (
    <div className="space-y-4 mt-4">
      {/* Status Card */}
      <div className="bg-card rounded-2xl p-5 card-shadow">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-xl bg-primary/10">
              <ShieldAlert className="h-5 w-5 text-primary" />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <span className="text-[15px] font-semibold">{t('firewall.fail2ban.title')}</span>
                {status.running ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                    {t('firewall.fail2ban.running')}
                  </span>
                ) : (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#f04452]/10 text-[#f04452]">
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

      {/* Jail List (only when running) */}
      {status.running && (
        <>
          {/* Header */}
          <div className="flex items-center justify-between">
            <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
              {t('firewall.fail2ban.jailCount', { count: jails.length })}
            </span>
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
                      className={`cursor-pointer transition-colors ${
                        selectedJail === jail.name ? 'bg-primary/5' : 'hover:bg-muted/50'
                      }`}
                    >
                      <TableCell
                        className="text-muted-foreground"
                        onClick={() => handleSelectJail(jail.name)}
                      >
                        {selectedJail === jail.name ? (
                          <ChevronUp className="h-4 w-4" />
                        ) : (
                          <ChevronDown className="h-4 w-4" />
                        )}
                      </TableCell>
                      <TableCell
                        className="text-[13px] font-medium font-mono"
                        onClick={() => handleSelectJail(jail.name)}
                      >
                        {jail.name}
                      </TableCell>
                      <TableCell onClick={() => handleSelectJail(jail.name)}>
                        {jail.enabled ? (
                          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                            {t('firewall.fail2ban.enabled')}
                          </span>
                        ) : (
                          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#f04452]/10 text-[#f04452]">
                            {t('firewall.fail2ban.disabled')}
                          </span>
                        )}
                      </TableCell>
                      <TableCell
                        className="text-[13px] font-mono"
                        onClick={() => handleSelectJail(jail.name)}
                      >
                        {jail.banned_count}
                      </TableCell>
                      <TableCell
                        className="text-[13px] font-mono text-muted-foreground"
                        onClick={() => handleSelectJail(jail.name)}
                      >
                        {jail.total_banned}
                      </TableCell>
                      <TableCell className="text-right">
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
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>

              {/* Expanded Jail Detail: Banned IPs */}
              {selectedJail && (
                <div className="border-t border-border/50 bg-muted/20 px-5 py-4">
                  <h4 className="text-[13px] font-semibold mb-3 flex items-center gap-2">
                    <ShieldAlert className="h-3.5 w-3.5" />
                    {t('firewall.fail2ban.bannedIPs')} — <span className="font-mono">{selectedJail}</span>
                  </h4>

                  {detailLoading ? (
                    <div className="flex items-center gap-2 py-4 text-muted-foreground text-[13px]">
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      {t('common.loading')}
                    </div>
                  ) : !jailDetail || jailDetail.banned_ips.length === 0 ? (
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
                            onClick={() => setUnbanDialog({ open: true, jail: selectedJail, ip })}
                            className="rounded-xl text-[12px] text-[#f04452] hover:text-[#f04452] hover:bg-[#f04452]/10"
                          >
                            <Unlock className="h-3 w-3" />
                            {t('firewall.fail2ban.unban')}
                          </Button>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
        </>
      )}

      {/* Unban Confirmation Dialog */}
      <Dialog open={unbanDialog.open} onOpenChange={(open) => { if (!unbanLoading) setUnbanDialog((prev) => ({ ...prev, open })) }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Unlock className="h-4 w-4" />
              {t('firewall.fail2ban.unban')}
            </DialogTitle>
            <DialogDescription>
              {t('firewall.fail2ban.unbanConfirm', { ip: unbanDialog.ip })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              onClick={() => setUnbanDialog({ open: false, jail: '', ip: '' })}
              disabled={unbanLoading}
              className="rounded-xl"
            >
              {t('common.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={handleUnban}
              disabled={unbanLoading}
              className="rounded-xl"
            >
              {unbanLoading ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {t('firewall.fail2ban.unban')}
                </>
              ) : (
                <>
                  <Unlock className="h-4 w-4" />
                  {t('firewall.fail2ban.unban')}
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
