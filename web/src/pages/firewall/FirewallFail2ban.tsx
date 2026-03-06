import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { ShieldAlert, Download, Power, Unlock, Loader2, RefreshCw, ChevronDown, ChevronUp, Info, Settings, AlertTriangle, Plus, Trash2, Check } from 'lucide-react'
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
import { Input } from '@/components/ui/input'
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
  filter: string
  banned_count: number
  total_banned: number
  banned_ips: string[]
  max_retry: number
  ban_time: string
  find_time: string
  ignoreip: string
}

interface JailDetail {
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

interface JailTemplate {
  id: string
  name: string
  description: string
  filter: string
  log_path: string
  max_retry: number
  ban_time: number
  find_time: number
  available: boolean
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

  // About dialog
  const [aboutOpen, setAboutOpen] = useState(false)

  // Edit config dialog
  const [editConfigOpen, setEditConfigOpen] = useState(false)
  const [editConfigJail, setEditConfigJail] = useState<string>('')
  const [editMaxRetry, setEditMaxRetry] = useState('')
  const [editBanTime, setEditBanTime] = useState('')
  const [editFindTime, setEditFindTime] = useState('')
  const [editIgnoreIP, setEditIgnoreIP] = useState('')
  const [editConfigLoading, setEditConfigLoading] = useState(false)

  // Add jail dialog
  const [addJailOpen, setAddJailOpen] = useState(false)
  const [templates, setTemplates] = useState<JailTemplate[]>([])
  const [templatesLoading, setTemplatesLoading] = useState(false)
  const [selectedTemplate, setSelectedTemplate] = useState<JailTemplate | null>(null)
  const [isCustomMode, setIsCustomMode] = useState(false)
  const [customName, setCustomName] = useState('')
  const [customFilter, setCustomFilter] = useState('')
  const [newMaxRetry, setNewMaxRetry] = useState('')
  const [newBanTime, setNewBanTime] = useState('')
  const [newFindTime, setNewFindTime] = useState('')
  const [newLogPath, setNewLogPath] = useState('')
  const [newIgnoreIP, setNewIgnoreIP] = useState('')
  const [addJailLoading, setAddJailLoading] = useState(false)

  // Delete jail dialog
  const [deleteJailDialog, setDeleteJailDialog] = useState<{ open: boolean; name: string }>({ open: false, name: '' })
  const [deleteJailLoading, setDeleteJailLoading] = useState(false)

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

  const handleOpenEditConfig = useCallback((jail: JailDetail | Fail2banJail) => {
    setEditConfigJail(jail.name)
    setEditMaxRetry(String(jail.max_retry))
    setEditBanTime(jail.ban_time)
    setEditFindTime(jail.find_time)
    setEditIgnoreIP(jail.ignoreip || '')
    setEditConfigOpen(true)
  }, [])

