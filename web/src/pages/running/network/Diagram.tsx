import { createContext, useCallback, useContext, useId, useLayoutEffect, useRef, useState, type CSSProperties, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import {
  AlertTriangle, ArrowUpRight, Box, Cctv, ChevronDown, Cpu, Eye, Globe, HardDrive, Monitor, MonitorSmartphone, Network, Power, Printer, Router, Server,
  Smartphone, StickyNote, Tv, Wifi, type LucideIcon,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { addressParts, columnsFor, DEVICE_KINDS, deviceName, rowsOf, type DeviceLabel, type MapDevice, type MapPort, type Segment } from '@/lib/netmap'
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

/** What can be done to a device from its card. Each resolves to what went wrong, or null. */
export interface DeviceActions {
  /** Whether what is written is kept anywhere: some node of the network keeps a registry. */
  canEdit: boolean
  save: (mac: string, label: DeviceLabel) => Promise<string | null>
  ack: (mac: string) => Promise<string | null>
  forget: (mac: string) => Promise<string | null>
  /** Has a node send the device a Wake-on-LAN packet. */
  wake: (mac: string) => Promise<string | null>
}
const Actions = createContext<DeviceActions | null>(null)

type LabelKind = (typeof DEVICE_KINDS)[number]
type Kind = 'gateway' | 'server' | 'vm' | 'lxc' | LabelKind | 'device'

const isLabelKind = (kind: string | undefined): kind is LabelKind => !!kind && (DEVICE_KINDS as readonly string[]).includes(kind)

/** What a device is drawn as: what the network makes it first, then what the operator said, else a device. */
const kindOf = (d: MapDevice): Kind =>
  d.gateway ? 'gateway' : d.node ? 'server' : d.guest ? d.guest.kind : isLabelKind(d.label?.kind) ? d.label.kind : 'device'

const ICON: Record<Kind, LucideIcon> = {
  gateway: Router, server: Server, vm: Box, lxc: Box, router: Router, switch: Network, ap: Wifi, nas: HardDrive, pc: Monitor, phone: Smartphone,
  tv: Tv, camera: Cctv, printer: Printer, iot: Cpu, other: MonitorSmartphone, device: MonitorSmartphone,
}
const NETWORK_TONE = 'bg-warning/15 text-warning'
const SERVER_TONE = 'bg-[color-mix(in_srgb,var(--primary)_12%,var(--card))] text-primary'
const PLAIN_TONE = 'bg-secondary text-secondary-foreground'
const ICON_TONE: Record<Kind, string> = {
  gateway: NETWORK_TONE, router: NETWORK_TONE, switch: NETWORK_TONE, ap: NETWORK_TONE,
  server: SERVER_TONE,
  vm: PLAIN_TONE, lxc: PLAIN_TONE, nas: PLAIN_TONE, pc: PLAIN_TONE, phone: PLAIN_TONE, tv: PLAIN_TONE, camera: PLAIN_TONE, printer: PLAIN_TONE, iot: PLAIN_TONE, other: PLAIN_TONE,
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

const fieldClass = 'w-full rounded-lg border-0 bg-secondary px-2 text-base focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:text-[13px]'

/**
 * What the operator writes of a device, kept by its MAC: a name and what it
 * is — for a device neither the panel nor Proxmox names — and a note. A new
 * device can be marked as seen, and one the scan did not find forgotten.
 */
function LabelForm({ device, actions }: { device: MapDevice; actions: DeviceActions }) {
  const { t } = useTranslation()
  const id = useId()
  const namable = !device.node && !device.guest
  const [name, setName] = useState(device.label?.name ?? '')
  const [kind, setKind] = useState(device.label?.kind ?? '')
  const [note, setNote] = useState(device.label?.note ?? '')
  const [watch, setWatch] = useState(device.label?.watch ?? false)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState<string | null>(null)
  const [woken, setWoken] = useState(false)
  const changed = name.trim() !== (device.label?.name ?? '') || kind !== (device.label?.kind ?? '') || note.trim() !== (device.label?.note ?? '')
    || watch !== (device.label?.watch ?? false)

  const run = async (what: () => Promise<string | null>) => {
    setBusy(true)
    setProblem(null)
    setProblem(await what())
    setBusy(false)
  }
  const submit = (e: FormEvent) => {
    e.preventDefault()
    void run(() => actions.save(device.mac, { name: namable ? name.trim() : device.label?.name ?? '', kind: namable ? kind : device.label?.kind ?? '', note: note.trim(), watch }))
  }
  const wake = async () => {
    setBusy(true)
    setProblem(null)
    setWoken(false)
    const failed = await actions.wake(device.mac)
    setProblem(failed)
    setWoken(failed === null)
    setBusy(false)
  }
  return (
    <form onSubmit={submit} className="mt-2 space-y-1.5 border-t border-border/60 pt-2" data-testid="net-label-form">
      {namable && (
        <div className="grid grid-cols-[minmax(0,3fr)_minmax(0,2fr)] gap-1.5">
          <label htmlFor={`${id}-name`} className="text-xs text-muted-foreground">
            {t('running.network.edit.name')}
            <input id={`${id}-name`} value={name} onChange={(e) => setName(e.target.value)} maxLength={64} autoComplete="off" className={cn(fieldClass, 'mt-0.5 h-9 text-foreground')} />
          </label>
          <label htmlFor={`${id}-kind`} className="text-xs text-muted-foreground">
            {t('running.network.edit.kind')}
            <select id={`${id}-kind`} value={kind} onChange={(e) => setKind(e.target.value)} className={cn(fieldClass, 'mt-0.5 h-9 text-foreground')}>
              <option value="">{t('running.network.edit.kindNone')}</option>
              {DEVICE_KINDS.map((k) => <option key={k} value={k}>{t(`running.network.kind.${k}`)}</option>)}
            </select>
          </label>
        </div>
      )}
      <label htmlFor={`${id}-note`} className="block text-xs text-muted-foreground">
        {t('running.network.edit.note')}
        <textarea id={`${id}-note`} value={note} onChange={(e) => setNote(e.target.value)} maxLength={500} rows={2} className={cn(fieldClass, 'mt-0.5 py-1.5 text-foreground')} />
      </label>
      <label htmlFor={`${id}-watch`} className="flex min-h-9 cursor-pointer items-start gap-1.5 text-xs">
        <input id={`${id}-watch`} type="checkbox" checked={watch} onChange={(e) => setWatch(e.target.checked)} className="mt-0.5 size-4 shrink-0 accent-[var(--primary)]" aria-describedby={`${id}-watch-hint`} />
        <span>
          <span className="text-foreground">{t('running.network.edit.watch')}</span>
          <span id={`${id}-watch-hint`} className="block text-muted-foreground">{t('running.network.edit.watchHint')}</span>
        </span>
      </label>
      <div className="flex flex-wrap items-center gap-1.5">
        <Button type="submit" size="sm" className="rounded-lg" disabled={busy || !changed}>{t('running.network.edit.save')}</Button>
        {device.fresh && (
          <Button type="button" variant="outline" size="sm" className="rounded-lg" disabled={busy} onClick={() => void run(() => actions.ack(device.mac))}>
            {t('running.network.edit.ack')}
          </Button>
        )}
        {device.absent && (
          <Button type="button" variant="outline" size="sm" className="rounded-lg" disabled={busy} onClick={() => void run(() => actions.forget(device.mac))}>
            {t('running.network.edit.forget')}
          </Button>
        )}
        {/* What a registry has seen can be woken — not the node the scan was made from, which is plainly up. */}
        {device.firstSeen > 0 && !device.scanner && (
          <Button type="button" variant="outline" size="sm" className="rounded-lg" disabled={busy} onClick={() => void wake()}>
            <Power aria-hidden="true" />
            {t('running.network.edit.wake')}
          </Button>
        )}
      </div>
      {woken && <p role="status" className="text-xs text-muted-foreground">{t('running.network.edit.woken')}</p>}
      {problem && <p role="alert" className="break-words text-xs text-destructive">{t('running.network.edit.failed')} {problem === 'no-registry' ? t('running.network.edit.noRegistry') : problem}</p>}
    </form>
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
  const { t, i18n } = useTranslation()
  const actions = useContext(Actions)
  const kind = kindOf(device)
  const Icon = ICON[kind]
  const address = addressParts(device.ip, prefix)
  const name = deviceName(device)
  const when = (ms: number) => new Date(ms).toLocaleString(i18n.language, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
  const notes = [
    // A guest says what it is with its number, a plain device with nothing.
    kind !== 'vm' && kind !== 'lxc' && kind !== 'device' ? t(`running.network.kind.${kind}`) : '',
    device.scanner ? t('running.network.device.scanner') : '',
    device.guest
      ? nested
        ? t(`running.guest.${device.guest.kind}`, { vmid: device.guest.vmid })
        : t('running.network.device.guestOf', { host: device.guest.hostName, kind: t(`running.network.kind.${device.guest.kind}`), vmid: device.guest.vmid })
      : '',
    (name?.source === 'node' || name?.source === 'guest' || name?.source === 'label') && device.hostname ? device.hostname : '',
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
      data-conflict={device.conflict ? 'true' : undefined}
      data-absent={device.absent ? 'true' : undefined}
      data-fresh={device.fresh ? 'true' : undefined}
      className={cn(
        'h-full rounded-xl border p-2.5',
        // Not found by the scan: drawn from memory, and said so in words below — no letter is dimmed.
        device.absent ? 'border-dashed bg-secondary/40' : 'bg-card',
        device.conflict ? 'border-destructive' : kind === 'server' ? 'border-[color-mix(in_srgb,var(--primary)_35%,var(--border))]' : 'border-border',
      )}
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
          {device.gatewayFor.length > 0 && (
            <>
              <dt className="text-muted-foreground">{t('running.network.field.gatewayFor')}</dt>
              <dd className="break-words">{device.gatewayFor.join(', ')}</dd>
            </>
          )}
          {device.firstSeen > 0 && (
            <>
              <dt className="text-muted-foreground">{t('running.network.field.firstSeen')}</dt>
              <dd>{when(device.firstSeen)}</dd>
              <dt className="text-muted-foreground">{t('running.network.field.lastSeen')}</dt>
              <dd>{when(device.lastSeen)}</dd>
            </>
          )}
        </dl>
        {actions?.canEdit && device.mac && <LabelForm key={`${device.label?.name ?? ''}|${device.label?.kind ?? ''}|${device.label?.note ?? ''}|${device.label?.watch ?? false}`} device={device} actions={actions} />}
      </details>
      {(device.fresh || device.absent) && (
        <p className="mt-1.5 flex flex-wrap gap-1 text-xs">
          {device.fresh && <span className="rounded-md bg-[color-mix(in_srgb,var(--primary)_12%,var(--card))] px-1.5 py-0.5 font-medium text-primary">{t('running.network.device.fresh')}</span>}
          {device.absent && <span className="rounded-md bg-secondary px-1.5 py-0.5 text-secondary-foreground">{t('running.network.device.absent', { time: when(device.lastSeen) })}</span>}
        </p>
      )}
      {device.label?.watch && (
        <p className="mt-1.5 flex items-center gap-1 text-xs text-muted-foreground" data-testid="net-watch">
          <Eye className="size-3.5 shrink-0" aria-hidden="true" />
          {t('running.network.device.watched')}
        </p>
      )}
      {device.label?.note && (
        <p className="mt-1.5 flex items-start gap-1 text-xs text-muted-foreground" data-testid="net-note">
          <StickyNote className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          <span className="line-clamp-2 break-words">{device.label.note}</span>
        </p>
      )}
      {device.suspect.length > 0 && !device.conflict && (
        <p className="mt-1.5 flex items-start gap-1 text-xs font-medium text-warning">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          <span className="break-all">{t('running.network.device.suspect', { macs: device.suspect.join(', ') })}</span>
        </p>
      )}
      {device.conflict && (
        <p className="mt-1.5 flex items-start gap-1 text-xs font-medium text-destructive">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          <span className="break-words">{t('running.network.device.conflict', { node: device.conflict.name })}</span>
        </p>
      )}
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
export function Diagram({ segment, actions }: { segment: Segment; actions?: DeviceActions }) {
  const { t } = useTranslation()
  const flat = (list: MapDevice[]): MapDevice[] => list.flatMap((d) => [d, ...flat(d.children)])
  // Under the nodes hang their guests, which are not all nodes: each is counted as what it is.
  const under = flat(segment.servers)
  const nodes = under.filter((d) => d.node).length
  const guests = under.length - nodes
  const hung = segment.gear.length + segment.servers.length + segment.devices.length > 0
  const drawn = (
    <div className="nm" data-testid="net-diagram">
      <p className="inline-flex min-h-8 items-center gap-1.5 rounded-full bg-secondary px-3 text-xs font-medium text-secondary-foreground">
        <Globe className="size-3.5" aria-hidden="true" />
        {t('running.network.internet')}
      </p>
      <div className="nm-gateway flex flex-wrap items-stretch gap-[var(--nm-gap)]">
        {segment.gateways.length > 0
          ? segment.gateways.map((d) => (
            <div key={d.key} className="w-full max-w-[22rem]"><DeviceCard device={d} prefix={segment.prefix} portsChecked={segment.portsChecked} /></div>
          ))
          : (
            <p data-testid="net-no-gateway" className="w-full max-w-[22rem] rounded-xl border border-dashed border-border bg-card p-2.5 text-xs text-muted-foreground">
              {segment.gatewayAddress ? t('running.network.gatewaySilent', { address: segment.gatewayAddress }) : t('running.network.gatewayNone')}
            </p>
          )}
      </div>
      {hung && (
        <Hung
          prefix={segment.prefix}
          portsChecked={segment.portsChecked}
          groups={[
            { label: t('running.network.group.gear', { count: segment.gear.length }), devices: segment.gear },
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
  return <Actions.Provider value={actions ?? null}>{drawn}</Actions.Provider>
}
