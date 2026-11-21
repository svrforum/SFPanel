import { useCallback, useLayoutEffect, useRef, useState, type CSSProperties } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowUpRight, Box, ChevronDown, Globe, MonitorSmartphone, Router, Server, type LucideIcon } from 'lucide-react'
import { addressParts, columnsFor, deviceName, rowsOf, type MapDevice, type MapPort, type Segment } from '@/lib/netmap'
import { cn } from '@/lib/utils'
import './network.css'

/** The narrowest a device's card is drawn; a row holds as many as fit. */
const CARD_MIN = 196
/** Ports shown on a card before the rest are counted. */
const PORTS_SHOWN = 6

/**
 * How many cards fit a row of the element the returned ref is put on, kept as
 * the element is resized. The gap is the stylesheet's own (--nm-gap).
 */
function useColumns(): [(el: HTMLElement | null) => void, number] {
  const [columns, setColumns] = useState(1)
  const observer = useRef<ResizeObserver | null>(null)
  const ref = useCallback((el: HTMLElement | null) => {
    observer.current?.disconnect()
    observer.current = null
    if (!el) return
    const measure = () => {
      const gap = parseFloat(getComputedStyle(el).getPropertyValue('--nm-gap')) || 10
      setColumns(columnsFor(el.clientWidth, CARD_MIN, gap))
    }
    measure()
    observer.current = new ResizeObserver(measure)
    observer.current.observe(el)
  }, [])
  useLayoutEffect(() => () => observer.current?.disconnect(), [])
  return [ref, columns]
}

type Kind = 'gateway' | 'server' | 'vm' | 'lxc' | 'device'

const kindOf = (d: MapDevice): Kind => (d.gateway ? 'gateway' : d.node ? 'server' : d.guest ? d.guest.kind : 'device')

const ICON: Record<Kind, LucideIcon> = { gateway: Router, server: Server, vm: Box, lxc: Box, device: MonitorSmartphone }
const ICON_TONE: Record<Kind, string> = {
  gateway: 'bg-warning/15 text-warning',
  server: 'bg-[color-mix(in_srgb,var(--primary)_12%,var(--card))] text-primary',
  vm: 'bg-secondary text-secondary-foreground',
  lxc: 'bg-secondary text-secondary-foreground',
  device: 'bg-secondary text-muted-foreground',
}

const chipBase = 'inline-flex min-h-8 items-center gap-1 rounded-lg px-2 text-xs'

function Port({ port }: { port: MapPort }) {
  const { t } = useTranslation()
  const number = port.proto === 'tcp' ? String(port.port) : `${port.port}/${port.proto}`
  if (port.href) {
    return (
      <a
        href={port.href}
        target="_blank"
        rel="noopener noreferrer"
        className={cn(chipBase, 'bg-[color-mix(in_srgb,var(--primary)_10%,var(--card))] font-semibold text-primary hover:bg-[color-mix(in_srgb,var(--primary)_15%,var(--card))] hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring')}
      >
        {number}
        <ArrowUpRight className="size-3.5" aria-hidden="true" />
        <span className="sr-only"> ({t('running.port.newTab')})</span>
      </a>
    )
  }
  return (
    <span className={cn(chipBase, 'bg-secondary text-muted-foreground')}>
      {number}
      {port.name && <span className="font-medium text-secondary-foreground">{port.name}</span>}
    </span>
  )
}

/**
 * One device: its address first — the part that tells it from its neighbours
 * in full weight — then what it is called, and what is open on it. The top of
 * the card opens the rest (MAC, maker, every port).
 */
