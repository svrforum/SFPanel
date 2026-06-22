import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Shield,
  Download,
  Loader2,
  RefreshCw,
  Power,
  PowerOff,
  Plus,
  Trash2,
  Settings2,
  Upload,
  Clock,
  ArrowUpDown,
  Key,
  Copy,
  Check,
  UserPlus,
  X,
  AlertTriangle,
} from 'lucide-react'
import { toast } from 'sonner'
import { QRCodeSVG } from 'qrcode.react'
import { api } from '@/lib/api'
import { formatBytes, copyText } from '@/lib/utils'
import type { WireGuardStatus, WireGuardInterface } from '@/types/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

export default function NetworkWireGuard() {
  const { t } = useTranslation()
  const confirm = useConfirm()

  const [status, setStatus] = useState<WireGuardStatus | null>(null)
  const [interfaces, setInterfaces] = useState<WireGuardInterface[]>([])
  const [loading, setLoading] = useState(true)
  const [installing, setInstalling] = useState(false)

  // Create config dialog
  const [createOpen, setCreateOpen] = useState(false)
  const [createName, setCreateName] = useState('')
  const [createContent, setCreateContent] = useState('')
  const [creating, setCreating] = useState(false)

  // Edit config dialog
  const [editTarget, setEditTarget] = useState<string | null>(null)
  const [editContent, setEditContent] = useState('')
  const [editSaving, setEditSaving] = useState(false)

  // Delete dialog
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)

  // Toggle (up/down) loading
  const [toggling, setToggling] = useState<string | null>(null)

  // Copied state for public key
  const [copiedKey, setCopiedKey] = useState<string | null>(null)

  // Add-peer dialog
  const [peerIface, setPeerIface] = useState<WireGuardInterface | null>(null)
  const [keypair, setKeypair] = useState<{ private_key: string; public_key: string } | null>(null)
  const [genLoading, setGenLoading] = useState(false)
  const [peerAddress, setPeerAddress] = useState('')
  const [peerEndpoint, setPeerEndpoint] = useState('')
  const [peerDns, setPeerDns] = useState('')
  const [peerKeepalive, setPeerKeepalive] = useState('25')
  const [peerCreating, setPeerCreating] = useState(false)
  const [clientConfig, setClientConfig] = useState<string | null>(null)

  // Autostart action loading (per interface)
  const [autostartBusy, setAutostartBusy] = useState<string | null>(null)

  // Remove-peer loading (per public key)
  const [removingPeer, setRemovingPeer] = useState<string | null>(null)

  const fetchData = useCallback(async () => {
    try {
      setLoading(true)
      const statusData = await api.getWireGuardStatus()
      setStatus(statusData)

      if (statusData.installed) {
        const ifaceData = await api.getWireGuardInterfaces()
        setInterfaces(ifaceData || [])
      }
    } catch {
      toast.error(t('network.wireguard.fetchFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchData()
  }, [fetchData])

  const handleInstall = async () => {
    setInstalling(true)
    try {
      await api.installWireGuard()
      toast.success(t('network.wireguard.installSuccess'))
      await fetchData()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.wireguard.installFailed')
      toast.error(msg)
    } finally {
      setInstalling(false)
    }
  }

  const handleToggle = async (name: string, active: boolean) => {
    setToggling(name)
    try {
      if (active) {
        await api.wireGuardInterfaceDown(name)
      } else {
        await api.wireGuardInterfaceUp(name)
      }
      toast.success(active ? t('network.wireguard.downSuccess') : t('network.wireguard.upSuccess'))
      await fetchData()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.wireguard.toggleFailed')
      toast.error(msg)
    } finally {
      setToggling(null)
    }
  }

  const handleCreate = async () => {
    if (!createName.trim() || !createContent.trim()) return
    setCreating(true)
    try {
      await api.createWireGuardConfig(createName.trim(), createContent)
      toast.success(t('network.wireguard.createSuccess'))
      setCreateOpen(false)
      setCreateName('')
      setCreateContent('')
      await fetchData()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.wireguard.createFailed')
      toast.error(msg)
    } finally {
      setCreating(false)
    }
  }

  const openEdit = async (name: string) => {
    try {
      const data = await api.getWireGuardConfig(name)
      setEditContent(data.content)
      setEditTarget(name)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.wireguard.fetchFailed')
      toast.error(msg)
    }
  }

  const handleEditSave = async () => {
    if (!editTarget || !editContent.trim()) return
    // Validate that masked keys are not being saved back
    if (editContent.includes('********')) {
      toast.error(t('network.wireguard.maskedKeyError'))
      return
    }
    setEditSaving(true)
    try {
      await api.updateWireGuardConfig(editTarget, editContent)
      toast.success(t('network.wireguard.updateSuccess'))
      setEditTarget(null)
      await fetchData()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.wireguard.updateFailed')
      toast.error(msg)
    } finally {
      setEditSaving(false)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await api.deleteWireGuardConfig(deleteTarget)
      toast.success(t('network.wireguard.deleteSuccess'))
      setDeleteTarget(null)
      await fetchData()
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('network.wireguard.deleteFailed')
      toast.error(msg)
    } finally {
      setDeleting(false)
    }
  }

  const generateKeypair = useCallback(async () => {
    setGenLoading(true)
    try {
      const kp = await api.generateWireGuardKeypair()
      setKeypair(kp)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      setGenLoading(false)
    }
  }, [])

  const openAddPeer = (iface: WireGuardInterface) => {
    setPeerIface(iface)
    setKeypair(null)
    setClientConfig(null)
    setPeerAddress('')
    setPeerEndpoint(`${window.location.hostname}:${iface.listen_port || ''}`)
    setPeerDns(iface.dns || '')
    setPeerKeepalive('25')
    generateKeypair()
  }

  const buildClientConfig = (iface: WireGuardInterface, privateKey: string) => {
    const keepalive = parseInt(peerKeepalive, 10)
    const lines = [
      '[Interface]',
      `PrivateKey = ${privateKey}`,
      `Address = ${peerAddress.trim()}`,
    ]
    if (peerDns.trim()) lines.push(`DNS = ${peerDns.trim()}`)
    lines.push('')
    lines.push('[Peer]')
    lines.push(`PublicKey = ${iface.public_key}`)
    lines.push(`Endpoint = ${peerEndpoint.trim()}`)
    lines.push('AllowedIPs = 0.0.0.0/0, ::/0')
    if (keepalive > 0) lines.push(`PersistentKeepalive = ${keepalive}`)
    return lines.join('\n')
  }

  const handleCreatePeer = async () => {
    if (!peerIface || !keypair || !peerAddress.trim()) return
    setPeerCreating(true)
    try {
      const keepalive = parseInt(peerKeepalive, 10)
      await api.addWireGuardPeer(peerIface.name, {
        public_key: keypair.public_key,
        allowed_ips: [peerAddress.trim()],
        persistent_keepalive: keepalive > 0 ? keepalive : undefined,
      })
      setClientConfig(buildClientConfig(peerIface, keypair.private_key))
      toast.success(t('network.wireguard.peers.created'))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      setPeerCreating(false)
    }
  }

  const closeAddPeer = async () => {
    const reload = !!clientConfig
    setPeerIface(null)
    setKeypair(null)
    setClientConfig(null)
    if (reload) await fetchData()
  }

  const handleRemovePeer = async (name: string, publicKey: string) => {
    if (!(await confirm({ title: t('network.wireguard.peers.removeConfirm', { name }), danger: true }))) return
    setRemovingPeer(publicKey)
    try {
      await api.removeWireGuardPeer(name, publicKey)
      toast.success(t('network.wireguard.peers.removed'))
      await fetchData()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('network.wireguard.peers.removeFailed'))
    } finally {
      setRemovingPeer(null)
    }
  }

  const handleAutostart = async (name: string, enabled: boolean) => {
    setAutostartBusy(name)
    try {
      await api.setWireGuardAutostart(name, enabled)
      toast.success(enabled
        ? t('network.wireguard.peers.autostartEnabled')
        : t('network.wireguard.peers.autostartDisabled'))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('network.wireguard.peers.autostartFailed'))
    } finally {
      setAutostartBusy(null)
    }
  }

  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return

    const reader = new FileReader()
    reader.onload = () => {
      setCreateContent(reader.result as string)
      // Auto-fill name from filename (without .conf)
      const name = file.name.replace(/\.conf$/, '')
      if (!createName) setCreateName(name)
    }
    reader.readAsText(file)
    e.target.value = ''
  }

  const copyToClipboard = async (text: string, key: string) => {
    if (!(await copyText(text))) {
      toast.error('Failed to copy to clipboard')
      return
    }
    setCopiedKey(key)
    setTimeout(() => setCopiedKey(null), 2000)
  }

  const formatHandshake = (ts: number) => {
    if (!ts || ts === 0) return t('network.wireguard.never')
    const seconds = Math.floor(Date.now() / 1000) - ts
    if (seconds < 60) return t('network.wireguard.secondsAgo', { count: seconds })
    if (seconds < 3600) return t('network.wireguard.minutesAgo', { count: Math.floor(seconds / 60) })
    if (seconds < 86400) return t('network.wireguard.hoursAgo', { count: Math.floor(seconds / 3600) })
    return t('network.wireguard.daysAgo', { count: Math.floor(seconds / 86400) })
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
      <div className="bg-card rounded-2xl p-8 card-shadow text-center max-w-lg mx-auto">
        <Shield className="h-12 w-12 text-muted-foreground mx-auto mb-4" />
        <h3 className="text-[15px] font-semibold mb-2">{t('network.wireguard.notInstalled')}</h3>
        <p className="text-[13px] text-muted-foreground mb-6">{t('network.wireguard.notInstalledDesc')}</p>
        <Button onClick={handleInstall} disabled={installing}>
          {installing ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
          {installing ? t('network.wireguard.installing') : t('network.wireguard.install')}
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-3">
          {status?.version && (
            <span className="text-[11px] text-muted-foreground">{status.version}</span>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={fetchData} disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="h-3.5 w-3.5" />
            {t('network.wireguard.addConfig')}
          </Button>
        </div>
      </div>

      {/* Empty state */}
      {interfaces.length === 0 && (
        <div className="bg-card rounded-2xl p-8 card-shadow text-center">
          <Shield className="h-10 w-10 text-muted-foreground mx-auto mb-3" />
          <h3 className="text-[15px] font-semibold mb-1">{t('network.wireguard.noInterfaces')}</h3>
          <p className="text-[13px] text-muted-foreground mb-4">{t('network.wireguard.noInterfacesDesc')}</p>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="h-3.5 w-3.5" />
            {t('network.wireguard.addConfig')}
          </Button>
        </div>
      )}

      {/* Interface Cards */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {interfaces.map((iface) => (
          <div key={iface.name} className="bg-card rounded-2xl p-5 card-shadow">
            {/* Interface Header */}
            <div className="flex items-center justify-between gap-2 mb-4">
              <div className="flex items-center gap-2 min-w-0">
                <Shield className="h-4 w-4 text-primary shrink-0" />
                <span className="text-[15px] font-semibold truncate min-w-0" title={iface.name}>{iface.name}</span>
                <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium shrink-0 ${
                  iface.active
                    ? 'bg-success/10 text-success'
                    : 'bg-secondary text-muted-foreground'
                }`}>
                  {iface.active ? t('network.wireguard.active') : t('network.wireguard.inactive')}
                </span>
              </div>
              <div className="flex items-center gap-1 shrink-0">
                <Button
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => openAddPeer(iface)}
                  title={t('network.wireguard.peers.addPeer')}
                  aria-label={t('network.wireguard.peers.addPeer')}
                >
                  <UserPlus className="h-4 w-4 text-primary" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => handleToggle(iface.name, iface.active)}
                  disabled={toggling === iface.name}
                  title={iface.active ? t('network.wireguard.down') : t('network.wireguard.up')}
                  aria-label={iface.active ? t('network.wireguard.down') : t('network.wireguard.up')}
                >
                  {toggling === iface.name ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : iface.active ? (
                    <PowerOff className="h-4 w-4 text-destructive" />
                  ) : (
                    <Power className="h-4 w-4 text-success" />
                  )}
                </Button>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => openEdit(iface.name)}
                  title={t('common.edit')}
                  aria-label={t('common.edit')}
                >
                  <Settings2 className="h-4 w-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  className="text-destructive hover:text-destructive"
                  onClick={() => setDeleteTarget(iface.name)}
                  title={t('common.delete')}
                  aria-label={t('common.delete')}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            </div>

            {/* Info */}
            <div className="space-y-2 text-[13px]">
              {iface.address && (
                <div className="flex items-center justify-between gap-2 min-w-0">
                  <span className="text-muted-foreground shrink-0">{t('network.wireguard.address')}</span>
                  <span className="font-mono truncate min-w-0 text-right" title={iface.address}>{iface.address}</span>
                </div>
              )}
              {iface.dns && (
                <div className="flex items-center justify-between gap-2 min-w-0">
                  <span className="text-muted-foreground shrink-0">DNS</span>
                  <span className="font-mono truncate min-w-0 text-right" title={iface.dns}>{iface.dns}</span>
                </div>
              )}
              {iface.public_key && (
                <div className="flex items-center justify-between gap-2 min-w-0">
                  <span className="text-muted-foreground flex items-center gap-1 shrink-0">
                    <Key className="h-3 w-3" />
                    {t('network.wireguard.publicKey')}
                  </span>
                  <button
                    className="font-mono text-[11px] truncate min-w-0 max-w-[200px] hover:text-primary flex items-center gap-1 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                    onClick={() => copyToClipboard(iface.public_key, iface.name)}
                    title={iface.public_key}
                    aria-label={t('common.copy')}
                  >
                    {iface.public_key.substring(0, 20)}...
                    {copiedKey === iface.name ? <Check className="h-3 w-3 text-success" /> : <Copy className="h-3 w-3" />}
                  </button>
                </div>
              )}
              {iface.listen_port > 0 && (
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">{t('network.wireguard.listenPort')}</span>
                  <span className="font-mono">{iface.listen_port}</span>
                </div>
              )}
            </div>

            {/* Peers */}
            <div className="mt-4 pt-4 border-t border-border">
              <h4 className="text-[11px] text-muted-foreground uppercase tracking-wider mb-3">
                {t('network.wireguard.peers.title')} ({iface.peers.length})
              </h4>
              {iface.peers.length === 0 ? (
                <p className="text-[12px] text-muted-foreground">{t('network.wireguard.peers.noPeers')}</p>
              ) : (
                <div className="space-y-3">
                  {iface.peers.map((peer, idx) => (
                    <div key={idx} className="bg-secondary/30 rounded-xl p-3 space-y-1.5 text-[12px]">
                      <div className="flex items-start justify-between gap-2">
                        <span className="text-muted-foreground flex items-center gap-1">
                          <Key className="h-3 w-3" />
                          {t('network.wireguard.publicKey')}
                        </span>
                        <div className="flex items-center gap-1">
                          <span className="font-mono text-[11px]" title={peer.public_key}>
                            {peer.public_key.substring(0, 16)}...
                          </span>
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            className="h-5 w-5 text-destructive hover:text-destructive"
                            onClick={() => handleRemovePeer(iface.name, peer.public_key)}
                            disabled={removingPeer === peer.public_key}
                            title={t('network.wireguard.peers.removePeer')}
                            aria-label={t('network.wireguard.peers.removePeer')}
                          >
                            {removingPeer === peer.public_key ? (
                              <Loader2 className="h-3 w-3 animate-spin" />
                            ) : (
                              <Trash2 className="h-3 w-3" />
                            )}
                          </Button>
                        </div>
                      </div>
                      {peer.endpoint && (
                        <div className="flex items-center justify-between gap-2 min-w-0">
                          <span className="text-muted-foreground shrink-0">{t('network.wireguard.endpoint')}</span>
                          <span className="font-mono truncate min-w-0 text-right" title={peer.endpoint}>{peer.endpoint}</span>
                        </div>
                      )}
                      {peer.allowed_ips && peer.allowed_ips.length > 0 && (
                        <div className="flex items-center justify-between gap-2 min-w-0">
                          <span className="text-muted-foreground shrink-0">{t('network.wireguard.allowedIPs')}</span>
                          <span className="font-mono text-[11px] truncate min-w-0 text-right" title={peer.allowed_ips.join(', ')}>{peer.allowed_ips.join(', ')}</span>
                        </div>
                      )}
                      <div className="flex items-center justify-between">
                        <span className="text-muted-foreground flex items-center gap-1">
                          <Clock className="h-3 w-3" />
                          {t('network.wireguard.lastHandshake')}
                        </span>
                        <span>{formatHandshake(peer.latest_handshake)}</span>
                      </div>
                      <div className="flex items-center justify-between">
                        <span className="text-muted-foreground flex items-center gap-1">
                          <ArrowUpDown className="h-3 w-3" />
                          {t('network.wireguard.transfer')}
                        </span>
                        <span>
                          <span className="text-primary">{formatBytes(peer.transfer_tx)}</span>
                          {' / '}
                          <span className="text-success">{formatBytes(peer.transfer_rx)}</span>
                        </span>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Autostart */}
            <div className="mt-4 pt-4 border-t border-border flex items-center justify-between">
              <span className="text-[12px] text-muted-foreground flex items-center gap-1">
                <Power className="h-3 w-3" />
                {t('network.wireguard.peers.autostart')}
              </span>
              <div className="flex items-center gap-1.5">
                <Button
                  variant="outline"
                  size="sm"
                  className="h-7 text-[12px]"
                  onClick={() => handleAutostart(iface.name, true)}
                  disabled={autostartBusy === iface.name}
                >
                  {autostartBusy === iface.name && <Loader2 className="h-3 w-3 animate-spin" />}
                  {t('network.wireguard.peers.autostartEnable')}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="h-7 text-[12px]"
                  onClick={() => handleAutostart(iface.name, false)}
                  disabled={autostartBusy === iface.name}
                >
                  {t('network.wireguard.peers.autostartDisable')}
                </Button>
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* Add Peer Dialog */}
      <Dialog open={!!peerIface} onOpenChange={(open) => !open && closeAddPeer()}>
        <DialogContent className="sm:max-w-lg max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <UserPlus className="h-5 w-5" />
              {t('network.wireguard.peers.addTitle')}
            </DialogTitle>
            <DialogDescription>
              {t('network.wireguard.peers.addDesc', { name: peerIface?.name })}
            </DialogDescription>
          </DialogHeader>

          {!clientConfig ? (
            <>
              <div className="space-y-4">
                {/* Generated client public key */}
                <div className="space-y-2">
                  <div className="flex items-center justify-between">
                    <Label className="text-[13px]">{t('network.wireguard.peers.clientPublicKey')}</Label>
                    <button
                      type="button"
                      className="inline-flex items-center gap-1 text-[12px] text-primary hover:text-primary/80 font-medium disabled:opacity-50 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                      onClick={generateKeypair}
                      disabled={genLoading}
                    >
                      <RefreshCw className={`h-3 w-3 ${genLoading ? 'animate-spin' : ''}`} />
                      {t('network.wireguard.peers.regenerate')}
                    </button>
                  </div>
                  <div className="flex items-center gap-2 rounded-xl bg-secondary/50 px-3 py-2 text-[12px] font-mono min-h-[38px] min-w-0">
                    {genLoading || !keypair ? (
                      <span className="flex items-center gap-2 text-muted-foreground">
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        {t('network.wireguard.peers.generating')}
                      </span>
                    ) : (
                      <span className="truncate min-w-0" title={keypair.public_key}>{keypair.public_key}</span>
                    )}
                  </div>
                </div>

                <div className="space-y-2">
                  <Label className="text-[13px]">{t('network.wireguard.peers.clientAddress')}</Label>
                  <Input
                    value={peerAddress}
                    onChange={(e) => setPeerAddress(e.target.value)}
                    placeholder="10.0.0.2/32"
                    className="font-mono text-[13px]"
                  />
                  <p className="text-[11px] text-muted-foreground">{t('network.wireguard.peers.clientAddressHint')}</p>
                </div>

                <div className="space-y-2">
                  <Label className="text-[13px]">{t('network.wireguard.peers.serverEndpoint')}</Label>
                  <Input
                    value={peerEndpoint}
                    onChange={(e) => setPeerEndpoint(e.target.value)}
                    placeholder="vpn.example.com:51820"
                    className="font-mono text-[13px]"
                  />
                  <p className="text-[11px] text-muted-foreground">{t('network.wireguard.peers.serverEndpointHint')}</p>
                </div>

                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-2">
                    <Label className="text-[13px]">{t('network.wireguard.peers.dns')}</Label>
                    <Input
                      value={peerDns}
                      onChange={(e) => setPeerDns(e.target.value)}
                      placeholder="1.1.1.1"
                      className="font-mono text-[13px]"
                    />
                  </div>
                  <div className="space-y-2">
                    <Label className="text-[13px]">{t('network.wireguard.peers.keepalive')}</Label>
                    <Input
                      type="number"
                      value={peerKeepalive}
                      onChange={(e) => setPeerKeepalive(e.target.value)}
                      className="font-mono text-[13px]"
                    />
                  </div>
                </div>
              </div>

              <DialogFooter>
                <Button variant="outline" onClick={closeAddPeer}>
                  {t('common.cancel')}
                </Button>
                <Button
                  onClick={handleCreatePeer}
                  disabled={peerCreating || genLoading || !keypair || !peerAddress.trim() || !peerEndpoint.trim()}
                >
                  {peerCreating && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                  {peerCreating ? t('network.wireguard.peers.creating') : t('network.wireguard.peers.create')}
                </Button>
              </DialogFooter>
            </>
          ) : (
            <>
              <div className="space-y-4">
                <div className="flex items-start gap-2 rounded-xl bg-destructive/10 px-3 py-2 text-[12px] text-destructive">
                  <AlertTriangle className="h-4 w-4 shrink-0 mt-0.5" />
                  <span>{t('network.wireguard.peers.privateKeyWarning')}</span>
                </div>

                <div className="space-y-2">
                  <div className="flex items-center justify-between">
                    <Label className="text-[13px]">{t('network.wireguard.peers.clientConfig')}</Label>
                    <button
                      type="button"
                      className="inline-flex items-center gap-1 text-[12px] text-primary hover:text-primary/80 font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                      onClick={() => copyToClipboard(clientConfig, 'peer-config')}
                    >
                      {copiedKey === 'peer-config' ? (
                        <><Check className="h-3 w-3 text-success" />{t('network.wireguard.peers.copied')}</>
                      ) : (
                        <><Copy className="h-3 w-3" />{t('network.wireguard.peers.copyConfig')}</>
                      )}
                    </button>
                  </div>
                  <pre className="rounded-xl bg-secondary/50 px-3 py-2 text-[12px] font-mono whitespace-pre-wrap break-all">
                    {clientConfig}
                  </pre>
                </div>

                <div className="flex flex-col items-center gap-2">
                  <div className="bg-white p-3 rounded-xl">
                    <QRCodeSVG value={clientConfig} size={200} />
                  </div>
                  <p className="text-[11px] text-muted-foreground">{t('network.wireguard.peers.scanQr')}</p>
                </div>
              </div>

              <DialogFooter>
                <Button onClick={closeAddPeer}>
                  <X className="h-3.5 w-3.5" />
                  {t('network.wireguard.peers.done')}
                </Button>
              </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>

      {/* Create Config Dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Plus className="h-5 w-5" />
              {t('network.wireguard.addConfig')}
            </DialogTitle>
            <DialogDescription>
              {t('network.wireguard.addConfigDesc')}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4">
            <div className="space-y-2">
              <Label className="text-[13px]">{t('network.wireguard.configName')}</Label>
              <Input
                value={createName}
                onChange={(e) => setCreateName(e.target.value)}
                placeholder="wg0"
                className="font-mono text-[13px]"
              />
              <p className="text-[11px] text-muted-foreground">{t('network.wireguard.configNameHint')}</p>
            </div>

            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <Label className="text-[13px]">{t('network.wireguard.configContent')}</Label>
                <label className="cursor-pointer">
                  <input
                    type="file"
                    accept=".conf"
                    className="hidden"
                    onChange={handleFileUpload}
                  />
                  <span className="inline-flex items-center gap-1 text-[12px] text-primary hover:text-primary/80 font-medium">
                    <Upload className="h-3 w-3" />
                    {t('network.wireguard.uploadFile')}
                  </span>
                </label>
              </div>
              <textarea
                value={createContent}
                onChange={(e) => setCreateContent(e.target.value)}
                placeholder={`[Interface]\nPrivateKey = ...\nAddress = 10.0.0.2/24\nDNS = 1.1.1.1\n\n[Peer]\nPublicKey = ...\nEndpoint = vpn.example.com:51820\nAllowedIPs = 0.0.0.0/0`}
                className="flex w-full rounded-xl border-0 bg-secondary/50 px-3 py-2 text-[13px] font-mono transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/20 min-h-[200px] resize-none"
                rows={10}
              />
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreate} disabled={creating || !createName.trim() || !createContent.trim()}>
              {creating && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Config Dialog */}
      <Dialog open={!!editTarget} onOpenChange={(open) => !open && setEditTarget(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Settings2 className="h-5 w-5" />
              {t('network.wireguard.editConfig')} — {editTarget}
            </DialogTitle>
            <DialogDescription>
              {t('network.wireguard.editConfigDesc')}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4">
            <textarea
              value={editContent}
              onChange={(e) => setEditContent(e.target.value)}
              className="flex w-full rounded-xl border-0 bg-secondary/50 px-3 py-2 text-[13px] font-mono transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/20 min-h-[250px] resize-none"
              rows={12}
            />
            <p className="text-[11px] text-muted-foreground">{t('network.wireguard.editConfigHint')}</p>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setEditTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleEditSave} disabled={editSaving || !editContent.trim()}>
              {editSaving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t('common.save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('network.wireguard.deleteTitle')}</DialogTitle>
            <DialogDescription>
              {t('network.wireguard.deleteConfirm', { name: deleteTarget })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleDelete} disabled={deleting}>
              {deleting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
