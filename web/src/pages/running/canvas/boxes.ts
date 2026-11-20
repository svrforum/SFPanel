import type { Node } from '@xyflow/react'
import { containerProblem, filterContents, filterGuests, stackInPart, type RunningContainer, type RunningGuest, type RunningStack } from '@/lib/running'
import type { LayoutBox, LayoutLink } from '@/lib/runningLayout'
import type { HeadingLevel } from '../Heading'
import type { Scope } from '../targets'
import type { BoardSection, GuestServer } from '../useRunningBoard'

export type TileItem =
  | { type: 'guest'; guest: RunningGuest; server?: GuestServer }
  | { type: 'stack'; stack: RunningStack; attention: boolean }
  | { type: 'container'; container: RunningContainer; attention: boolean }

export type NoteKind = 'notAsked' | 'loading' | 'loadError' | 'noDocker' | 'empty' | 'guestsError'

/** A row of a server box, as the layout sizes it and the box draws it. */
export type CanvasRow =
  | { kind: 'title'; key: string; group: 'guests' | 'stacks' | 'standalone'; count: number }
  | { kind: 'tile'; key: string; name: string; item: TileItem }
  | { kind: 'note'; key: string; note: NoteKind }

export const guestTileKey = (vmid: number) => `guest:${vmid}`

/**
 * What a server box holds, in the board's order: the guests, the stacks, the
 * standalone containers, each under its title. Where there is nothing to
 * draw, a note says why, as the board does; a search draws only what it
 * found, so a box it found nothing in is its header alone.
 */
export function boxRows(section: BoardSection, query: string): CanvasRow[] {
  const searching = query.trim() !== ''
  const rows: CanvasRow[] = []
  const note = (n: NoteKind) => { if (!searching) rows.push({ kind: 'note', key: `note:${n}`, note: n }) }

  const guests = section.guests
  if (guests?.load.phase === 'error') note('guestsError')
  else if (guests) {
    const shown = filterGuests(guests.guests, query)
    if (shown.length > 0) {
      rows.push({ kind: 'title', key: 'title:guests', group: 'guests', count: shown.length })
      for (const g of shown) rows.push({ kind: 'tile', key: guestTileKey(g.vmid), name: g.title, item: { type: 'guest', guest: g, server: guests.servers.get(g.vmid) } })
    }
  }

  const load = section.load
  if (!load) note('notAsked')
  else if (load.phase === 'loading') note('loading')
  else if (load.phase === 'error') note('loadError')
  else if (load.phase === 'noDocker') note('noDocker')
  else if (section.node.contents) {
    const { contents } = section.node
    const shown = filterContents(contents, query)
    if (shown.stacks.length > 0) {
      rows.push({ kind: 'title', key: 'title:stacks', group: 'stacks', count: shown.stacks.length })
      for (const s of shown.stacks) {
        const attention = stackInPart(s) || s.containers.some((c) => containerProblem(c) !== null)
        rows.push({ kind: 'tile', key: `stack:${s.managed ? 'managed' : 'label'}:${s.name}`, name: s.name, item: { type: 'stack', stack: s, attention } })
      }
    }
    if (shown.standalone.length > 0) {
      rows.push({ kind: 'title', key: 'title:standalone', group: 'standalone', count: shown.standalone.length })
      for (const c of shown.standalone) {
        rows.push({ kind: 'tile', key: `ctr:${c.id}`, name: c.name, item: { type: 'container', container: c, attention: containerProblem(c) !== null } })
      }
    }
    if (contents.stacks.length === 0 && contents.standalone.length === 0 && !guests) note('empty')
  }
  return rows
}

export function layoutBox(section: BoardSection, rows: CanvasRow[]): LayoutBox {
  return { id: section.node.id, name: section.node.name, rows }
}

/** Each guest that is one of the drawn servers: from its tile, if the box draws it, to that server's box. */
export function guestLinks(sections: BoardSection[], drawn: ReadonlyMap<string, CanvasRow[]>): (LayoutLink & { guest: RunningGuest })[] {
  const ids = new Set(sections.map((s) => s.node.id))
  const links: (LayoutLink & { guest: RunningGuest })[] = []
  for (const s of sections) {
    const rows = drawn.get(s.node.id) ?? []
    for (const row of rows) {
      if (row.kind !== 'tile' || row.item.type !== 'guest' || !row.item.server) continue
      if (!ids.has(row.item.server.id) || row.item.server.id === s.node.id) continue
      links.push({ host: s.node.id, tile: row.key, node: row.item.server.id, guest: row.item.guest })
    }
  }
  return links
}

/** What a server box is drawn from. */
export type ServerBoxData = {
  section: BoardSection
  /** The rows drawn: what the search left. */
  rows: CanvasRow[]
  scope: Scope
  localId: string
  level: HeadingLevel
  /** The guest tiles an edge leaves from, which carry a handle for it. */
  linked: ReadonlySet<string>
}

export type ServerNode = Node<ServerBoxData, 'server'>
