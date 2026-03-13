import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Network as NetworkIcon,
  RefreshCw,
  Settings2,
  Wifi,
  Cable,
  Link2,
  Unlink,
  Loader2,
  Shield,
  Globe,
  Router,
  ArrowUpDown,
  Plus,
  Trash2,
  AlertTriangle,
  Container,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
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

import type { NetworkInterfaceInfo, InterfaceConfig, NetworkRoute } from '@/types/api'

interface DNSConfig {
  servers: string[]
}

const BOND_MODES = [
  'balance-rr',
  'active-backup',
  'balance-xor',
  'broadcast',
  '802.3ad',
  'balance-tlb',
  'balance-alb',
]

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

  // Data state
  const [interfaces, setInterfaces] = useState<NetworkInterfaceInfo[]>([])
  const [routes, setRoutes] = useState<NetworkRoute[]>([])
  const [dnsConfig, setDnsConfig] = useState<DNSConfig>({ servers: [] })
  const [loading, setLoading] = useState(true)
  const [hasChanges, setHasChanges] = useState(false)

  // Interface config dialog
  const [configTarget, setConfigTarget] = useState<NetworkInterfaceInfo | null>(null)
  const [configMode, setConfigMode] = useState<'dhcp' | 'static'>('dhcp')
  const [configAddresses, setConfigAddresses] = useState('')
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
  const [bondName, setBondName] = useState('')
  const [bondMode, setBondMode] = useState('active-backup')
  const [bondSlaves, setBondSlaves] = useState<string[]>([])
  const [bondCreating, setBondCreating] = useState(false)

  // Bond delete dialog
  const [bondDeleteTarget, setBondDeleteTarget] = useState<NetworkInterfaceInfo | null>(null)
  const [bondDeleting, setBondDeleting] = useState(false)

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
        gateway4: configMode === 'static' ? configGateway4.trim() : '',
        gateway6: configMode === 'static' ? configGateway6.trim() : '',
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

  // Create bond
  const handleCreateBond = async () => {
    if (!bondName.trim() || bondSlaves.length === 0) return
    setBondCreating(true)
    try {
      await api.createBond({ name: bondName.trim(), mode: bondMode, slaves: bondSlaves })
      toast.success(t('network.bondCreated', { name: bondName }))
      setBondCreateOpen(false)
      setBondName('')
      setBondMode('active-backup')
      setBondSlaves([])
      setHasChanges(true)
      await fetchData()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('network.bondCreateFailed')
      toast.error(message)
    } finally {
      setBondCreating(false)
    }
  }

  // Delete bond
  const handleDeleteBond = async () => {
    if (!bondDeleteTarget) return
    setBondDeleting(true)
    try {
      await api.deleteBond(bondDeleteTarget.name)
      toast.success(t('network.bondDeleted', { name: bondDeleteTarget.name }))
      setBondDeleteTarget(null)
      setHasChanges(true)
      await fetchData()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('network.bondDeleteFailed')
      toast.error(message)
    } finally {
      setBondDeleting(false)
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

  // Toggle bond slave selection
  const toggleBondSlave = (name: string) => {
    setBondSlaves((prev) =>
      prev.includes(name) ? prev.filter((s) => s !== name) : [...prev, name]
    )
  }

  // State indicator helpers
  const getStateStyle = (state: string) => {
    const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
    if (state === 'up') return `${base} bg-[#00c471]/10 text-[#00c471]`
    if (state === 'down') return `${base} bg-[#f04452]/10 text-[#f04452]`
    return `${base} bg-secondary text-muted-foreground`
  }

  const getStateDot = (state: string) => {
    if (state === 'up') return 'bg-[#00c471]'
    if (state === 'down') return 'bg-[#f04452]'
    return 'bg-muted-foreground'
  }

  // Interface type icon
  const getInterfaceIcon = (iface: NetworkInterfaceInfo) => {
    if (iface.type === 'loopback') return <Router className="h-4 w-4 text-muted-foreground" />
    if (iface.bond_info) return <Link2 className="h-4 w-4 text-[#3182f6]" />
    if (iface.type === 'wireless' || iface.name.startsWith('wl')) return <Wifi className="h-4 w-4 text-[#3182f6]" />
    if (iface.name.startsWith('docker') || iface.name.startsWith('br-') || iface.name.startsWith('veth'))
      return <Container className="h-4 w-4 text-[#3182f6]" />
    return <Cable className="h-4 w-4 text-[#3182f6]" />
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

  // Render interface card
  const renderInterfaceCard = (iface: NetworkInterfaceInfo) => {
    const isLoopback = iface.type === 'loopback'
    const ipv4 = iface.addresses.find((a) => a.family === 'ipv4' || a.family === 'inet')

    return (
      <div
        key={iface.name}
        className={`bg-card rounded-2xl p-5 card-shadow transition-all ${
          iface.is_default ? 'ring-1 ring-primary/30' : ''
        } ${isLoopback ? 'opacity-60' : ''}`}
      >
        {/* Header: name + state */}
        <div className="flex items-center justify-between mb-3">
          <div className="flex items-center gap-2">
            {getInterfaceIcon(iface)}
            <span className="text-[15px] font-semibold">{iface.name}</span>
            {iface.is_default && (
              <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#3182f6]/10 text-[#3182f6]">
                {t('network.defaultGateway')}
              </span>
            )}
            {iface.bond_info && (
              <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#f59e0b]/10 text-[#f59e0b]">
                Bond
              </span>
            )}
          </div>
          <div className="flex items-center gap-1.5">
            <span className={`h-2 w-2 rounded-full ${getStateDot(iface.state)}`} />
            <span className={getStateStyle(iface.state)}>
              {iface.state === 'up' ? t('network.up') : iface.state === 'down' ? t('network.down') : iface.state}
            </span>
          </div>
        </div>

        {/* IP Address */}
        <div className="space-y-1.5 mb-3">
          {ipv4 ? (
            <p className="text-[13px] font-mono">
              {ipv4.address}/{ipv4.prefix}
            </p>
          ) : (
            <p className="text-[13px] text-muted-foreground">{t('network.noAddresses')}</p>
          )}
          {iface.addresses
            .filter((a) => (a.family === 'ipv6' || a.family === 'inet6') && !a.address.startsWith('fe80'))
            .slice(0, 1)
            .map((a, idx) => (
              <p key={idx} className="text-[11px] text-muted-foreground font-mono truncate" title={`${a.address}/${a.prefix}`}>
                {a.address}/{a.prefix}
              </p>
            ))}
        </div>

        {/* Details */}
        <div className="space-y-1 text-[11px] text-muted-foreground">
          {iface.mac_address && iface.mac_address !== '00:00:00:00:00:00' && (
            <p>MAC: {iface.mac_address}</p>
          )}
          {iface.speed > 0 && (
            <p>{t('network.speed')}: {iface.speed >= 1000 ? `${iface.speed / 1000} Gbps` : `${iface.speed} Mbps`}</p>
          )}
          {iface.bond_info && (
            <p>{t('network.bondMode')}: {iface.bond_info.mode}</p>
          )}
        </div>

        {/* Traffic */}
        {!isLoopback && (
          <div className="flex items-center gap-4 mt-3 pt-3 border-t border-border">
            <div className="flex items-center gap-1">
              <ArrowUpDown className="h-3 w-3 text-muted-foreground" />
              <span className="text-[11px] text-muted-foreground">
                <span className="text-[#3182f6]">{formatBytes(iface.tx_bytes)}</span>
                {' / '}
                <span className="text-[#00c471]">{formatBytes(iface.rx_bytes)}</span>
              </span>
            </div>
            {(iface.tx_errors > 0 || iface.rx_errors > 0) && (
              <span className="text-[11px] text-[#f04452]">
                {t('network.errors')}: {iface.tx_errors + iface.rx_errors}
              </span>
            )}
          </div>
        )}

        {/* Config button */}
        {!isLoopback && (
          <div className="mt-3">
            <Button
              variant="outline"
              size="sm"
              className="w-full h-8 text-[12px] rounded-xl"
              onClick={() => openConfigDialog(iface)}
            >
              <Settings2 className="h-3.5 w-3.5" />
              {t('network.configure')}
            </Button>
          </div>
        )}
      </div>
    )
  }

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
                {classified.physical.map(renderInterfaceCard)}
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
                {classified.loopback.map(renderInterfaceCard)}
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
                {classified.virtual.map(renderInterfaceCard)}
              </div>
            </div>
          )}

          {/* Docker Interfaces — collapsible */}
          {classified.docker.length > 0 && (
            <div>
              <button
                className="flex items-center gap-2 mb-3 group"
                onClick={() => setDockerCollapsed(!dockerCollapsed)}
                aria-expanded={!dockerCollapsed}
                aria-controls="docker-interfaces"
              >
                <Container className="h-4 w-4 text-[#3182f6]" />
                <h2 className="text-[15px] font-semibold">Docker</h2>
                <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-[#3182f6]/10 text-[#3182f6]">
                  {classified.docker.length}
                </span>
                <span className="text-[11px] text-muted-foreground ml-1">
                  {dockerCollapsed ? '▸' : '▾'}
                </span>
              </button>
              {!dockerCollapsed && (
                <div id="docker-interfaces" className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                  {classified.docker.map(renderInterfaceCard)}
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
        <div className="bg-card rounded-2xl card-shadow overflow-hidden">
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
                        <Shield className="h-3.5 w-3.5 text-[#3182f6]" />
                        <span className="text-[#3182f6] font-medium">default</span>
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
                      <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#f59e0b]/10 text-[#f59e0b]">
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
                        className="text-[#f04452] hover:text-[#f04452]"
                        onClick={() => setBondDeleteTarget(bond)}
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
            <DialogTitle className="flex items-center gap-2">
              <Settings2 className="h-5 w-5" />
              {t('network.configureInterface')} — {configTarget?.name}
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
                  className={`flex-1 py-2 rounded-xl text-[13px] font-medium transition-all ${
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
                  className={`flex-1 py-2 rounded-xl text-[13px] font-medium transition-all ${
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
      <Dialog open={bondCreateOpen} onOpenChange={setBondCreateOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Link2 className="h-5 w-5" />
              {t('network.createBond')}
            </DialogTitle>
            <DialogDescription>
              {t('network.createBondDesc')}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="bond-name" className="text-[13px]">{t('network.bondName')}</Label>
              <Input
                id="bond-name"
                value={bondName}
                onChange={(e) => setBondName(e.target.value)}
                placeholder="bond0"
                className="pl-3 h-9 rounded-xl bg-secondary/50 border-0 text-[13px] font-mono"
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="bond-mode" className="text-[13px]">{t('network.bondMode')}</Label>
              <select
                id="bond-mode"
                value={bondMode}
                onChange={(e) => setBondMode(e.target.value)}
                className="flex h-9 w-full rounded-xl border-0 bg-secondary/50 px-3 py-1 text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/20"
              >
                {BOND_MODES.map((mode) => (
                  <option key={mode} value={mode}>
                    {mode}
                  </option>
                ))}
              </select>
            </div>

            <div className="space-y-2">
              <Label className="text-[13px]">{t('network.bondSlaves')}</Label>
              {availableSlaves.length === 0 ? (
                <p className="text-[13px] text-muted-foreground">{t('network.noAvailableSlaves')}</p>
              ) : (
                <div className="space-y-1">
                  {availableSlaves.map((iface) => (
                    <label
                      key={iface.name}
                      className={`flex items-center gap-3 px-3 py-2 rounded-xl cursor-pointer transition-all ${
                        bondSlaves.includes(iface.name)
                          ? 'bg-primary/10 text-primary'
                          : 'bg-secondary/50 text-foreground hover:bg-secondary'
                      }`}
                    >
                      <input
                        type="checkbox"
                        checked={bondSlaves.includes(iface.name)}
                        onChange={() => toggleBondSlave(iface.name)}
                        className="rounded"
                      />
                      <span className="text-[13px] font-medium">{iface.name}</span>
                      <span className={getStateStyle(iface.state)}>
                        {iface.state}
                      </span>
                    </label>
                  ))}
                </div>
              )}
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" className="rounded-xl" onClick={() => setBondCreateOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button
              className="rounded-xl"
              onClick={handleCreateBond}
              disabled={bondCreating || !bondName.trim() || bondSlaves.length === 0}
            >
              {bondCreating && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Bond Delete Dialog */}
      <Dialog open={!!bondDeleteTarget} onOpenChange={(open) => !open && setBondDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('network.deleteBondTitle')}</DialogTitle>
            <DialogDescription>
              {t('network.deleteBondConfirm', { name: bondDeleteTarget?.name })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" className="rounded-xl" onClick={() => setBondDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" className="rounded-xl" onClick={handleDeleteBond} disabled={bondDeleting}>
              {bondDeleting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Apply Config Warning Dialog */}
      <Dialog open={applyDialogOpen} onOpenChange={setApplyDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertTriangle className="h-5 w-5 text-[#f59e0b]" />
              {t('network.applyWarning')}
            </DialogTitle>
            <DialogDescription>
              {t('network.applyWarningDesc')}
            </DialogDescription>
          </DialogHeader>
          <div className="bg-[#f59e0b]/10 rounded-xl p-3">
            <p className="text-[13px] text-[#f59e0b] font-medium flex items-center gap-2">
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
