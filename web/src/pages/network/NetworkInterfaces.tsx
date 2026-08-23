import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Network as NetworkIcon,
  RefreshCw,
  Settings2,
  Cable,
  Link2,
  Unlink,
  Loader2,
  Shield,
  Globe,
  Router,
  Plus,
  Trash2,
  AlertTriangle,
  Container,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
 import { gatewayField } from './gatewayField'
import { useConfirm } from '@/components/ConfirmDialog'
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
import { InterfaceCard, getStateStyle } from './components/InterfaceCard'
import { BondCreateDialog } from './components/BondCreateDialog'

import type { NetworkInterfaceInfo, InterfaceConfig, NetworkRoute } from '@/types/api'

interface DNSConfig {
  servers: string[]
}

// Classify interfaces into categories
function classifyInterfaces(interfaces: NetworkInterfaceInfo[]) {
  const physical: NetworkInterfaceInfo[] = []
  const docker: NetworkInterfaceInfo[] = []
  const virtual: NetworkInterfaceInfo[] = []
  const loopback: NetworkInterfaceInfo[] = []

  for (const iface of interfaces) {
    if (iface.type === 'loopback' || iface.name === 'lo') {
      loopback.push(iface)
    } else if (
      iface.name.startsWith('docker') ||
      iface.name.startsWith('br-') ||
      iface.name.startsWith('veth')
    ) {
      docker.push(iface)
    } else if (
      iface.name.startsWith('eth') ||
      iface.name.startsWith('en') ||
      iface.name.startsWith('wl') ||
      iface.name.startsWith('ww') ||
      iface.bond_info ||
      iface.type === 'bond' ||
      iface.speed > 0
    ) {
      physical.push(iface)
    } else {
      virtual.push(iface)
    }
  }

  return { physical, docker, virtual, loopback }
}