  const handleSaveConfig = useCallback(async () => {
    try {
      setEditConfigLoading(true)
      await api.updateFail2banJailConfig(editConfigJail, {
        max_retry: parseInt(editMaxRetry, 10),
        ban_time: editBanTime,
        find_time: editFindTime,
        ignoreip: editIgnoreIP,
      })
      toast.success(t('firewall.fail2ban.configUpdated'))
      setEditConfigOpen(false)
      // Refresh data
      await fetchJails()
      if (selectedJail === editConfigJail) {
        await fetchJailDetail(editConfigJail)
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('firewall.fail2ban.configUpdateFailed')
      toast.error(message)
    } finally {
      setEditConfigLoading(false)
    }
  }, [editConfigJail, editMaxRetry, editBanTime, editFindTime, editIgnoreIP, t, fetchJails, fetchJailDetail, selectedJail])

  const formatBanTime = (val: string): string => {
    const num = parseInt(val, 10)
    if (isNaN(num)) return val
    if (num === -1) return t('firewall.fail2ban.configWarning').includes('permanent') ? 'Permanent' : '영구'
    if (num < 60) return `${num}${t('firewall.fail2ban.seconds')}`
    if (num < 3600) return `${Math.floor(num / 60)}m`
    if (num < 86400) return `${Math.floor(num / 3600)}h`
    return `${Math.floor(num / 86400)}d`
  }

  const handleOpenAddJail = useCallback(async () => {
    setAddJailOpen(true)
    setSelectedTemplate(null)
    setIsCustomMode(false)
    setCustomName('')
    setCustomFilter('')
    try {
      setTemplatesLoading(true)
      const data = await api.getFail2banTemplates()
      setTemplates(data.templates || [])
    } catch {
      toast.error('Failed to load templates')
    } finally {
      setTemplatesLoading(false)
    }
  }, [])

  const handleSelectTemplate = useCallback((tmpl: JailTemplate) => {
    setSelectedTemplate(tmpl)
    setIsCustomMode(false)
    setNewMaxRetry(String(tmpl.max_retry))
    setNewBanTime(String(tmpl.ban_time))
    setNewFindTime(String(tmpl.find_time))
    setNewLogPath(tmpl.log_path)
    setNewIgnoreIP('')
  }, [])

  const handleSelectCustom = useCallback(() => {
    setSelectedTemplate(null)
    setIsCustomMode(true)
    setCustomName('')
    setCustomFilter('')
    setNewMaxRetry('5')
    setNewBanTime('600')
    setNewFindTime('600')
    setNewLogPath('')
    setNewIgnoreIP('')
  }, [])

  const handleCreateJail = useCallback(async () => {
    if (!selectedTemplate && !isCustomMode) return
    try {
      setAddJailLoading(true)
      if (isCustomMode) {
        await api.createFail2banJail({
          id: 'custom',
          name: customName,
          filter: customFilter,
          max_retry: parseInt(newMaxRetry, 10),
          ban_time: parseInt(newBanTime, 10),
          find_time: parseInt(newFindTime, 10),
          log_path: newLogPath,
          ignoreip: newIgnoreIP,
        })
      } else {
        await api.createFail2banJail({
          id: selectedTemplate!.id,
          max_retry: parseInt(newMaxRetry, 10),
          ban_time: parseInt(newBanTime, 10),
          find_time: parseInt(newFindTime, 10),
          log_path: newLogPath,
          ignoreip: newIgnoreIP,
        })
      }
      toast.success(t('firewall.fail2ban.jailCreated'))
      setAddJailOpen(false)
      await fetchJails()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('firewall.fail2ban.jailCreateFailed')
      toast.error(message)
    } finally {
      setAddJailLoading(false)
    }
  }, [selectedTemplate, isCustomMode, customName, customFilter, newMaxRetry, newBanTime, newFindTime, newLogPath, newIgnoreIP, t, fetchJails])

  const handleDeleteJail = useCallback(async () => {
    try {
      setDeleteJailLoading(true)
      await api.deleteFail2banJail(deleteJailDialog.name)
      toast.success(t('firewall.fail2ban.jailDeleted'))
      setDeleteJailDialog({ open: false, name: '' })
      if (selectedJail === deleteJailDialog.name) {
        setSelectedJail(null)
        setJailDetail(null)
      }
      await fetchJails()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('firewall.fail2ban.jailDeleteFailed')
      toast.error(message)
    } finally {
      setDeleteJailLoading(false)
    }
  }, [deleteJailDialog.name, t, fetchJails, selectedJail])

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

        {/* About Dialog */}
        <Dialog open={aboutOpen} onOpenChange={setAboutOpen}>
          <DialogContent className="sm:max-w-lg">
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <ShieldAlert className="h-5 w-5 text-primary" />
                {t('firewall.fail2ban.aboutTitle')}
              </DialogTitle>
              <DialogDescription>
                {t('firewall.fail2ban.aboutDesc')}
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4">
              {/* How it works */}
              <div className="space-y-2">
                <h4 className="text-[13px] font-semibold">{t('firewall.fail2ban.aboutHowTitle')}</h4>
                <ol className="space-y-1.5 text-[13px] text-muted-foreground list-decimal list-inside">
                  <li>{t('firewall.fail2ban.aboutHow1')}</li>
                  <li>{t('firewall.fail2ban.aboutHow2')}</li>
                  <li>{t('firewall.fail2ban.aboutHow3')}</li>
                </ol>
              </div>

              {/* Jail types */}
              <div className="space-y-2">
                <h4 className="text-[13px] font-semibold">{t('firewall.fail2ban.aboutJailTitle')}</h4>
                <ul className="space-y-1.5 text-[13px] text-muted-foreground">
                  <li className="flex items-start gap-2">
                    <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-primary/10 text-primary shrink-0 mt-0.5">sshd</span>
                    <span>{t('firewall.fail2ban.aboutJailSSH')}</span>
                  </li>
                  <li className="flex items-start gap-2">
                    <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-secondary text-muted-foreground shrink-0 mt-0.5">nginx</span>
                    <span>{t('firewall.fail2ban.aboutJailNginx')}</span>
                  </li>
                  <li className="flex items-start gap-2">
                    <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-secondary text-muted-foreground shrink-0 mt-0.5">apache</span>
                    <span>{t('firewall.fail2ban.aboutJailApache')}</span>
                  </li>
                  <li className="flex items-start gap-2">
                    <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-secondary text-muted-foreground shrink-0 mt-0.5">recidive</span>
                    <span>{t('firewall.fail2ban.aboutJailRecidive')}</span>
                  </li>
                </ul>
              </div>

              {/* Recommendation */}
              <div className="bg-primary/5 rounded-xl px-4 py-3">
                <p className="text-[13px] text-primary font-medium">
                  {t('firewall.fail2ban.aboutRecommend')}
                </p>
              </div>
            </div>
            <DialogFooter>
              <Button onClick={() => setAboutOpen(false)} className="rounded-xl">
                {t('common.close')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
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
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={() => setAboutOpen(true)}
              title={t('firewall.fail2ban.learnMore')}
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
          <div className="flex items-center justify-between">
            <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
              {t('firewall.fail2ban.jailCount', { count: jails.length })}
            </span>
            <Button size="sm" onClick={handleOpenAddJail} className="rounded-xl">
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
                              setDeleteJailDialog({ open: true, name: jail.name })
                            }}
                            className="rounded-xl text-[12px] text-[#f04452] hover:text-[#f04452] hover:bg-[#f04452]/10"
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
                            onClick={() => handleOpenEditConfig(jailDetail)}
                            className="rounded-xl text-[12px]"
                          >
                            <Settings className="h-3 w-3" />
                            {t('common.edit')}
                          </Button>
                        </div>
                        <div className="grid grid-cols-3 gap-3">
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
                    </>
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
      {/* About Dialog */}
      <Dialog open={aboutOpen} onOpenChange={setAboutOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <ShieldAlert className="h-5 w-5 text-primary" />
              {t('firewall.fail2ban.aboutTitle')}
            </DialogTitle>
            <DialogDescription>
              {t('firewall.fail2ban.aboutDesc')}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <h4 className="text-[13px] font-semibold">{t('firewall.fail2ban.aboutHowTitle')}</h4>
              <ol className="space-y-1.5 text-[13px] text-muted-foreground list-decimal list-inside">
                <li>{t('firewall.fail2ban.aboutHow1')}</li>
                <li>{t('firewall.fail2ban.aboutHow2')}</li>
                <li>{t('firewall.fail2ban.aboutHow3')}</li>
              </ol>
            </div>
            <div className="space-y-2">
              <h4 className="text-[13px] font-semibold">{t('firewall.fail2ban.aboutJailTitle')}</h4>
              <ul className="space-y-1.5 text-[13px] text-muted-foreground">
                <li className="flex items-start gap-2">
                  <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-primary/10 text-primary shrink-0 mt-0.5">sshd</span>
                  <span>{t('firewall.fail2ban.aboutJailSSH')}</span>
                </li>
                <li className="flex items-start gap-2">
                  <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-secondary text-muted-foreground shrink-0 mt-0.5">nginx</span>
                  <span>{t('firewall.fail2ban.aboutJailNginx')}</span>
                </li>
                <li className="flex items-start gap-2">
                  <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-secondary text-muted-foreground shrink-0 mt-0.5">apache</span>
                  <span>{t('firewall.fail2ban.aboutJailApache')}</span>
                </li>
                <li className="flex items-start gap-2">
                  <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-mono font-medium bg-secondary text-muted-foreground shrink-0 mt-0.5">recidive</span>
                  <span>{t('firewall.fail2ban.aboutJailRecidive')}</span>
                </li>
              </ul>
            </div>
            <div className="bg-primary/5 rounded-xl px-4 py-3">
              <p className="text-[13px] text-primary font-medium">
                {t('firewall.fail2ban.aboutRecommend')}
              </p>
            </div>
          </div>
          <DialogFooter>
            <Button onClick={() => setAboutOpen(false)} className="rounded-xl">
              {t('common.close')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Jail Config Dialog */}
      <Dialog open={editConfigOpen} onOpenChange={(open) => { if (!editConfigLoading) setEditConfigOpen(open) }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Settings className="h-4 w-4" />
              {t('firewall.fail2ban.editConfig')}
            </DialogTitle>
            <DialogDescription>
              {t('firewall.fail2ban.editConfigDesc')}
            </DialogDescription>
          </DialogHeader>

          {/* Warning Banner */}
          <div className="bg-[#f59e0b]/10 border border-[#f59e0b]/30 rounded-xl px-4 py-3 flex items-start gap-3">
            <AlertTriangle className="h-5 w-5 text-[#f59e0b] shrink-0 mt-0.5" />
            <p className="text-[13px] text-[#f59e0b] font-medium leading-relaxed">
              {t('firewall.fail2ban.configWarning')}
            </p>
          </div>

          <div className="space-y-4">
            {/* Jail name display */}
            <div>
              <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
                Jail
              </label>
              <p className="text-[13px] font-mono font-medium mt-1">{editConfigJail}</p>
            </div>

            {/* Max Retry */}
            <div className="space-y-1.5">
              <label className="text-[13px] font-medium">{t('firewall.fail2ban.maxRetry')}</label>
              <Input
                type="number"
                min={1}
                max={100}
                value={editMaxRetry}
                onChange={(e) => setEditMaxRetry(e.target.value)}
                className="rounded-xl text-[13px] font-mono"
              />
              <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.maxRetryHint')}</p>
            </div>

            {/* Ban Time */}
            <div className="space-y-1.5">
              <label className="text-[13px] font-medium">{t('firewall.fail2ban.banTime')}</label>
              <Input
                value={editBanTime}
                onChange={(e) => setEditBanTime(e.target.value)}
                className="rounded-xl text-[13px] font-mono"
              />
              <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.banTimeHint')}</p>
            </div>

            {/* Find Time */}
            <div className="space-y-1.5">
              <label className="text-[13px] font-medium">{t('firewall.fail2ban.findTime')}</label>
              <Input
                value={editFindTime}
                onChange={(e) => setEditFindTime(e.target.value)}
                className="rounded-xl text-[13px] font-mono"
              />
              <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.findTimeHint')}</p>
            </div>

            {/* Ignore IP */}
            <div className="space-y-1.5">
              <label className="text-[13px] font-medium">{t('firewall.fail2ban.ignoreIp')}</label>
              <Input
                placeholder="127.0.0.1/8 ::1"
                value={editIgnoreIP}
                onChange={(e) => setEditIgnoreIP(e.target.value)}
                className="rounded-xl text-[13px] font-mono"
              />
              <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.ignoreIpHelp')}</p>
            </div>
          </div>

          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              onClick={() => setEditConfigOpen(false)}
              disabled={editConfigLoading}
              className="rounded-xl"
            >
              {t('common.cancel')}
            </Button>
            <Button
              onClick={handleSaveConfig}
              disabled={editConfigLoading}
              className="rounded-xl"
            >
              {editConfigLoading && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('common.save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add Jail Dialog */}
      <Dialog open={addJailOpen} onOpenChange={(open) => { if (!addJailLoading) setAddJailOpen(open) }}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Plus className="h-4 w-4" />
              {t('firewall.fail2ban.addJailTitle')}
            </DialogTitle>
            <DialogDescription>
              {t('firewall.fail2ban.addJailDesc')}
            </DialogDescription>
          </DialogHeader>

          {/* Template Grid */}
          <div className="space-y-3">
            <label className="text-[13px] font-medium">{t('firewall.fail2ban.selectTemplate')}</label>
            {templatesLoading ? (
              <div className="flex items-center justify-center py-8 text-muted-foreground text-[13px]">
                <Loader2 className="h-4 w-4 animate-spin mr-2" />
                {t('common.loading')}
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-2 max-h-[240px] overflow-y-auto">
                {/* Custom jail option */}
                <button
                  type="button"
                  onClick={handleSelectCustom}
                  className={`text-left rounded-xl px-3 py-2.5 border transition-all ${
                    isCustomMode
                      ? 'bg-primary/10 border-primary/30 ring-1 ring-primary/20'
                      : 'bg-card border-border hover:border-primary/30 hover:bg-muted/50'
                  }`}
                >
                  <div className="flex items-center justify-between">
                    <span className="text-[13px] font-medium">{t('firewall.fail2ban.customJail')}</span>
                    {isCustomMode && <Check className="h-3.5 w-3.5 text-primary" />}
                  </div>
                  <p className="text-[11px] text-muted-foreground mt-0.5 line-clamp-1">{t('firewall.fail2ban.customJailDesc')}</p>
                </button>
                {templates.map((tmpl) => {
                  const isActive = !tmpl.available && templates.some(t2 => t2.id === tmpl.id)
                  return (
                    <button
                      key={tmpl.id}
                      type="button"
                      onClick={() => tmpl.available && handleSelectTemplate(tmpl)}
                      disabled={!tmpl.available}
                      className={`text-left rounded-xl px-3 py-2.5 border transition-all ${
                        selectedTemplate?.id === tmpl.id
                          ? 'bg-primary/10 border-primary/30 ring-1 ring-primary/20'
                          : tmpl.available
                            ? 'bg-card border-border hover:border-primary/30 hover:bg-muted/50'
                            : 'bg-muted/30 border-border/50 opacity-60 cursor-not-allowed'
                      }`}
                    >
                      <div className="flex items-center justify-between">
                        <span className="text-[13px] font-medium font-mono">{tmpl.name}</span>
                        {selectedTemplate?.id === tmpl.id ? (
                          <Check className="h-3.5 w-3.5 text-primary" />
                        ) : !tmpl.available ? (
                          <span className="text-[10px] text-muted-foreground">
                            {isActive ? t('firewall.fail2ban.templateActive') : t('firewall.fail2ban.templateUnavailable')}
                          </span>
                        ) : (
                          <span className="text-[10px] text-[#00c471]">{t('firewall.fail2ban.templateAvailable')}</span>
                        )}
                      </div>
                      <p className="text-[11px] text-muted-foreground mt-0.5 line-clamp-1">{tmpl.description}</p>
                    </button>
                  )
                })}
              </div>
            )}
          </div>

          {/* Config fields (shown when template or custom selected) */}
          {(selectedTemplate || isCustomMode) && (
            <div className="space-y-3 border-t border-border/50 pt-4">
              {/* Custom-only fields: jail name and filter */}
              {isCustomMode && (
                <>
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-1.5">
                      <label className="text-[13px] font-medium">{t('firewall.fail2ban.jailName')}</label>
                      <Input
                        value={customName}
                        onChange={(e) => setCustomName(e.target.value)}
                        placeholder="my-custom-jail"
                        className="rounded-xl text-[13px] font-mono"
                      />
                      <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.jailNameHint')}</p>
                    </div>
                    <div className="space-y-1.5">
                      <label className="text-[13px] font-medium">{t('firewall.fail2ban.filterName')}</label>
                      <Input
                        value={customFilter}
                        onChange={(e) => setCustomFilter(e.target.value)}
                        placeholder="sshd"
                        className="rounded-xl text-[13px] font-mono"
                      />
                      <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.filterNameHint')}</p>
                    </div>
                  </div>
                </>
              )}
              <div className="space-y-1.5">
                <label className="text-[13px] font-medium">{t('firewall.fail2ban.logPath')}</label>
                <Input
                  value={newLogPath}
                  onChange={(e) => setNewLogPath(e.target.value)}
                  placeholder="/var/log/auth.log"
                  className="rounded-xl text-[13px] font-mono"
                />
                <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.logPathHint')}</p>
              </div>
              <div className="grid grid-cols-3 gap-3">
                <div className="space-y-1.5">
                  <label className="text-[13px] font-medium">{t('firewall.fail2ban.maxRetry')}</label>
                  <Input
                    type="number"
                    min={1}
                    value={newMaxRetry}
                    onChange={(e) => setNewMaxRetry(e.target.value)}
                    className="rounded-xl text-[13px] font-mono"
                  />
                </div>
                <div className="space-y-1.5">
                  <label className="text-[13px] font-medium">{t('firewall.fail2ban.banTime')}</label>
                  <Input
                    type="number"
                    value={newBanTime}
                    onChange={(e) => setNewBanTime(e.target.value)}
                    className="rounded-xl text-[13px] font-mono"
                  />
                </div>
                <div className="space-y-1.5">
                  <label className="text-[13px] font-medium">{t('firewall.fail2ban.findTime')}</label>
                  <Input
                    type="number"
                    value={newFindTime}
                    onChange={(e) => setNewFindTime(e.target.value)}
                    className="rounded-xl text-[13px] font-mono"
                  />
                </div>
              </div>
              <div className="space-y-1.5">
                <label className="text-[13px] font-medium">{t('firewall.fail2ban.ignoreIp')}</label>
                <Input
                  placeholder="127.0.0.1/8 ::1"
                  value={newIgnoreIP}
                  onChange={(e) => setNewIgnoreIP(e.target.value)}
                  className="rounded-xl text-[13px] font-mono"
                />
                <p className="text-[11px] text-muted-foreground">{t('firewall.fail2ban.ignoreIpHelp')}</p>
              </div>
            </div>
          )}

          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              onClick={() => setAddJailOpen(false)}
              disabled={addJailLoading}
              className="rounded-xl"
            >
              {t('common.cancel')}
            </Button>
            <Button
              onClick={handleCreateJail}
              disabled={addJailLoading || (!selectedTemplate && !isCustomMode) || (isCustomMode && (!customName || !customFilter || !newLogPath))}
              className="rounded-xl"
            >
              {addJailLoading ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {t('common.creating')}
                </>
              ) : (
                <>
                  <Plus className="h-4 w-4" />
                  {t('common.create')}
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Jail Dialog */}
      <Dialog open={deleteJailDialog.open} onOpenChange={(open) => { if (!deleteJailLoading) setDeleteJailDialog(prev => ({ ...prev, open })) }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 text-[#f04452]">
              <Trash2 className="h-4 w-4" />
              {t('firewall.fail2ban.deleteJail')}
            </DialogTitle>
            <DialogDescription>
              {t('firewall.fail2ban.deleteJailConfirm', { name: deleteJailDialog.name })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              onClick={() => setDeleteJailDialog({ open: false, name: '' })}
              disabled={deleteJailLoading}
              className="rounded-xl"
            >
              {t('common.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={handleDeleteJail}
              disabled={deleteJailLoading}
              className="rounded-xl"
            >
              {deleteJailLoading ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {t('common.delete')}
                </>
              ) : (
                <>
                  <Trash2 className="h-4 w-4" />
                  {t('common.delete')}
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
