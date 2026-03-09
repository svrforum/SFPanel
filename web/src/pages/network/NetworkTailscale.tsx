import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Globe,
  Download,
  Loader2,
  RefreshCw,
  Power,
  PowerOff,
  LogOut,
  Copy,
  Check,
  ExternalLink,
  Monitor,
  ArrowUpDown,
  CheckCircle2,
  ArrowUpCircle,
  Shield,
  Route,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import type { TailscaleStatus, TailscalePeer } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface OutputDialog {
  open: boolean
  title: string
  output: string
  done: boolean
}

export default function NetworkTailscale() {
  const { t } = useTranslation()

  const [status, setStatus] = useState<TailscaleStatus | null>(null)
  const [peers, setPeers] = useState<TailscalePeer[]>([])
  const [loading, setLoading] = useState(true)
  const [installing, setInstalling] = useState(false)

  // Install output dialog
  const [outputDialog, setOutputDialog] = useState<OutputDialog>({
    open: false,
    title: '',
    output: '',
    done: false,
  })

  // Connect state
  const [authKey, setAuthKey] = useState('')
  const [connecting, setConnecting] = useState(false)
  const [authURL, setAuthURL] = useState('')

  // Disconnect / logout
  const [disconnecting, setDisconnecting] = useState(false)
  const [logoutDialogOpen, setLogoutDialogOpen] = useState(false)
  const [loggingOut, setLoggingOut] = useState(false)

  // Exit node / settings
  const [settingExitNode, setSettingExitNode] = useState<string | null>(null) // IP being set, or null
  const [togglingAcceptRoutes, setTogglingAcceptRoutes] = useState(false)
  const [togglingAdvertise, setTogglingAdvertise] = useState(false)

  // Update check
  const [checkingUpdate, setCheckingUpdate] = useState(false)
  const [updateInfo, setUpdateInfo] = useState<{ available: boolean; version: string } | null>(null)

  // Copy
  const [copiedField, setCopiedField] = useState<string | null>(null)

  const fetchData = useCallback(async () => {
    try {
      setLoading(true)
      const statusData = await api.getTailscaleStatus()
      setStatus(statusData)

      if (statusData.auth_url) {
        setAuthURL(statusData.auth_url)
      }

      if (statusData.installed && statusData.backend_state === 'Running') {
        const peerData = await api.getTailscalePeers()
        setPeers(peerData || [])
      }
    } catch {
      toast.error(t('network.tailscale.fetchFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchData()
  }, [fetchData])

  const appendOutput = useCallback((text: string) => {
    setOutputDialog((prev) => ({ ...prev, output: prev.output + text }))
  }, [])

  const finishOutput = useCallback(() => {
    setOutputDialog((prev) => ({ ...prev, done: true }))
  }, [])

  const handleInstall = async () => {
    setInstalling(true)
    setOutputDialog({
      open: true,
      title: t('network.tailscale.installingTitle'),
      output: '',
      done: false,
    })

    try {
      const token = api.getToken()
      const res = await fetch('/api/v1/network/tailscale/install', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${token}`,
        },
      })

      if (!res.ok || !res.body) {
        throw new Error('Failed to start Tailscale installation')
      }

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          if (line.startsWith('data: ')) {
            const data = line.slice(6)
            if (data === '[DONE]') {
              finishOutput()
            } else {
              appendOutput(data + '\n')
            }
          }
        }
      }

      toast.success(t('network.tailscale.installSuccess'))
      finishOutput()
      await fetchData()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.tailscale.installFailed')
      appendOutput('\nERROR: ' + msg)
      finishOutput()
      toast.error(msg)
    } finally {
      setInstalling(false)
    }
  }

  const handleConnect = async () => {
    setConnecting(true)
    setAuthURL('')
    try {
      const result = await api.tailscaleUp(authKey || undefined)
      if (result && 'needs_auth' in result && result.needs_auth) {
        const url = 'auth_url' in result ? result.auth_url : ''
        if (url) {
          setAuthURL(url)
          // Automatically open auth URL in new tab
          window.open(url, '_blank', 'noopener,noreferrer')
          toast.info(t('network.tailscale.authRequired'))
        } else {
          // No URL yet, refetch status to get it
          toast.info(t('network.tailscale.authRequired'))
          await fetchData()
        }
      } else {
        toast.success(t('network.tailscale.connected'))
        setAuthKey('')
        await fetchData()
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.tailscale.connectFailed')
      toast.error(msg)
    } finally {
      setConnecting(false)
    }
  }

  const handleDisconnect = async () => {
    setDisconnecting(true)
    try {
      await api.tailscaleDown()
      toast.success(t('network.tailscale.disconnected'))
      await fetchData()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.tailscale.disconnectFailed')
      toast.error(msg)
    } finally {
      setDisconnecting(false)
    }
  }

  const handleLogout = async () => {
    setLoggingOut(true)
    try {
      await api.tailscaleLogout()
      toast.success(t('network.tailscale.loggedOut'))
      setLogoutDialogOpen(false)
      await fetchData()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.tailscale.logoutFailed')
      toast.error(msg)
    } finally {
      setLoggingOut(false)
    }
  }

  const handleSetExitNode = async (ip: string) => {
    setSettingExitNode(ip || 'clear')
    try {
      // When using another node as exit node, must disable advertising as exit node
      const options: { exit_node: string; advertise_exit_node?: boolean } = { exit_node: ip }
      if (ip && status?.advertise_exit_node) {
        options.advertise_exit_node = false
      }
      await api.setTailscalePreferences(options)
      toast.success(ip ? t('network.tailscale.exitNodeSet') : t('network.tailscale.exitNodeCleared'))
      await fetchData()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.tailscale.exitNodeFailed')
      toast.error(msg)
    } finally {
      setSettingExitNode(null)
    }
  }

  const handleToggleAcceptRoutes = async (enabled: boolean) => {
    setTogglingAcceptRoutes(true)
    try {
      await api.setTailscalePreferences({ accept_routes: enabled })
      toast.success(enabled
        ? t('network.tailscale.acceptRoutesEnabled')
        : t('network.tailscale.acceptRoutesDisabled'))
      await fetchData()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.tailscale.settingsFailed')
      toast.error(msg)
    } finally {
      setTogglingAcceptRoutes(false)
    }
  }

  const handleToggleAdvertiseExitNode = async (enabled: boolean) => {
    setTogglingAdvertise(true)
    try {
      // When advertising as exit node, must clear any active exit node usage
      const options: { advertise_exit_node: boolean; exit_node?: string } = { advertise_exit_node: enabled }
      if (enabled && status?.current_exit_node) {
        options.exit_node = ''
      }
      await api.setTailscalePreferences(options)
      toast.success(enabled
        ? t('network.tailscale.advertiseExitNodeEnabled')
        : t('network.tailscale.advertiseExitNodeDisabled'))
      await fetchData()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.tailscale.settingsFailed')
      toast.error(msg)
    } finally {
      setTogglingAdvertise(false)
    }
  }

  const handleCheckUpdate = async () => {
    setCheckingUpdate(true)
    setUpdateInfo(null)
    try {
      const result = await api.checkTailscaleUpdate()
      setUpdateInfo({
        available: result.update_available,
        version: result.new_version,
      })
      if (result.update_available) {
        toast.info(t('network.tailscale.updateAvailable', { version: result.new_version }))
      } else {
        toast.success(t('network.tailscale.upToDate'))
      }
    } catch {
      toast.error(t('network.tailscale.updateCheckFailed'))
    } finally {
      setCheckingUpdate(false)
    }
  }

  const copyToClipboard = (text: string, field: string) => {
    navigator.clipboard.writeText(text)
    setCopiedField(field)
    setTimeout(() => setCopiedField(null), 2000)
  }

  // Loading
  if (loading && !status) {
    return (
      <div className="flex items-center justify-center py-16">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        <span className="ml-2 text-[13px] text-muted-foreground">{t('common.loading')}</span>
      </div>
    )
  }

  // Not installed
  if (status && !status.installed) {
    return (
      <>
        <div className="bg-card rounded-2xl p-8 card-shadow text-center max-w-lg mx-auto">
          <Globe className="h-12 w-12 text-muted-foreground mx-auto mb-4" />
          <h3 className="text-[15px] font-semibold mb-2">{t('network.tailscale.notInstalled')}</h3>
          <p className="text-[13px] text-muted-foreground mb-6">{t('network.tailscale.notInstalledDesc')}</p>
          <Button onClick={handleInstall} disabled={installing}>
            {installing ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
            {installing ? t('network.tailscale.installing') : t('network.tailscale.install')}
          </Button>
        </div>

        {/* Install Output Dialog */}
        <Dialog
          open={outputDialog.open}
          onOpenChange={(open) => {
            if (!open && outputDialog.done) {
              setOutputDialog({ open: false, title: '', output: '', done: false })
            }
          }}
        >
          <DialogContent className="sm:max-w-2xl">
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                {!outputDialog.done && <Loader2 className="h-4 w-4 animate-spin" />}
                {outputDialog.done && <CheckCircle2 className="h-4 w-4 text-[#00c471]" />}
                {outputDialog.title}
              </DialogTitle>
              <DialogDescription>
                {outputDialog.done
                  ? t('network.tailscale.operationComplete')
                  : t('network.tailscale.operationRunning')}
              </DialogDescription>
            </DialogHeader>
            <div className="bg-zinc-950 text-zinc-100 rounded-lg p-4 max-h-96 overflow-y-auto">
              <pre className="text-xs font-mono whitespace-pre-wrap break-words">
                {outputDialog.output || t('network.tailscale.waitingForOutput')}
              </pre>
            </div>
            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => setOutputDialog({ open: false, title: '', output: '', done: false })}
                disabled={!outputDialog.done}
              >
                {outputDialog.done ? t('network.tailscale.close') : t('network.tailscale.pleaseWait')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </>
    )
  }

  // Needs login / stopped
  const isConnected = status?.backend_state === 'Running'

  if (!isConnected) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          {status?.version && (
            <div className="flex items-center gap-2">
              <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">
                {t('network.tailscale.version')}: {status.version}
              </span>
              <Button
                variant="ghost"
                size="sm"
                className="h-6 text-[11px] px-2"
                onClick={handleCheckUpdate}
                disabled={checkingUpdate}
              >
                {checkingUpdate ? (
                  <Loader2 className="h-3 w-3 animate-spin" />
                ) : (
                  <ArrowUpCircle className="h-3 w-3" />
                )}
                {t('network.tailscale.checkUpdate')}
              </Button>
              {updateInfo && (
                <span className={`text-[11px] ${updateInfo.available ? 'text-[#f59e0b] font-medium' : 'text-[#00c471]'}`}>
                  {updateInfo.available
                    ? t('network.tailscale.updateAvailable', { version: updateInfo.version })
                    : t('network.tailscale.upToDate')}
                </span>
              )}
            </div>
          )}
          <Button variant="outline" size="sm" onClick={fetchData} disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
        </div>

        <div className="bg-card rounded-2xl p-6 card-shadow max-w-lg mx-auto">
          <div className="text-center mb-6">
            <Globe className="h-10 w-10 text-[#3182f6] mx-auto mb-3" />
            <h3 className="text-[15px] font-semibold mb-1">{t('network.tailscale.notConnected')}</h3>
            <p className="text-[13px] text-muted-foreground">
              {status?.backend_state === 'NeedsLogin'
                ? t('network.tailscale.needsLogin')
                : t('network.tailscale.notConnectedDesc')}
            </p>
          </div>

          <div className="space-y-4">
            <div className="space-y-2">
              <Label className="text-[13px]">{t('network.tailscale.authKey')}</Label>
              <Input
                value={authKey}
                onChange={(e) => setAuthKey(e.target.value)}
                placeholder={t('network.tailscale.authKeyPlaceholder')}
                className="font-mono text-[13px]"
                type="password"
              />
              <p className="text-[11px] text-muted-foreground">{t('network.tailscale.authKeyHint')}</p>
            </div>

            <Button className="w-full" onClick={handleConnect} disabled={connecting}>
              {connecting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Power className="h-4 w-4" />}
              {connecting ? t('network.tailscale.installing') : t('network.tailscale.connect')}
            </Button>

            {authURL && (
              <div className="bg-[#3182f6]/10 rounded-xl p-3">
                <p className="text-[13px] text-[#3182f6] font-medium mb-2">{t('network.tailscale.authRequired')}</p>
                <a
                  href={authURL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 text-[13px] text-[#3182f6] hover:underline font-mono break-all"
                >
                  {authURL}
                  <ExternalLink className="h-3 w-3 shrink-0" />
                </a>
              </div>
            )}
          </div>
        </div>
      </div>
    )
  }

  // Connected — full UI
  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          {status?.version && (
            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">
              {t('network.tailscale.version')}: {status.version}
            </span>
          )}
          <Button
            variant="ghost"
            size="sm"
            className="h-6 text-[11px] px-2"
            onClick={handleCheckUpdate}
            disabled={checkingUpdate}
          >
            {checkingUpdate ? (
              <Loader2 className="h-3 w-3 animate-spin" />
            ) : (
              <ArrowUpCircle className="h-3 w-3" />
            )}
            {t('network.tailscale.checkUpdate')}
          </Button>
          {updateInfo && (
            <span className={`text-[11px] ${updateInfo.available ? 'text-[#f59e0b] font-medium' : 'text-[#00c471]'}`}>
              {updateInfo.available
                ? t('network.tailscale.updateAvailable', { version: updateInfo.version })
                : t('network.tailscale.upToDate')}
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={fetchData} disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={handleDisconnect}
            disabled={disconnecting}
          >
            {disconnecting ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <PowerOff className="h-3.5 w-3.5" />}
            {t('network.tailscale.disconnect')}
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="text-[#f04452] hover:text-[#f04452]"
            onClick={() => setLogoutDialogOpen(true)}
          >
            <LogOut className="h-3.5 w-3.5" />
            {t('network.tailscale.logout')}
          </Button>
        </div>
      </div>

      {/* Self Info + Network Settings */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {/* Self Info Card */}
        {status?.self && (
          <div className="bg-card rounded-2xl p-5 card-shadow">
            <h3 className="text-[11px] text-muted-foreground uppercase tracking-wider mb-3">
              {t('network.tailscale.thisDevice')}
            </h3>
            <div className="space-y-2 text-[13px]">
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">{t('network.tailscale.hostname')}</span>
                <span className="font-semibold">{status.self.hostname}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">{t('network.tailscale.tailscaleIP')}</span>
                <button
                  className="font-mono flex items-center gap-1 hover:text-primary"
                  onClick={() => copyToClipboard(status.self!.tailscale_ip, 'ip4')}
                >
                  {status.self.tailscale_ip}
                  {copiedField === 'ip4' ? <Check className="h-3 w-3 text-[#00c471]" /> : <Copy className="h-3 w-3" />}
                </button>
              </div>
              {status.self.tailscale_ipv6 && (
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">IPv6</span>
                  <button
                    className="font-mono text-[11px] flex items-center gap-1 hover:text-primary truncate max-w-[200px]"
                    onClick={() => copyToClipboard(status.self!.tailscale_ipv6, 'ip6')}
                    title={status.self.tailscale_ipv6}
                  >
                    {status.self.tailscale_ipv6}
                    {copiedField === 'ip6' ? <Check className="h-3 w-3 text-[#00c471]" /> : <Copy className="h-3 w-3" />}
                  </button>
                </div>
              )}
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">OS</span>
                <span>{status.self.os}</span>
              </div>
              {status.tailnet_name && (
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">{t('network.tailscale.tailnet')}</span>
                  <span className="font-medium">{status.tailnet_name}</span>
                </div>
              )}
              {status.magic_dns_suffix && (
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">MagicDNS</span>
                  <span className="font-mono text-[11px]">{status.magic_dns_suffix}</span>
                </div>
              )}
            </div>
          </div>
        )}

        {/* Network Settings Card */}
        <div className="bg-card rounded-2xl p-5 card-shadow">
          <h3 className="text-[11px] text-muted-foreground uppercase tracking-wider mb-3">
            {t('network.tailscale.networkSettings')}
          </h3>
          <div className="space-y-3">
            {/* Accept Routes */}
            <button
              className={`w-full flex items-center justify-between rounded-xl p-3 transition-colors ${
                status?.accept_routes
                  ? 'bg-[#00c471]/10 ring-1 ring-[#00c471]/20'
                  : 'bg-secondary/50 hover:bg-secondary/80'
              }`}
              onClick={() => handleToggleAcceptRoutes(!status?.accept_routes)}
              disabled={togglingAcceptRoutes}
            >
              <div className="flex items-center gap-3 text-left">
                <div className={`flex items-center justify-center w-8 h-8 rounded-lg ${
                  status?.accept_routes ? 'bg-[#00c471]/20' : 'bg-secondary'
                }`}>
                  <Route className={`h-4 w-4 ${status?.accept_routes ? 'text-[#00c471]' : 'text-muted-foreground'}`} />
                </div>
                <div>
                  <div className="text-[13px] font-medium">{t('network.tailscale.acceptRoutes')}</div>
                  <p className="text-[11px] text-muted-foreground">{t('network.tailscale.acceptRoutesDesc')}</p>
                </div>
              </div>
              <div className="ml-3 shrink-0">
                {togglingAcceptRoutes ? (
                  <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                ) : (
                  <div className={`w-10 h-6 rounded-full transition-colors relative ${
                    status?.accept_routes ? 'bg-[#00c471]' : 'bg-secondary'
                  }`}>
                    <div className={`absolute top-1 w-4 h-4 rounded-full bg-white shadow transition-transform ${
                      status?.accept_routes ? 'translate-x-5' : 'translate-x-1'
                    }`} />
                  </div>
                )}
              </div>
            </button>

            {/* Advertise as Exit Node */}
            <button
              className={`w-full flex items-center justify-between rounded-xl p-3 transition-colors ${
                status?.advertise_exit_node
                  ? 'bg-[#3182f6]/10 ring-1 ring-[#3182f6]/20'
                  : 'bg-secondary/50 hover:bg-secondary/80'
              }`}
              onClick={() => handleToggleAdvertiseExitNode(!status?.advertise_exit_node)}
              disabled={togglingAdvertise}
            >
              <div className="flex items-center gap-3 text-left">
                <div className={`flex items-center justify-center w-8 h-8 rounded-lg ${
                  status?.advertise_exit_node ? 'bg-[#3182f6]/20' : 'bg-secondary'
                }`}>
                  <Shield className={`h-4 w-4 ${status?.advertise_exit_node ? 'text-[#3182f6]' : 'text-muted-foreground'}`} />
                </div>
                <div>
                  <div className="text-[13px] font-medium">{t('network.tailscale.advertiseExitNode')}</div>
                  <p className="text-[11px] text-muted-foreground">{t('network.tailscale.advertiseExitNodeDesc')}</p>
                </div>
              </div>
              <div className="ml-3 shrink-0">
                {togglingAdvertise ? (
                  <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                ) : (
                  <div className={`w-10 h-6 rounded-full transition-colors relative ${
                    status?.advertise_exit_node ? 'bg-[#3182f6]' : 'bg-secondary'
                  }`}>
                    <div className={`absolute top-1 w-4 h-4 rounded-full bg-white shadow transition-transform ${
                      status?.advertise_exit_node ? 'translate-x-5' : 'translate-x-1'
                    }`} />
                  </div>
                )}
              </div>
            </button>

            {/* Admin Console Hint */}
            {(status?.accept_routes || status?.advertise_exit_node) && (
              <p className="text-[11px] text-muted-foreground px-1 flex items-center gap-1">
                <ExternalLink className="h-3 w-3 shrink-0" />
                {status?.advertise_exit_node
                  ? t('network.tailscale.advertiseExitNodeHint')
                  : t('network.tailscale.acceptRoutesHint')}
              </p>
            )}

            {/* Exit Node Select */}
            {(() => {
              const exitNodePeers = peers.filter(p => p.exit_node_option || p.exit_node)
              if (exitNodePeers.length === 0) return null
              return (
                <div className="rounded-xl p-3 bg-secondary/30">
                  <div className="flex items-center gap-3 mb-2">
                    <div className="flex items-center justify-center w-8 h-8 rounded-lg bg-secondary">
                      <Globe className="h-4 w-4 text-muted-foreground" />
                    </div>
                    <div>
                      <div className="text-[13px] font-medium">{t('network.tailscale.selectExitNode')}</div>
                      <p className="text-[11px] text-muted-foreground">{t('network.tailscale.selectExitNodeDesc')}</p>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <select
                      className="flex-1 h-9 rounded-xl bg-secondary/50 border-0 text-[13px] px-3 focus:outline-none focus:ring-2 focus:ring-primary/20"
                      value={status?.current_exit_node || ''}
                      onChange={(e) => handleSetExitNode(e.target.value)}
                      disabled={settingExitNode !== null}
                    >
                      <option value="">{t('network.tailscale.noExitNode')}</option>
                      {exitNodePeers.map((p) => (
                        <option key={p.tailscale_ip} value={p.tailscale_ip}>
                          {p.hostname} ({p.tailscale_ip}) {p.online ? '' : `— ${t('network.tailscale.offline')}`}
                        </option>
                      ))}
                    </select>
                    {settingExitNode !== null && (
                      <Loader2 className="h-4 w-4 animate-spin text-muted-foreground shrink-0" />
                    )}
                  </div>
                </div>
              )
            })()}
          </div>
        </div>
      </div>

      {/* Peers Table */}
      <div>
        <h3 className="text-[15px] font-semibold mb-3 flex items-center gap-2">
          <Monitor className="h-4 w-4" />
          {t('network.tailscale.peers')}
          <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
            {peers.length}
          </span>
        </h3>

        {peers.length === 0 ? (
          <div className="bg-card rounded-2xl p-8 card-shadow text-center">
            <Monitor className="h-8 w-8 text-muted-foreground mx-auto mb-2" />
            <p className="text-[13px] text-muted-foreground">{t('network.tailscale.noPeers')}</p>
          </div>
        ) : (
          <div className="bg-card rounded-2xl card-shadow overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('network.tailscale.hostname')}</TableHead>
                  <TableHead>{t('network.tailscale.tailscaleIP')}</TableHead>
                  <TableHead>OS</TableHead>
                  <TableHead>{t('common.status')}</TableHead>
                  <TableHead className="text-right">
                    <ArrowUpDown className="h-3 w-3 inline" /> {t('network.tailscale.traffic')}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {peers.map((peer) => (
                  <TableRow key={peer.tailscale_ip}>
                    <TableCell className="font-medium text-[13px]">
                      <div className="flex items-center gap-1.5">
                        {peer.hostname}
                        {peer.exit_node && (
                          <span className="inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-[#3182f6]/10 text-[#3182f6]">
                            Exit
                          </span>
                        )}
                        {peer.exit_node_option && !peer.exit_node && (
                          <span className="inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-secondary text-muted-foreground">
                            {t('network.tailscale.exitNodeAvailable')}
                          </span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-[13px]">
                      <button
                        className="hover:text-primary flex items-center gap-1"
                        onClick={() => copyToClipboard(peer.tailscale_ip, peer.tailscale_ip)}
                      >
                        {peer.tailscale_ip}
                        {copiedField === peer.tailscale_ip ? (
                          <Check className="h-3 w-3 text-[#00c471]" />
                        ) : (
                          <Copy className="h-3 w-3 opacity-0 group-hover:opacity-100" />
                        )}
                      </button>
                    </TableCell>
                    <TableCell className="text-[13px] text-muted-foreground">{peer.os}</TableCell>
                    <TableCell>
                      <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium ${
                        peer.online
                          ? 'bg-[#00c471]/10 text-[#00c471]'
                          : 'bg-secondary text-muted-foreground'
                      }`}>
                        {peer.online ? t('network.tailscale.online') : t('network.tailscale.offline')}
                      </span>
                    </TableCell>
                    <TableCell className="text-right text-[12px]">
                      {(peer.tx_bytes > 0 || peer.rx_bytes > 0) ? (
                        <span>
                          <span className="text-[#3182f6]">{formatBytes(peer.tx_bytes)}</span>
                          {' / '}
                          <span className="text-[#00c471]">{formatBytes(peer.rx_bytes)}</span>
                        </span>
                      ) : (
                        <span className="text-muted-foreground">-</span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      {/* Logout Confirmation Dialog */}
      <Dialog open={logoutDialogOpen} onOpenChange={setLogoutDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('network.tailscale.logoutTitle')}</DialogTitle>
            <DialogDescription>{t('network.tailscale.logoutConfirm')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setLogoutDialogOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleLogout} disabled={loggingOut}>
              {loggingOut && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t('network.tailscale.logout')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
