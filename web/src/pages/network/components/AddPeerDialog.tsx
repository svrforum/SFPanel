import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, RefreshCw, Copy, Check, UserPlus, X, AlertTriangle } from 'lucide-react'
import { toast } from 'sonner'
import { QRCodeSVG } from 'qrcode.react'
import { api } from '@/lib/api'
import { useCopyFeedback } from '@/hooks/useCopyFeedback'
import type { WireGuardInterface } from '@/types/api'
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

/**
 * Add-peer flow for a WireGuard interface: keypair generation → peer
 * registration → client config + QR display. Owns all form state; the page
 * only supplies the target interface and refreshes on close when a peer was
 * actually created.
 */
export function AddPeerDialog({
  iface,
  onClose,
}: {
  iface: WireGuardInterface | null
  // createdPeer=true when a peer was registered, so the caller should refetch.
  onClose: (createdPeer: boolean) => void
}) {
  const { t } = useTranslation()
  const { copy, copiedKey } = useCopyFeedback()

  const [keypair, setKeypair] = useState<{ private_key: string; public_key: string } | null>(null)
  const [genLoading, setGenLoading] = useState(false)
  const [peerAddress, setPeerAddress] = useState('')
  const [peerEndpoint, setPeerEndpoint] = useState('')
  const [peerDns, setPeerDns] = useState('')
  const [peerKeepalive, setPeerKeepalive] = useState('25')
  const [peerCreating, setPeerCreating] = useState(false)
  const [clientConfig, setClientConfig] = useState<string | null>(null)

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

  // Reset the form and generate a fresh keypair each time the dialog opens.
  useEffect(() => {
    if (!iface) return
    setKeypair(null)
    setClientConfig(null)
    setPeerAddress('')
    setPeerEndpoint(`${window.location.hostname}:${iface.listen_port || ''}`)
    setPeerDns(iface.dns || '')
    setPeerKeepalive('25')
    generateKeypair()
  }, [iface, generateKeypair])

  const buildClientConfig = (target: WireGuardInterface, privateKey: string) => {
    const keepalive = parseInt(peerKeepalive, 10)
    const lines = [
      '[Interface]',
      `PrivateKey = ${privateKey}`,
      `Address = ${peerAddress.trim()}`,
    ]
    if (peerDns.trim()) lines.push(`DNS = ${peerDns.trim()}`)
    lines.push('')
    lines.push('[Peer]')
    lines.push(`PublicKey = ${target.public_key}`)
    lines.push(`Endpoint = ${peerEndpoint.trim()}`)
    lines.push('AllowedIPs = 0.0.0.0/0, ::/0')
    if (keepalive > 0) lines.push(`PersistentKeepalive = ${keepalive}`)
    return lines.join('\n')
  }

  const handleCreatePeer = async () => {
    if (!iface || !keypair || !peerAddress.trim()) return
    setPeerCreating(true)
    try {
      const keepalive = parseInt(peerKeepalive, 10)
      await api.addWireGuardPeer(iface.name, {
        public_key: keypair.public_key,
        allowed_ips: [peerAddress.trim()],
        persistent_keepalive: keepalive > 0 ? keepalive : undefined,
      })
      setClientConfig(buildClientConfig(iface, keypair.private_key))
      toast.success(t('network.wireguard.peers.created'))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      setPeerCreating(false)
    }
  }

  const handleClose = () => onClose(!!clientConfig)

  const copyConfig = async () => {
    if (clientConfig && !(await copy(clientConfig, 'peer-config'))) {
      toast.error('Failed to copy to clipboard')
    }
  }

  return (
    <Dialog open={!!iface} onOpenChange={(open) => !open && handleClose()}>
      <DialogContent className="sm:max-w-lg max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <UserPlus className="h-5 w-5" />
            {t('network.wireguard.peers.addTitle')}
          </DialogTitle>
          <DialogDescription>
            {t('network.wireguard.peers.addDesc', { name: iface?.name })}
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
              <Button variant="outline" onClick={handleClose}>
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
                    onClick={copyConfig}
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
              <Button onClick={handleClose}>
                <X className="h-3.5 w-3.5" />
                {t('network.wireguard.peers.done')}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
