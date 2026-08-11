import { useTranslation } from 'react-i18next'
import { Wifi, Cable, Link2, Router, ArrowUpDown, Settings2, Container } from 'lucide-react'
import { formatBytes } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import type { NetworkInterfaceInfo } from '@/types/api'

// State-badge styling shared by the interface cards, the page's bond table and
// BondCreateDialog — co-located with its main consumer.
// eslint-disable-next-line react-refresh/only-export-components
export function getStateStyle(state: string) {
  const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
  if (state === 'up') return `${base} bg-success/10 text-success`
  if (state === 'down') return `${base} bg-destructive/10 text-destructive`
  return `${base} bg-secondary text-muted-foreground`
}

function getStateDot(state: string) {
  if (state === 'up') return 'bg-success'
  if (state === 'down') return 'bg-destructive'
  return 'bg-muted-foreground'
}

function getInterfaceIcon(iface: NetworkInterfaceInfo) {
  if (iface.type === 'loopback') return <Router className="h-4 w-4 text-muted-foreground" />
  if (iface.bond_info) return <Link2 className="h-4 w-4 text-primary" />
  if (iface.type === 'wireless' || iface.name.startsWith('wl')) return <Wifi className="h-4 w-4 text-primary" />
  if (iface.name.startsWith('docker') || iface.name.startsWith('br-') || iface.name.startsWith('veth'))
    return <Container className="h-4 w-4 text-primary" />
  return <Cable className="h-4 w-4 text-primary" />
}

/** Display-only card for a single network interface. */
export function InterfaceCard({
  iface,
  onConfigure,
}: {
  iface: NetworkInterfaceInfo
  onConfigure: (iface: NetworkInterfaceInfo) => void
}) {
  const { t } = useTranslation()
  const isLoopback = iface.type === 'loopback'
  const ipv4 = iface.addresses.find((a) => a.family === 'ipv4' || a.family === 'inet')

  return (
    <div
      className={`bg-card rounded-2xl p-5 card-shadow transition-all ${
        iface.is_default ? 'ring-1 ring-primary/30' : ''
      } ${isLoopback ? 'opacity-60' : ''}`}
    >
      {/* Header: name + state */}
      <div className="flex items-center justify-between gap-2 mb-3">
        <div className="flex items-center gap-2 min-w-0">
          {getInterfaceIcon(iface)}
          <span className="text-[15px] font-semibold truncate min-w-0" title={iface.name}>{iface.name}</span>
          {iface.is_default && (
            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-primary/10 text-primary shrink-0">
              {t('network.defaultGateway')}
            </span>
          )}
          {iface.bond_info && (
            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-warning/10 text-warning shrink-0">
              Bond
            </span>
          )}
        </div>
        <div className="flex items-center gap-1.5 shrink-0">
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
              <span className="text-primary">{formatBytes(iface.tx_bytes)}</span>
              {' / '}
              <span className="text-success">{formatBytes(iface.rx_bytes)}</span>
            </span>
          </div>
          {(iface.tx_errors > 0 || iface.rx_errors > 0) && (
            <span className="text-[11px] text-destructive">
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
            onClick={() => onConfigure(iface)}
          >
            <Settings2 className="h-3.5 w-3.5" />
            {t('network.configure')}
          </Button>
        </div>
      )}
    </div>
  )
}