export function DeviceCard({ device, prefix, portsChecked, nested }: {
  device: MapDevice
  prefix: number
  portsChecked: boolean
  /** Drawn inside its host: which host need not be said again. */
  nested?: boolean
}) {
  const { t } = useTranslation()
  const kind = kindOf(device)
  const Icon = ICON[kind]
  const address = addressParts(device.ip, prefix)
  const name = deviceName(device)
  const notes = [
    kind === 'gateway' || kind === 'server' ? t(`running.network.kind.${kind}`) : '',
    device.scanner ? t('running.network.device.scanner') : '',
    device.guest
      ? nested
        ? t(`running.guest.${device.guest.kind}`, { vmid: device.guest.vmid })
        : t('running.network.device.guestOf', { host: device.guest.hostName, kind: t(`running.network.kind.${device.guest.kind}`), vmid: device.guest.vmid })
      : '',
    (name?.source === 'node' || name?.source === 'guest') && device.hostname ? device.hostname : '',
    // Who made the network card says something of a device nothing else
    // names; of a node or a guest it is the hypervisor's name, every time.
    !device.node && !device.guest && name?.source !== 'vendor' ? device.vendor : '',
  ].filter(Boolean)
  const shown = device.ports.length > PORTS_SHOWN ? device.ports.slice(0, PORTS_SHOWN - 1) : device.ports
  const more = device.ports.length - shown.length
  const allPorts = device.timedOut
    ? t('running.network.device.timedOut')
    : !portsChecked
      ? t('running.network.device.notChecked')
      : device.ports.length === 0
        ? t('running.network.device.noPorts')
        : device.ports.map((p) => (p.proto === 'tcp' ? p.port : `${p.port}/${p.proto}`)).join(', ')
  return (
    <article
      data-testid="net-device"
      data-ip={device.ip}
      data-kind={kind}
      className={cn('h-full rounded-xl border bg-card p-2.5', kind === 'server' ? 'border-[color-mix(in_srgb,var(--primary)_35%,var(--border))]' : 'border-border')}
    >
      <details className="group">
        <summary className="relative flex cursor-pointer list-none items-start gap-2.5 rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
          <span className={cn('grid size-8 shrink-0 place-items-center rounded-lg', ICON_TONE[kind])}>
            <Icon className="size-4" aria-hidden="true" />
          </span>
          <span className="min-w-0 flex-1">
            {/* Room for the arrow in the corner, on this line only: a MAC below needs the whole width. */}
            <span className="block break-all pr-5 font-mono text-sm leading-5 tabular-nums">
              <span className="text-muted-foreground">{address.network}</span>
              <span className="font-bold text-foreground">{address.host}</span>
            </span>
            <span className={cn('block break-words text-[13px] leading-5', name ? 'font-semibold' : 'text-muted-foreground')}>
              {name?.text ?? t('running.network.device.unnamed')}
            </span>
            {(notes.length > 0 || device.mac) && (
              <span className={cn('block break-words text-xs leading-4 text-muted-foreground', notes.length === 0 && 'font-mono')}>
                {notes.join(' · ') || device.mac}
              </span>
            )}
          </span>
          <span className="absolute right-0 top-0.5 text-muted-foreground">
            <ChevronDown className="size-4 transition-transform group-open:rotate-180 motion-reduce:transition-none" aria-hidden="true" />
            <span className="sr-only">{t('running.network.device.details')}</span>
          </span>
        </summary>
        <dl className="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-xs">
          <dt className="text-muted-foreground">{t('running.network.field.address')}</dt>
          <dd className="break-all font-mono">{device.ip}</dd>
          <dt className="text-muted-foreground">{t('running.network.field.mac')}</dt>
          <dd className="break-all font-mono">{device.mac || '—'}</dd>
          <dt className="text-muted-foreground">{t('running.network.field.vendor')}</dt>
          <dd className="break-words">{device.vendor || '—'}</dd>
          <dt className="text-muted-foreground">{t('running.network.field.hostname')}</dt>
          <dd className="break-all">{device.hostname || '—'}</dd>
          <dt className="text-muted-foreground">{t('running.network.field.ports')}</dt>
          <dd className="break-words">{allPorts}</dd>
        </dl>
      </details>
      {device.timedOut && <p className="mt-1.5 text-xs text-muted-foreground">{t('running.network.device.timedOut')}</p>}
      {shown.length > 0 && (
        <ul aria-label={t('running.network.device.ports')} className="mt-1.5 flex flex-wrap gap-1">
          {shown.map((p) => <li key={`${p.port}/${p.proto}`}><Port port={p} /></li>)}
          {more > 0 && <li><span className={cn(chipBase, 'text-muted-foreground')}>{t('running.network.device.morePorts', { count: more })}</span></li>}
        </ul>
      )}
    </article>
  )
}

interface Group {
  /** The words over the group, where it has any. */
  label?: string
  devices: MapDevice[]
}

