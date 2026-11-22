import { Fragment, useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronDown, ChevronUp, Pencil, Plus, Server, Trash2, Type } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useConfirm } from '@/components/ConfirmDialog'
import { deviceName, type MapDevice } from '@/lib/netmap'
import {
  fits, gridRows, MAX_NAME, MAX_UNITS, minUnits, move, newId, place, placed, rackProblem, remove, resize, roomAt, slots,
  type Port, type Rack, type RackItem,
} from '@/lib/rack'
import { cn } from '@/lib/utils'
import { Heading, type HeadingLevel } from '../Heading'
import { ICON, ICON_TONE, kindOf } from '../network/kinds'
import type { BoardSection } from '../useRunningBoard'
import { NO_RACK_NODE, useRackView, type RackModel, type RackNode } from './useRackView'

const fieldClass = 'h-9 rounded-lg border-0 bg-secondary px-2 text-base text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:text-[13px]'

/** What a unit of a rack holds, found among what the panel knows. */
type Held =
  | { kind: 'node'; node: RackNode | null; id: string }
  | { kind: 'device'; device: MapDevice | null; mac: string }
  | { kind: 'text'; text: string }

function held(item: RackItem, model: RackModel): Held {
  if (item.kind === 'node') return { kind: 'node', id: item.node ?? '', node: model.nodes.find((n) => n.id === item.node) ?? null }
  if (item.kind === 'device') {
    const mac = (item.mac ?? '').toLowerCase()
    return { kind: 'device', mac, device: model.devices.get(mac) ?? null }
  }
  return { kind: 'text', text: item.text ?? '' }
}

/** How many ports are drawn one by one, and up to how many each says its speed. */
const LAMPS = 8
const LAMPS_WITH_SPEED = 4

/**
 * A node's sockets as small lamps: filled where the link is up, hollow where
 * it is not, with the link's speed beside it while there is room. Said in
 * words too, for who cannot see them.
 */
function Lamps({ ports }: { ports: Port[] }) {
  const { t } = useTranslation()
  if (ports.length === 0) return null
  const speeds = ports.length <= LAMPS_WITH_SPEED
  return (
    <span role="img" aria-label={t('running.rack.ports', { up: ports.filter((p) => p.up).length, total: ports.length })} className="flex shrink-0 items-center gap-1">
      {ports.slice(0, LAMPS).map((p) => (
        <span
          key={p.name}
          data-testid="rack-port"
          data-up={p.up}
          title={`${p.name}${p.speed ? ` ${p.speed}` : ''} · ${t(p.up ? 'running.rack.portUp' : 'running.rack.portDown')}`}
          className="inline-flex h-5 items-center gap-1 rounded-[5px] border border-border bg-secondary/60 px-1 font-mono text-xs text-secondary-foreground"
        >
          <span className={cn('size-2 rounded-full', p.up ? 'bg-success' : 'border border-muted-foreground')} />
          {speeds && p.up && p.speed}
        </span>
      ))}
      {ports.length > LAMPS && <span className="text-xs text-muted-foreground">+{ports.length - LAMPS}</span>}
    </span>
  )
}

/** What a unit says of the thing in it: a mark of what it is, its name, and how it is doing. */
function Face({ what, tall }: { what: Held; tall: boolean }) {
  const { t } = useTranslation()
  if (what.kind === 'text') {
    return (
      <>
        <span className="grid size-6 shrink-0 place-items-center rounded-md bg-secondary text-muted-foreground"><Type className="size-3.5" aria-hidden="true" /></span>
        <span className="min-w-0 flex-1 truncate text-[13px] text-secondary-foreground">{what.text}</span>
      </>
    )
  }
  if (what.kind === 'node') {
    const node = what.node
    return (
      <>
        <span className="grid size-6 shrink-0 place-items-center rounded-md bg-[color-mix(in_srgb,var(--primary)_12%,var(--card))] text-primary"><Server className="size-3.5" aria-hidden="true" /></span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[13px] font-semibold">{node?.name ?? t('running.rack.unknownNode')}</span>
          {tall && node?.address && <span className="block truncate font-mono text-xs text-muted-foreground">{node.address}</span>}
        </span>
        {node && !node.online
          ? <span className="shrink-0 text-xs font-medium text-destructive">{t('running.rack.offline')}</span>
          : node?.ports && <Lamps ports={node.ports} />}
      </>
    )
  }
  const d = what.device
  const kind = d ? kindOf(d) : 'device'
  const Icon = ICON[kind]
  return (
    <>
      <span className={cn('grid size-6 shrink-0 place-items-center rounded-md', ICON_TONE[kind])}><Icon className="size-3.5" aria-hidden="true" /></span>
      <span className="min-w-0 flex-1">
        <span className={cn('block truncate text-[13px]', d ? 'font-semibold' : 'font-mono text-muted-foreground')}>{d ? deviceName(d)?.text ?? d.ip : what.mac}</span>
        {tall && d && <span className="block truncate font-mono text-xs text-muted-foreground">{d.ip}</span>}
      </span>
      {(!d || d.absent) && <span className="shrink-0 text-xs text-muted-foreground">{t(d ? 'running.rack.absent' : 'running.rack.unknownDevice')}</span>}
    </>
  )
}

