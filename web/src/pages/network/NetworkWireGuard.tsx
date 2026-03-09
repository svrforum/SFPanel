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
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import type { WireGuardStatus, WireGuardInterface } from '@/types/api'
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

  const copyToClipboard = (text: string, key: string) => {
    navigator.clipboard.writeText(text)
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
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          {status?.version && (
            <span className="text-[11px] text-muted-foreground">{status.version}</span>
          )}
        </div>
        <div className="flex items-center gap-2">
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
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-2">
                <Shield className="h-4 w-4 text-[#3182f6]" />
                <span className="text-[15px] font-semibold">{iface.name}</span>
                <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium ${
                  iface.active
                    ? 'bg-[#00c471]/10 text-[#00c471]'
                    : 'bg-secondary text-muted-foreground'
                }`}>
                  {iface.active ? t('network.wireguard.active') : t('network.wireguard.inactive')}
                </span>
              </div>
              <div className="flex items-center gap-1">
                <Button
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => handleToggle(iface.name, iface.active)}
                  disabled={toggling === iface.name}
                  title={iface.active ? t('network.wireguard.down') : t('network.wireguard.up')}
                >
                  {toggling === iface.name ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : iface.active ? (
                    <PowerOff className="h-4 w-4 text-[#f04452]" />
                  ) : (
                    <Power className="h-4 w-4 text-[#00c471]" />
                  )}
                </Button>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => openEdit(iface.name)}
                  title={t('common.edit')}
                >
                  <Settings2 className="h-4 w-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  className="text-[#f04452] hover:text-[#f04452]"
                  onClick={() => setDeleteTarget(iface.name)}
                  title={t('common.delete')}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            </div>

            {/* Info */}
            <div className="space-y-2 text-[13px]">
              {iface.address && (
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">{t('network.wireguard.address')}</span>
                  <span className="font-mono">{iface.address}</span>
                </div>
              )}
              {iface.dns && (
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">DNS</span>
                  <span className="font-mono">{iface.dns}</span>
                </div>
              )}
              {iface.public_key && (
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground flex items-center gap-1">
                    <Key className="h-3 w-3" />
                    {t('network.wireguard.publicKey')}
                  </span>
                  <button
                    className="font-mono text-[11px] truncate max-w-[200px] hover:text-primary flex items-center gap-1"
                    onClick={() => copyToClipboard(iface.public_key, iface.name)}
                    title={iface.public_key}
                  >
                    {iface.public_key.substring(0, 20)}...
                    {copiedKey === iface.name ? <Check className="h-3 w-3 text-[#00c471]" /> : <Copy className="h-3 w-3" />}
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
            {iface.peers.length > 0 && (
              <div className="mt-4 pt-4 border-t border-border">
                <h4 className="text-[11px] text-muted-foreground uppercase tracking-wider mb-3">
                  {t('network.wireguard.peers')} ({iface.peers.length})
                </h4>
                <div className="space-y-3">
                  {iface.peers.map((peer, idx) => (
                    <div key={idx} className="bg-secondary/30 rounded-xl p-3 space-y-1.5 text-[12px]">
                      {peer.endpoint && (
                        <div className="flex items-center justify-between">
                          <span className="text-muted-foreground">{t('network.wireguard.endpoint')}</span>
                          <span className="font-mono">{peer.endpoint}</span>
                        </div>
                      )}
                      {peer.allowed_ips && peer.allowed_ips.length > 0 && (
                        <div className="flex items-center justify-between">
                          <span className="text-muted-foreground">{t('network.wireguard.allowedIPs')}</span>
                          <span className="font-mono text-[11px]">{peer.allowed_ips.join(', ')}</span>
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
                          <span className="text-[#3182f6]">{formatBytes(peer.transfer_tx)}</span>
                          {' / '}
                          <span className="text-[#00c471]">{formatBytes(peer.transfer_rx)}</span>
                        </span>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        ))}
      </div>

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