/**
 * Groups of devices hung from one trunk: a device with guests takes a block
 * of its own with the guests hung under it, the rest stand in rows.
 */
function Hung({ groups, prefix, portsChecked, nested }: { groups: Group[]; prefix: number; portsChecked: boolean; nested?: boolean }) {
  const [sizer, columns] = useColumns()
  return (
    <div className="nm-rows" style={{ '--nm-cols': columns } as CSSProperties}>
      <div ref={sizer} aria-hidden="true" className="h-0" />
      {/* Flat under the trunk: the stylesheet ends the trunk at the last block. */}
      {groups.filter((g) => g.devices.length > 0).flatMap((group, index) => {
        const id = group.label ?? String(index)
        const blocks = group.label ? [<p key={`${id}/label`} className="nm-block nm-label pb-0.5 text-xs font-semibold text-muted-foreground">{group.label}</p>] : []
        // In the order of the addresses: a device with guests breaks the row
        // it would have stood in and takes a block of its own.
        let run: MapDevice[] = []
        const flush = () => {
          for (const row of rowsOf(run, columns)) {
            blocks.push(
              <ul key={`${id}/row/${row[0].key}`} className="nm-block nm-row">
                {row.map((d) => <li key={d.key} className="nm-cell"><DeviceCard device={d} prefix={prefix} portsChecked={portsChecked} nested={nested} /></li>)}
              </ul>,
            )
          }
          run = []
        }
        for (const d of group.devices) {
          if (d.children.length === 0) {
            run.push(d)
            continue
          }
          flush()
          blocks.push(
            <ul key={`${id}/host/${d.key}`} className="nm-block nm-row" style={{ '--nm-cols': 1 } as CSSProperties}>
              <li className="nm-cell">
                <div data-testid="net-host" className="rounded-2xl bg-secondary/60 p-2">
                  <div className="max-w-[22rem]"><DeviceCard device={d} prefix={prefix} portsChecked={portsChecked} nested={nested} /></div>
                  <Hung groups={[{ devices: d.children }]} prefix={prefix} portsChecked={portsChecked} nested />
                </div>
              </li>
            </ul>,
          )
        }
        flush()
        return blocks
      })}
    </div>
  )
}

/**
 * A scanned network as a diagram: where it leaves (the gateway) on top, this
 * panel's nodes under it with their guests inside them, then every other
 * device, each list by address. Laid out by the page — nothing to drag or
 * zoom — so the letters keep their size on any screen.
 */
export function Diagram({ segment }: { segment: Segment }) {
  const { t } = useTranslation()
  const flat = (list: MapDevice[]): MapDevice[] => list.flatMap((d) => [d, ...flat(d.children)])
  // Under the nodes hang their guests, which are not all nodes: each is counted as what it is.
  const under = flat(segment.servers)
  const nodes = under.filter((d) => d.node).length
  const guests = under.length - nodes
  const hung = segment.servers.length + segment.devices.length > 0
  return (
    <div className="nm" data-testid="net-diagram">
      <p className="inline-flex min-h-8 items-center gap-1.5 rounded-full bg-secondary px-3 text-xs font-medium text-secondary-foreground">
        <Globe className="size-3.5" aria-hidden="true" />
        {t('running.network.internet')}
      </p>
      <div className="nm-gateway max-w-[22rem]">
        {segment.gateway
          ? <DeviceCard device={segment.gateway} prefix={segment.prefix} portsChecked={segment.portsChecked} />
          : (
            <p data-testid="net-no-gateway" className="rounded-xl border border-dashed border-border bg-card p-2.5 text-xs text-muted-foreground">
              {segment.gatewayAddress ? t('running.network.gatewaySilent', { address: segment.gatewayAddress }) : t('running.network.gatewayNone')}
            </p>
          )}
      </div>
      {hung && (
        <Hung
          prefix={segment.prefix}
          portsChecked={segment.portsChecked}
          groups={[
            {
              label: guests > 0 ? `${t('running.network.group.servers', { count: nodes })} · ${t('running.groups.guests', { count: guests })}` : t('running.network.group.servers', { count: nodes }),
              devices: segment.servers,
            },
            { label: t('running.network.group.devices', { count: flat(segment.devices).length }), devices: segment.devices },
          ]}
        />
      )}
    </div>
  )
}