export default function NetworkInterfaces() {
  const { t } = useTranslation()
  const confirm = useConfirm()

  // Data state
  const [interfaces, setInterfaces] = useState<NetworkInterfaceInfo[]>([])
  const [routes, setRoutes] = useState<NetworkRoute[]>([])
  const [dnsConfig, setDnsConfig] = useState<DNSConfig>({ servers: [] })
  const [loading, setLoading] = useState(true)
  // Session-local only: there is no backend endpoint reporting whether the
  // netplan config on disk differs from the running state, so a page reload
  // loses the "apply needed" flag (and the floating Apply button) even though
  // saved-but-unapplied changes may still exist.
  const [hasChanges, setHasChanges] = useState(false)

  // Interface config dialog
  const [configTarget, setConfigTarget] = useState<NetworkInterfaceInfo | null>(null)
  const [configMode, setConfigMode] = useState<'dhcp' | 'static'>('dhcp')
  const [configAddresses, setConfigAddresses] = useState('')
  // Whether the dialog managed to read the interface's saved gateway. False
  // means "we do not know", which is different from "there is none".
  const [gatewayKnown, setGatewayKnown] = useState(false)
  const [configGateway4, setConfigGateway4] = useState('')
  const [configGateway6, setConfigGateway6] = useState('')
  const [configDns, setConfigDns] = useState('')
  const [configMtu, setConfigMtu] = useState('')
  const [configSaving, setConfigSaving] = useState(false)

  // DNS edit state
  const [dnsEditing, setDnsEditing] = useState(false)
  const [dnsInput, setDnsInput] = useState('')
  const [dnsSaving, setDnsSaving] = useState(false)

  // Bond create dialog
  const [bondCreateOpen, setBondCreateOpen] = useState(false)

  // Apply config dialog
  const [applyDialogOpen, setApplyDialogOpen] = useState(false)
  const [applying, setApplying] = useState(false)

  // Docker section collapsed
  const [dockerCollapsed, setDockerCollapsed] = useState(true)

  // Fetch all data
  const fetchData = useCallback(async () => {
    try {
      setLoading(true)
      const status = await api.getNetworkStatus()
      setInterfaces(status.interfaces || [])
      setRoutes(status.routes || [])
      setDnsConfig(status.dns || { servers: [] })
    } catch {
      toast.error(t('network.fetchFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchData()
  }, [fetchData])

  // Open config dialog for an interface
  const openConfigDialog = (iface: NetworkInterfaceInfo) => {
    setConfigTarget(iface)
    const hasStaticAddr = iface.addresses.length > 0 && iface.type !== 'loopback'
    setConfigMode(hasStaticAddr ? 'static' : 'dhcp')
    setConfigAddresses(
      iface.addresses
        .filter((a) => a.family === 'ipv4' || a.family === 'ipv6' || a.family === 'inet' || a.family === 'inet6')
        .map((a) => `${a.address}/${a.prefix}`)
        .join('\n')
    )
    setConfigGateway4('')
    setConfigGateway6('')
    setConfigDns(dnsConfig.servers.join(', '))
    setConfigMtu(iface.mtu > 0 ? String(iface.mtu) : '')

    // Fill the gateway in from what is actually saved. The dialog opened blank
    // and sent that blank on save, so changing an MTU deleted the host's
    // default route — on a remote machine, that is the last request it ever
    // serves. The list endpoint does not carry the gateway, so ask for the
    // interface's own config.
    setGatewayKnown(false)
    api
      .getInterfaceDetail(iface.name)
      .then((detail) => {
        if (!detail?.config) return
        setConfigGateway4(detail.config.gateway4 ?? '')
        setConfigGateway6(detail.config.gateway6 ?? '')
        setGatewayKnown(true)
      })
      .catch(() => {
        // Stays unknown, and an unknown gateway is not sent at all.
      })
  }

  // Save interface configuration
  const handleSaveConfig = async () => {
    if (!configTarget) return
    setConfigSaving(true)
    try {
      const config: InterfaceConfig = {
        dhcp4: configMode === 'dhcp',
        dhcp6: false,
        addresses: configMode === 'static' ? configAddresses.split('\n').map((a) => a.trim()).filter(Boolean) : [],
        // Omitted when we never learned the saved value and the operator has
        // not typed one: the server leaves a missing gateway alone, and a
        // blank box the dialog could not fill is not a request to delete the
        // default route. On hosts whose netplan the panel cannot read — a
        // NetworkManager-rendered file, for instance — that blank box is the
        // normal case.
        gateway4: gatewayField(configMode, configGateway4, gatewayKnown),
        gateway6: gatewayField(configMode, configGateway6, gatewayKnown),
        dns: configMode === 'static' ? configDns.split(',').map((d) => d.trim()).filter(Boolean) : [],
        mtu: configMtu ? parseInt(configMtu, 10) : undefined,
      }
      await api.configureInterface(configTarget.name, config)
      toast.success(t('network.saveSuccess'))
      setConfigTarget(null)
      setHasChanges(true)
      await fetchData()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('network.saveFailed')
      toast.error(message)
    } finally {
      setConfigSaving(false)
    }
  }

  // Save DNS config
  const handleSaveDns = async () => {
    setDnsSaving(true)
    try {
      const servers = dnsInput.split(',').map((d) => d.trim()).filter(Boolean)
      await api.configureDNS({ servers })
      toast.success(t('network.dnsSaved'))
      setDnsEditing(false)
      setHasChanges(true)
      await fetchData()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('network.dnsSaveFailed')
      toast.error(message)
    } finally {
      setDnsSaving(false)
    }
  }

  // Delete bond
  const handleDeleteBond = async (bond: NetworkInterfaceInfo) => {
    if (!(await confirm({
      title: t('network.deleteBondTitle'),
      description: t('network.deleteBondConfirm', { name: bond.name }),
      danger: true,
    }))) return
    try {
      await api.deleteBond(bond.name)
      toast.success(t('network.bondDeleted', { name: bond.name }))
      setHasChanges(true)
      await fetchData()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('network.bondDeleteFailed')
      toast.error(message)
    }
  }

  // Apply network config
  const handleApplyConfig = async () => {
    setApplying(true)
    try {
      await api.applyNetworkConfig()
      toast.success(t('network.applySuccess'))
      setApplyDialogOpen(false)
      setHasChanges(false)
      await fetchData()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('network.applyFailed')
      toast.error(message)
    } finally {
      setApplying(false)
    }
  }

  // Classify interfaces
  const classified = classifyInterfaces(interfaces)
  const bondInterfaces = interfaces.filter((i) => i.bond_info)
  const availableSlaves = interfaces.filter(
    (i) => i.type !== 'loopback' && !i.bond_info && i.type !== 'bond'
  )

  // Protocol label
  const protocolLabel = (proto: string) => {
    switch (proto) {
      case 'kernel': return 'Kernel'
      case 'boot': return 'Boot'
      case 'static': return 'Static'
      case 'dhcp': return 'DHCP'
      case 'redirect': return 'Redirect'
      default: return proto || '-'
    }
  }

  const renderCards = (list: NetworkInterfaceInfo[]) =>
    list.map((iface) => (
      <InterfaceCard key={iface.name} iface={iface} onConfigure={openConfigDialog} />
    ))

  return (
    <div className="space-y-6">
      {/* Top actions */}
      <div className="flex items-center justify-end">
        <Button variant="outline" size="sm" className="rounded-xl" onClick={fetchData} disabled={loading}>
          <RefreshCw className={loading ? 'animate-spin' : ''} />
          {t('common.refresh')}
        </Button>
      </div>

      {/* Loading state */}
      {loading && interfaces.length === 0 && (
        <div className="flex items-center justify-center py-16">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          <span className="ml-2 text-[13px] text-muted-foreground">{t('common.loading')}</span>
        </div>
      )}

      {/* Empty state */}
      {!loading && interfaces.length === 0 && (
        <div className="bg-card rounded-2xl p-8 card-shadow text-center">
          <NetworkIcon className="h-10 w-10 text-muted-foreground mx-auto mb-3" />
          <p className="text-[13px] text-muted-foreground">{t('network.noInterfaces')}</p>
        </div>
      )}

      {interfaces.length > 0 && (
        <>
          {/* Physical / Main Interfaces */}
          {classified.physical.length > 0 && (
            <div>
              <h2 className="text-[15px] font-semibold mb-3 flex items-center gap-2">
                <Cable className="h-4 w-4" />
                {t('network.interfaces')}
                <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
                  {classified.physical.length}
                </span>
              </h2>
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                {renderCards(classified.physical)}
              </div>
            </div>
          )}

          {/* Loopback */}
          {classified.loopback.length > 0 && (
            <div>
              <h2 className="text-[15px] font-semibold mb-3 flex items-center gap-2">
                <Router className="h-4 w-4" />
                {t('network.loopback')}
              </h2>
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                {renderCards(classified.loopback)}
              </div>
            </div>
          )}

          {/* Virtual (non-Docker) */}
          {classified.virtual.length > 0 && (
            <div>
              <h2 className="text-[15px] font-semibold mb-3 flex items-center gap-2">
                <NetworkIcon className="h-4 w-4" />
                {t('network.virtual')}
                <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
                  {classified.virtual.length}
                </span>
              </h2>
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                {renderCards(classified.virtual)}
              </div>
            </div>
          )}

          {/* Docker Interfaces — collapsible */}
          {classified.docker.length > 0 && (
            <div>
              <button
                className="flex items-center gap-2 mb-3 group outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                onClick={() => setDockerCollapsed(!dockerCollapsed)}
                aria-expanded={!dockerCollapsed}
                aria-controls="docker-interfaces"
              >
                <Container className="h-4 w-4 text-primary" />
                <h2 className="text-[15px] font-semibold">Docker</h2>
                <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
                  {classified.docker.length}
                </span>
                <span className="text-[11px] text-muted-foreground ml-1">
                  {dockerCollapsed ? '▸' : '▾'}
                </span>
              </button>
              {!dockerCollapsed && (
                <div id="docker-interfaces" className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                  {renderCards(classified.docker)}
                </div>
              )}
            </div>
          )}
        </>
      )}

      {/* DNS Section */}
      <div>
        <h2 className="text-[15px] font-semibold mb-3 flex items-center gap-2">
          <Globe className="h-4 w-4" />
          {t('network.dnsServers')}
        </h2>
        <div className="bg-card rounded-2xl p-5 card-shadow">
          {!dnsEditing ? (
            <div className="flex items-center justify-between">
              <div className="flex-1">
                {dnsConfig.servers.length > 0 ? (
                  <div className="flex flex-wrap gap-2">
                    {dnsConfig.servers.map((server, idx) => (
                      <span
                        key={idx}
                        className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-mono bg-secondary"
                      >
                        {server}
                      </span>
                    ))}
                  </div>
                ) : (
                  <p className="text-[13px] text-muted-foreground">{t('network.noDns')}</p>
                )}
              </div>
              <Button
                variant="outline"
                size="sm"
                className="rounded-xl"
                onClick={() => {
                  setDnsInput(dnsConfig.servers.join(', '))
                  setDnsEditing(true)
                }}
              >
                <Settings2 className="h-3.5 w-3.5" />
                {t('common.edit')}
              </Button>
            </div>
          ) : (
            <div className="space-y-3">
              <div className="space-y-2">
                <Label className="text-[13px]">{t('network.dnsServersLabel')}</Label>
                <Input
                  value={dnsInput}
                  onChange={(e) => setDnsInput(e.target.value)}
                  placeholder="8.8.8.8, 8.8.4.4, 1.1.1.1"
                  className="pl-3 h-9 rounded-xl bg-secondary/50 border-0 text-[13px] font-mono"
                />
                <p className="text-[11px] text-muted-foreground">{t('network.dnsHint')}</p>
              </div>
              <div className="flex items-center gap-2 justify-end">
                <Button variant="outline" size="sm" className="rounded-xl" onClick={() => setDnsEditing(false)}>
                  {t('common.cancel')}
                </Button>
                <Button size="sm" className="rounded-xl" onClick={handleSaveDns} disabled={dnsSaving}>
                  {dnsSaving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                  {t('common.save')}
                </Button>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Routing Table Section */}
      <div>
        <h2 className="text-[15px] font-semibold mb-3 flex items-center gap-2">
          <Router className="h-4 w-4" />
          {t('network.routes')}
          <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
            {routes.length}
          </span>
        </h2>
        <div className="bg-card rounded-2xl card-shadow overflow-hidden overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('network.destination')}</TableHead>
                <TableHead>{t('network.gatewayCol')}</TableHead>
                <TableHead>{t('network.interface')}</TableHead>
                <TableHead className="text-right">{t('network.metric')}</TableHead>
                <TableHead>{t('network.protocol')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {routes.length === 0 && !loading && (
                <TableRow>
                  <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                    {t('network.noRoutes')}
                  </TableCell>
                </TableRow>
              )}
              {routes.map((route, idx) => (
                <TableRow key={idx}>
                  <TableCell className="font-mono text-[13px]">
                    {route.destination === 'default' ? (
                      <span className="flex items-center gap-1.5">
                        <Shield className="h-3.5 w-3.5 text-primary" />
                        <span className="text-primary font-medium">default</span>
                      </span>
                    ) : (
                      route.destination
                    )}
                  </TableCell>
                  <TableCell className="font-mono text-[13px] text-muted-foreground">
                    {route.gateway || '-'}
                  </TableCell>
                  <TableCell className="text-[13px]">{route.interface}</TableCell>
                  <TableCell className="text-right text-[13px] text-muted-foreground">
                    {route.metric > 0 ? route.metric : '-'}
                  </TableCell>
                  <TableCell>
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">
                      {protocolLabel(route.protocol)}
                    </span>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* Bonding Section */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-[15px] font-semibold flex items-center gap-2">
            <Link2 className="h-4 w-4" />
            {t('network.bonding')}
            {bondInterfaces.length > 0 && (
              <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
                {bondInterfaces.length}
              </span>
            )}
          </h2>
          <Button size="sm" className="rounded-xl" onClick={() => setBondCreateOpen(true)}>
            <Plus className="h-3.5 w-3.5" />
            {t('network.createBond')}
          </Button>
        </div>

        {bondInterfaces.length === 0 ? (
          <div className="bg-card rounded-2xl p-8 card-shadow text-center">
            <Unlink className="h-8 w-8 text-muted-foreground mx-auto mb-2" />
            <p className="text-[13px] text-muted-foreground">{t('network.noBonds')}</p>
          </div>
        ) : (
          <div className="bg-card rounded-2xl card-shadow overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('common.name')}</TableHead>
                  <TableHead>{t('network.bondMode')}</TableHead>
                  <TableHead>{t('network.bondSlaves')}</TableHead>
                  <TableHead>{t('common.status')}</TableHead>
                  <TableHead className="text-right">{t('common.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {bondInterfaces.map((bond) => (
                  <TableRow key={bond.name}>
                    <TableCell className="font-medium">{bond.name}</TableCell>
                    <TableCell className="text-[13px]">
                      <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-warning/10 text-warning">
                        {bond.bond_info?.mode || '-'}
                      </span>
                    </TableCell>
                    <TableCell className="text-[13px] text-muted-foreground">
                      {bond.bond_info?.slaves.join(', ') || '-'}
                    </TableCell>
                    <TableCell>
                      <span className={getStateStyle(bond.state)}>
                        {bond.state === 'up' ? t('network.up') : t('network.down')}
                      </span>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        className="text-destructive hover:text-destructive"
                        onClick={() => handleDeleteBond(bond)}
                        aria-label={t('common.delete')}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      {/* Floating Apply Button */}
      {hasChanges && (
        <div className="fixed bottom-6 left-1/2 -translate-x-1/2 z-50">
          <Button
            size="lg"
            className="rounded-2xl px-6 shadow-lg"
            onClick={() => setApplyDialogOpen(true)}
          >
            <Shield className="h-4 w-4" />
            {t('network.applyConfig')}
          </Button>
        </div>
      )}

      {/* Interface Config Dialog */}
      <Dialog open={!!configTarget} onOpenChange={(open) => !open && setConfigTarget(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 min-w-0">
              <Settings2 className="h-5 w-5 shrink-0" />
              <span className="truncate min-w-0">{t('network.configureInterface')} — {configTarget?.name}</span>
            </DialogTitle>
            <DialogDescription>
              {t('network.configureDesc', { name: configTarget?.name })}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4">
            {/* DHCP / Static toggle */}
            <div className="space-y-2">
              <Label className="text-[13px] font-medium">{t('network.addressMode')}</Label>
              <div className="flex gap-2" role="radiogroup" aria-label={t('network.addressMode')}>
                <button
                  role="radio"
                  aria-checked={configMode === 'dhcp'}
                  className={`flex-1 py-2 rounded-xl text-[13px] font-medium transition-all outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
                    configMode === 'dhcp'
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-secondary text-muted-foreground hover:text-foreground'
                  }`}
                  onClick={() => setConfigMode('dhcp')}
                >
                  DHCP
                </button>
                <button
                  role="radio"
                  aria-checked={configMode === 'static'}
                  className={`flex-1 py-2 rounded-xl text-[13px] font-medium transition-all outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
                    configMode === 'static'
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-secondary text-muted-foreground hover:text-foreground'
                  }`}
                  onClick={() => setConfigMode('static')}
                >
                  Static
                </button>
              </div>
            </div>

            {/* Static config fields */}
            {configMode === 'static' && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="cfg-addresses" className="text-[13px]">{t('network.ipAddresses')}</Label>
                  <textarea
                    id="cfg-addresses"
                    value={configAddresses}
                    onChange={(e) => setConfigAddresses(e.target.value)}
                    placeholder="192.168.1.100/24&#10;10.0.0.1/8"
                    className="flex w-full rounded-xl border-0 bg-secondary/50 px-3 py-2 text-[13px] font-mono transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/20 min-h-[72px] resize-none"
                    rows={3}
                  />
                  <p className="text-[11px] text-muted-foreground">{t('network.ipAddressesHint')}</p>
                </div>

                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-2">
                    <Label htmlFor="cfg-gw4" className="text-[13px]">{t('network.gateway4')}</Label>
                    <Input
                      id="cfg-gw4"
                      value={configGateway4}
                      onChange={(e) => setConfigGateway4(e.target.value)}
                      placeholder="192.168.1.1"
                      className="pl-3 h-9 rounded-xl bg-secondary/50 border-0 text-[13px] font-mono"
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="cfg-gw6" className="text-[13px]">{t('network.gateway6')}</Label>
                    <Input
                      id="cfg-gw6"
                      value={configGateway6}
                      onChange={(e) => setConfigGateway6(e.target.value)}
                      placeholder="fe80::1"
                      className="pl-3 h-9 rounded-xl bg-secondary/50 border-0 text-[13px] font-mono"
                    />
                  </div>
                </div>

                <div className="space-y-2">
                  <Label htmlFor="cfg-dns" className="text-[13px]">{t('network.dnsServersLabel')}</Label>
                  <Input
                    id="cfg-dns"
                    value={configDns}
                    onChange={(e) => setConfigDns(e.target.value)}
                    placeholder="8.8.8.8, 1.1.1.1"
                    className="pl-3 h-9 rounded-xl bg-secondary/50 border-0 text-[13px] font-mono"
                  />
                </div>
              </>
            )}

            {/* MTU */}
            <div className="space-y-2">
              <Label htmlFor="cfg-mtu" className="text-[13px]">MTU</Label>
              <Input
                id="cfg-mtu"
                value={configMtu}
                onChange={(e) => setConfigMtu(e.target.value)}
                placeholder="1500"
                type="number"
                className="pl-3 h-9 rounded-xl bg-secondary/50 border-0 text-[13px] font-mono"
              />
              <p className="text-[11px] text-muted-foreground">{t('network.mtuHint')}</p>
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" className="rounded-xl" onClick={() => setConfigTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button className="rounded-xl" onClick={handleSaveConfig} disabled={configSaving}>
              {configSaving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t('common.save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Bond Create Dialog */}
      <BondCreateDialog
        open={bondCreateOpen}
        onOpenChange={setBondCreateOpen}
        availableSlaves={availableSlaves}
        onCreated={() => {
          setHasChanges(true)
          fetchData()
        }}
      />

      {/* Apply Config Warning Dialog */}
      <Dialog open={applyDialogOpen} onOpenChange={setApplyDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertTriangle className="h-5 w-5 text-warning" />
              {t('network.applyWarning')}
            </DialogTitle>
            <DialogDescription>
              {t('network.applyWarningDesc')}
            </DialogDescription>
          </DialogHeader>
          <div className="bg-warning/10 rounded-xl p-3">
            <p className="text-[13px] text-warning font-medium flex items-center gap-2">
              <AlertTriangle className="h-4 w-4 shrink-0" />
              {t('network.applyConfigCaution')}
            </p>
          </div>
          <DialogFooter>
            <Button variant="outline" className="rounded-xl" onClick={() => setApplyDialogOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button className="rounded-xl" onClick={handleApplyConfig} disabled={applying}>
              {applying && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t('network.applyConfig')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