function nameOf(what: Held, t: (key: string) => string): string {
  if (what.kind === 'text') return what.text
  if (what.kind === 'node') return what.node?.name ?? t('running.rack.unknownNode')
  return what.device ? deviceName(what.device)?.text ?? what.device.ip : what.mac
}

/** Putting a thing on a free unit: a server, a device of the registry, or words. */
function AddForm({ rack, unit, model, racks, onPut, onCancel }: {
  rack: Rack
  unit: number
  model: RackModel
  racks: Rack[]
  onPut: (item: RackItem) => void
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const taken = placed(racks)
  const nodes = model.nodes.filter((n) => !taken.nodes.has(n.id))
  const devices = [...model.devices.values()].filter((d) => !d.node && !taken.macs.has(d.mac))
  const [kind, setKind] = useState<RackItem['kind']>(nodes.length > 0 ? 'node' : devices.length > 0 ? 'device' : 'text')
  const [node, setNode] = useState(nodes[0]?.id ?? '')
  const [mac, setMac] = useState(devices[0]?.mac ?? '')
  const [text, setText] = useState('')
  const room = roomAt(rack, unit)
  const [height, setHeight] = useState(1)
  const ready = kind === 'node' ? node !== '' : kind === 'device' ? mac !== '' : text.trim() !== ''
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!ready) return
    onPut({
      id: newId(), u: unit, height, kind,
      ...(kind === 'node' ? { node } : kind === 'device' ? { mac } : { text: text.trim() }),
    })
  }
  return (
    <form onSubmit={submit} data-testid="rack-add-form" className="my-1 space-y-2 rounded-xl border border-primary/40 bg-card p-3 text-[13px]">
      <p className="font-semibold">{t('running.rack.addAt', { unit })}</p>
      <fieldset className="flex flex-wrap gap-x-4 gap-y-1">
        <legend className="sr-only">{t('running.rack.what')}</legend>
        {(['node', 'device', 'text'] as const).map((k) => (
          <label key={k} htmlFor={`${id}-kind-${k}`} className="flex min-h-9 cursor-pointer items-center gap-1.5">
            <input id={`${id}-kind-${k}`} type="radio" name={`${id}-kind`} checked={kind === k} onChange={() => setKind(k)} className="size-4 accent-[var(--primary)]" />
            {t(`running.rack.kind.${k}`)}
          </label>
        ))}
      </fieldset>
      {kind === 'node' && (nodes.length === 0
        ? <p className="text-muted-foreground">{t('running.rack.noNodes')}</p>
        : (
          <label htmlFor={`${id}-node`} className="block text-xs text-muted-foreground">
            {t('running.rack.pickNode')}
            <select id={`${id}-node`} value={node} onChange={(e) => setNode(e.target.value)} className={cn(fieldClass, 'mt-0.5 block w-full')}>
              {nodes.map((n) => <option key={n.id} value={n.id}>{n.name}</option>)}
            </select>
          </label>
        ))}
      {kind === 'device' && (devices.length === 0
        ? <p className="text-muted-foreground">{t('running.rack.noDevices')}</p>
        : (
          <label htmlFor={`${id}-mac`} className="block text-xs text-muted-foreground">
            {t('running.rack.pickDevice')}
            <select id={`${id}-mac`} value={mac} onChange={(e) => setMac(e.target.value)} className={cn(fieldClass, 'mt-0.5 block w-full')}>
              {devices.map((d) => <option key={d.mac} value={d.mac}>{`${deviceName(d)?.text ?? d.mac} · ${d.ip}`}</option>)}
            </select>
          </label>
        ))}
      {kind === 'text' && (
        <label htmlFor={`${id}-text`} className="block text-xs text-muted-foreground">
          {t('running.rack.textLabel')}
          <input id={`${id}-text`} value={text} onChange={(e) => setText(e.target.value)} maxLength={MAX_NAME} autoComplete="off" placeholder={t('running.rack.textPlaceholder')} className={cn(fieldClass, 'mt-0.5 block w-full')} />
        </label>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor={`${id}-height`} className="flex items-center gap-1.5 text-xs text-muted-foreground">
          {t('running.rack.height')}
          <select id={`${id}-height`} value={height} onChange={(e) => setHeight(Number(e.target.value))} className={fieldClass}>
            {Array.from({ length: room }, (_, i) => i + 1).map((h) => <option key={h} value={h}>{t('running.rack.heightUnits', { count: h })}</option>)}
          </select>
        </label>
        <Button type="submit" size="sm" className="rounded-lg" disabled={!ready}>{t('running.rack.put')}</Button>
        <Button type="button" variant="ghost" size="sm" className="rounded-lg" onClick={onCancel}>{t('common.cancel')}</Button>
      </div>
    </form>
  )
}

/** What can be done with the thing that is chosen: moved a unit, made taller or shorter, taken out. */
function ItemPanel({ rack, item, what, onChange, onClose }: {
  rack: Rack
  item: RackItem
  what: Held
  onChange: (next: Rack) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const room = roomAt(rack, item.u, item.id)
  return (
    <div role="group" aria-label={nameOf(what, t)} data-testid="rack-item-panel" className="my-1 space-y-2 rounded-xl border border-primary/40 bg-card p-3 text-[13px]">
      <p className="break-words font-semibold">
        {nameOf(what, t)}
        <span className="ml-2 font-normal text-muted-foreground">{t('running.rack.at', { from: item.u, to: item.u + item.height - 1 })}</span>
      </p>
      {what.kind === 'node' && what.node && (
        <ul className="space-y-0.5 text-xs text-muted-foreground" aria-label={t('running.rack.portList')}>
          {what.node.address && <li className="font-mono">{what.node.address}</li>}
          {(what.node.ports ?? []).map((p) => (
            <li key={p.name}><span className="font-mono text-foreground">{p.name}</span> {p.speed} · {t(p.up ? 'running.rack.portUp' : 'running.rack.portDown')}</li>
          ))}
        </ul>
      )}
      {what.kind === 'device' && (
        <p className="break-all font-mono text-xs text-muted-foreground">{what.device ? `${what.device.ip} · ${what.mac}` : what.mac}</p>
      )}
      <div className="flex flex-wrap items-center gap-1.5">
        <Button type="button" variant="outline" size="sm" className="rounded-lg" disabled={!fits(rack, item.u + 1, item.height, item.id)} onClick={() => { const next = move(rack, item.id, 1); if (next) onChange(next) }}>
          <ChevronUp aria-hidden="true" />
          {t('running.rack.up')}
        </Button>
        <Button type="button" variant="outline" size="sm" className="rounded-lg" disabled={!fits(rack, item.u - 1, item.height, item.id)} onClick={() => { const next = move(rack, item.id, -1); if (next) onChange(next) }}>
          <ChevronDown aria-hidden="true" />
          {t('running.rack.down')}
        </Button>
        <label htmlFor={`${id}-height`} className="flex items-center gap-1.5 text-xs text-muted-foreground">
          {t('running.rack.height')}
          <select id={`${id}-height`} value={item.height} onChange={(e) => { const next = resize(rack, item.id, Number(e.target.value)); if (next) onChange(next) }} className={fieldClass}>
            {Array.from({ length: Math.max(room, item.height) }, (_, i) => i + 1).map((h) => <option key={h} value={h}>{t('running.rack.heightUnits', { count: h })}</option>)}
          </select>
        </label>
        <Button type="button" variant="outline" size="sm" className="rounded-lg" onClick={() => { onChange(remove(rack, item.id)); onClose() }}>
          <Trash2 aria-hidden="true" />
          {t('running.rack.takeOut')}
        </Button>
        <Button type="button" variant="ghost" size="sm" className="rounded-lg" onClick={onClose}>{t('common.close')}</Button>
      </div>
    </div>
  )
}

/** A rack's own name and height, and its deletion. */
function RackSettings({ rack, onSave, onDelete, onCancel }: { rack: Rack; onSave: (name: string, units: number) => void; onDelete: () => void; onCancel: () => void }) {
  const { t } = useTranslation()
  const id = useId()
  const [name, setName] = useState(rack.name)
  const [units, setUnits] = useState(rack.units)
  const least = minUnits(rack)
  const problem = rackProblem(name, units, 0) ?? (units < least ? 'tooShort' : null)
  return (
    <form
      onSubmit={(e) => { e.preventDefault(); if (!problem) onSave(name.trim(), units) }}
      data-testid="rack-settings"
      className="mt-3 space-y-2 rounded-xl bg-secondary/60 p-3 text-[13px]"
    >
      <div className="grid grid-cols-[minmax(0,1fr)_6rem] gap-2">
        <label htmlFor={`${id}-name`} className="text-xs text-muted-foreground">
          {t('running.rack.name')}
          <input id={`${id}-name`} value={name} onChange={(e) => setName(e.target.value)} maxLength={MAX_NAME} autoComplete="off" className={cn(fieldClass, 'mt-0.5 block w-full')} />
        </label>
        <label htmlFor={`${id}-units`} className="text-xs text-muted-foreground">
          {t('running.rack.units')}
          <input id={`${id}-units`} type="number" min={least} max={MAX_UNITS} value={units} onChange={(e) => setUnits(Number(e.target.value))} className={cn(fieldClass, 'mt-0.5 block w-full')} />
        </label>
      </div>
      {problem && <p className="text-xs text-destructive">{t(`running.rack.problem.${problem}`, { min: least })}</p>}
      <div className="flex flex-wrap items-center gap-1.5">
        <Button type="submit" size="sm" className="rounded-lg" disabled={!!problem}>{t('running.rack.save')}</Button>
        <Button type="button" variant="ghost" size="sm" className="rounded-lg" onClick={onCancel}>{t('common.cancel')}</Button>
        <Button type="button" variant="outline" size="sm" className="ml-auto rounded-lg text-destructive" onClick={onDelete}>
          <Trash2 aria-hidden="true" />
          {t('running.rack.delete')}
        </Button>
      </div>
    </form>
  )
}

function RackCard({ rack, racks, model, level }: { rack: Rack; racks: Rack[]; model: RackModel; level: HeadingLevel }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [chosen, setChosen] = useState<string | null>(null)
  const [adding, setAdding] = useState<number | null>(null)
  const [editing, setEditing] = useState(false)
  const [problem, setProblem] = useState<string | null>(null)

  const write = async (next: Rack, deleted = false) => {
    setProblem(null)
    const failed = await model.save(next, deleted)
    setProblem(failed === NO_RACK_NODE ? t('running.rack.problem.noNode') : failed)
  }
  const item = rack.items.find((i) => i.id === chosen) ?? null
  // What is done with a thing is said right under it, the units below moved
  // down for it: in a rack of 42 units the foot of the rack is a long way
  // from its top. `openAfter` is the row, from the top, that it follows.
  const chosenRows = item ? gridRows(rack.units, item) : null
  const openAfter = chosenRows ? chosenRows.start + chosenRows.span - 1 : adding !== null && adding <= rack.units ? rack.units - adding + 1 : null
  const row = (r: number) => (openAfter !== null && r > openAfter ? r + 1 : r)
  const below = openAfter === null ? 0 : rack.units - openAfter
  const template = openAfter === null
    ? `repeat(${rack.units}, 2.5rem)`
    : `repeat(${openAfter}, 2.5rem) auto${below > 0 ? ` repeat(${below}, 2.5rem)` : ''}`
  const erase = async () => {
    if (!(await confirm({ title: t('running.rack.confirmDelete', { name: rack.name }), confirmLabel: t('running.rack.delete'), danger: true }))) return
    void write(rack, true)
  }
  return (
    <section data-testid="rack" data-rack={rack.id} className="w-full rounded-2xl bg-card p-4 card-shadow sm:w-[24rem]">
      <header className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <Heading level={level} className="break-words text-[15px] font-semibold">{rack.name}</Heading>
          <p className="text-xs text-muted-foreground">{t('running.rack.summary', { units: rack.units, count: rack.items.length })}</p>
        </div>
        <Button type="button" variant="outline" size="sm" className="shrink-0 rounded-lg" aria-expanded={editing} onClick={() => { setEditing((v) => !v); setChosen(null); setAdding(null) }}>
          <Pencil aria-hidden="true" />
          {t('running.rack.edit')}
        </Button>
      </header>
      {editing && (
        <RackSettings
          rack={rack}
          onCancel={() => setEditing(false)}
          onDelete={() => void erase()}
          onSave={(name, units) => { setEditing(false); void write({ ...rack, name, units }) }}
        />
      )}
      <ol
        aria-label={t('running.rack.label', { name: rack.name })}
        className="mt-3 grid grid-cols-[1.75rem_minmax(0,1fr)] gap-x-1.5 gap-y-0.5 rounded-xl border-2 border-border bg-secondary/40 p-1.5"
        style={{ gridTemplateRows: template }}
      >
        {Array.from({ length: rack.units }, (_, i) => rack.units - i).map((unit) => (
          <li key={`n${unit}`} aria-hidden="true" className="col-start-1 flex items-center justify-end pr-0.5 font-mono text-xs tabular-nums text-muted-foreground" style={{ gridRow: row(rack.units - unit + 1) }}>
            {unit}
          </li>
        ))}
        {slots(rack).map((slot) => {
          if (slot.kind === 'free') {
            return (
              <Fragment key={`f${slot.unit}`}>
                <li className="col-start-2" style={{ gridRow: row(rack.units - slot.unit + 1) }}>
                  <button
                    type="button"
                    data-testid="rack-free"
                    data-unit={slot.unit}
                    aria-label={t('running.rack.addAt', { unit: slot.unit })}
                    aria-pressed={adding === slot.unit}
                    onClick={() => { setAdding(adding === slot.unit ? null : slot.unit); setChosen(null); setEditing(false) }}
                    className={cn(
                      'group flex h-full w-full items-center justify-center rounded-md border border-dashed text-muted-foreground hover:border-primary hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                      adding === slot.unit ? 'border-primary text-primary' : 'border-border/80',
                    )}
                  >
                    <Plus className={cn('size-4 group-hover:opacity-100 group-focus-visible:opacity-100', adding === slot.unit ? 'opacity-100' : 'opacity-50')} aria-hidden="true" />
                  </button>
                </li>
                {adding === slot.unit && (
                  <li style={{ gridRow: openAfter! + 1, gridColumn: '1 / -1' }}>
                    <AddForm
                      rack={rack}
                      unit={slot.unit}
                      model={model}
                      racks={racks}
                      onCancel={() => setAdding(null)}
                      onPut={(next) => {
                        const placedRack = place(rack, next)
                        setAdding(null)
                        if (placedRack) void write(placedRack)
                      }}
                    />
                  </li>
                )}
              </Fragment>
            )
          }
          const rows = gridRows(rack.units, slot.item)
          const what = held(slot.item, model)
          return (
            <Fragment key={slot.item.id}>
              <li className="col-start-2" style={{ gridRow: `${row(rows.start)} / span ${rows.span}` }}>
                <button
                  type="button"
                  data-testid="rack-item"
                  data-kind={slot.item.kind}
                  data-unit={slot.item.u}
                  data-height={slot.item.height}
                  aria-pressed={chosen === slot.item.id}
                  onClick={() => { setChosen(chosen === slot.item.id ? null : slot.item.id); setAdding(null); setEditing(false) }}
                  className={cn(
                    'flex h-full w-full items-center gap-2 rounded-md border bg-card px-2 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                    chosen === slot.item.id ? 'border-primary' : 'border-border hover:border-muted-foreground',
                  )}
                >
                  <Face what={what} tall={rows.span > 1} />
                  <span className="sr-only">{t('running.rack.at', { from: slot.item.u, to: slot.item.u + slot.item.height - 1 })}</span>
                </button>
              </li>
              {chosen === slot.item.id && (
                <li style={{ gridRow: openAfter! + 1, gridColumn: '1 / -1' }}>
                  <ItemPanel rack={rack} item={slot.item} what={what} onChange={(next) => void write(next)} onClose={() => setChosen(null)} />
                </li>
              )}
            </Fragment>
          )
        })}
      </ol>
      {problem && <p role="alert" className="mt-2 break-words text-xs text-destructive">{t('running.rack.problem.saveFailed')} {problem}</p>}
    </section>
  )
}

/** Making a rack: a name and how many units tall it is. */
function NewRack({ count, onCreate }: { count: number; onCreate: (rack: Rack) => Promise<void> }) {
  const { t } = useTranslation()
  const id = useId()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [units, setUnits] = useState(12)
  const problem = rackProblem(name, units, count)
  if (!open) {
    return (
      <Button type="button" size="sm" className="rounded-xl" onClick={() => setOpen(true)}>
        <Plus aria-hidden="true" />
        {t('running.rack.add')}
      </Button>
    )
  }
  return (
    <form
      data-testid="rack-new"
      onSubmit={(e) => {
        e.preventDefault()
        if (problem) return
        const rack: Rack = { id: newId(), name: name.trim(), units, items: [], updatedAt: 0 }
        setOpen(false)
        setName('')
        void onCreate(rack)
      }}
      className="flex w-full flex-wrap items-end gap-2 rounded-2xl bg-card p-4 card-shadow sm:w-auto"
    >
      <label htmlFor={`${id}-name`} className="text-xs text-muted-foreground">
        {t('running.rack.name')}
        <input id={`${id}-name`} value={name} onChange={(e) => setName(e.target.value)} maxLength={MAX_NAME} autoComplete="off" autoFocus className={cn(fieldClass, 'mt-0.5 block w-56 max-w-full')} />
      </label>
      <label htmlFor={`${id}-units`} className="text-xs text-muted-foreground">
        {t('running.rack.units')}
        <input id={`${id}-units`} type="number" min={1} max={MAX_UNITS} value={units} onChange={(e) => setUnits(Number(e.target.value))} className={cn(fieldClass, 'mt-0.5 block w-20')} />
      </label>
      <Button type="submit" size="sm" className="rounded-lg" disabled={!!problem}>{t('running.rack.create')}</Button>
      <Button type="button" variant="ghost" size="sm" className="rounded-lg" onClick={() => setOpen(false)}>{t('common.cancel')}</Button>
      {problem && name !== '' && <p className="w-full text-xs text-destructive">{t(`running.rack.problem.${problem}`, { min: 1 })}</p>}
    </form>
  )
}

/**
 * The rack view: racks side by side, each unit holding a server, a device of
 * the registry or a word. Where a thing sits is the operator's to say; what
 * the unit shows of it — a server's ports and their links, whether a device
 * was found by the last scan — is the panel's. Nothing is dragged: a thing is
 * put on a free unit and moved a unit at a time, so a phone and a keyboard do
 * all a mouse does.
 */
export function RackView({ sections, round, level }: { sections: BoardSection[]; round: number; level: HeadingLevel }) {
  const { t } = useTranslation()
  const model = useRackView(sections, round)
  const [problem, setProblem] = useState<string | null>(null)
  const create = async (rack: Rack) => {
    setProblem(null)
    const failed = await model.save(rack)
    setProblem(failed === NO_RACK_NODE ? t('running.rack.problem.noNode') : failed)
  }
  return (
    <div className="space-y-4" data-testid="rack-view">
      {model.failed.map((f) => (
        <p key={f.id} role="alert" className="break-words rounded-2xl bg-card px-4 py-3 text-[13px] text-destructive card-shadow">
          {t('running.rack.loadError', { node: f.name })} {f.error}
        </p>
      ))}
      {model.loading && <p role="status" className="rounded-2xl bg-card px-4 py-3 text-[13px] text-muted-foreground card-shadow">{t('common.loading')}</p>}
      {model.unsupported && <p className="rounded-2xl bg-card px-4 py-3 text-[13px] text-muted-foreground card-shadow">{t('running.rack.unsupported')}</p>}
      {!model.loading && !model.unsupported && (
        <>
          <NewRack count={model.racks.length} onCreate={create} />
          {problem && <p role="alert" className="break-words text-xs text-destructive">{t('running.rack.problem.saveFailed')} {problem}</p>}
          {model.racks.length === 0 && model.failed.length === 0 && (
            <p className="rounded-2xl bg-card px-4 py-3 text-[13px] text-muted-foreground card-shadow">{t('running.rack.empty')}</p>
          )}
          <div className="flex flex-wrap items-start gap-4">
            {model.racks.map((rack) => <RackCard key={rack.id} rack={rack} racks={model.racks} model={model} level={level} />)}
          </div>
        </>
      )}
    </div>
  )
}
