// Where the running board's canvas draws its server boxes. Pure: the boxes'
// sizes come from what is in them, worked out with the same constants the
// canvas draws them with, so the first layout needs no measuring and the
// layout and the drawing cannot drift apart. Pinned by runningLayout.test.ts.

/** Sizes in CSS pixels at zoom 1. The canvas draws every row at exactly these heights. */
export const CANVAS = {
  boxWidth: 288,
  boxPadding: 12,
  /** The header: the name (one or two lines), then a row for kind and state, and one for the meters. */
  nameLineHeight: 20,
  headerRowHeight: 24,
  /** Between the header and the first row. */
  headerGap: 8,
  /** A group's title ("스택 4"). */
  titleHeight: 22,
  /** A tile: its name (one or two lines) beside its state word. */
  tileLineHeight: 18,
  tilePadding: 8,
  pillHeight: 22,
  /** A line that says why a box has no tiles: two lines of text. */
  noteHeight: 36,
  rowGap: 6,
  columnGap: 120,
  boxGap: 32,
  /** How much of a name one line holds, in narrow letters: an estimate on the safe side. */
  headerNameChars: 24,
  tileNameChars: 18,
  /** A longer name is cut, with the whole of it in the tile's title and accessible name. */
  maxNameLines: 2,
} as const

export type BoxRow =
  | { kind: 'title'; key: string }
  | { kind: 'tile'; key: string; name: string }
  | { kind: 'note'; key: string }

/** A server box: its node's id and name, and the rows drawn under its header. */
export interface LayoutBox {
  id: string
  name: string
  rows: BoxRow[]
}

/** A guest tile of `host` (the row keyed `tile`) that is the server drawn as box `node`. */
export interface LayoutLink {
  host: string
  tile: string
  node: string
}

export interface Placed {
  x: number
  y: number
  width: number
  height: number
  column: number
}

// Hangul, CJK and full-width forms take about twice the width of a Latin letter.
const WIDE = /[ᄀ-ᅟ⺀-꓏가-힣豈-﫿︰-﹏＀-｠￠-￦]/

/** How many lines a name takes where a line holds `perLine` narrow letters, up to maxNameLines. */
export function nameLines(name: string, perLine: number): number {
  let units = 0
  for (const ch of name) units += WIDE.test(ch) ? 2 : 1
  return Math.min(CANVAS.maxNameLines, Math.max(1, Math.ceil(units / perLine)))
}

export function headerHeight(name: string): number {
  return nameLines(name, CANVAS.headerNameChars) * CANVAS.nameLineHeight + 2 * CANVAS.headerRowHeight
}

export function rowHeight(row: BoxRow): number {
  if (row.kind === 'title') return CANVAS.titleHeight
  if (row.kind === 'note') return CANVAS.noteHeight
  return 2 * CANVAS.tilePadding + Math.max(CANVAS.pillHeight, nameLines(row.name, CANVAS.tileNameChars) * CANVAS.tileLineHeight)
}

export function boxHeight(box: LayoutBox): number {
  const rows = box.rows.reduce((sum, r) => sum + rowHeight(r), 0)
  const body = box.rows.length > 0 ? CANVAS.headerGap + rows + (box.rows.length - 1) * CANVAS.rowGap : 0
  return 2 * CANVAS.boxPadding + headerHeight(box.name) + body
}

/** Where the row keyed `key` starts, from the top of its box; null for a row the box does not have. */
export function rowTop(box: LayoutBox, key: string): number | null {
  let y = CANVAS.boxPadding + headerHeight(box.name) + CANVAS.headerGap
  for (const row of box.rows) {
    if (row.key === key) return y
    y += rowHeight(row) + CANVAS.rowGap
  }
  return null
}

/** Where an edge comes into a box: level with the middle of its name, on the left. */
export function anchorY(box: LayoutBox): number {
  return CANVAS.boxPadding + (nameLines(box.name, CANVAS.headerNameChars) * CANVAS.nameLineHeight) / 2
}

/**
 * Columns by depth: the servers that are no known guest in column 0, in the
 * order given; the servers that are their guests in column 1, and so on. A
 * guest's server goes level with its guest tile, so the edge between them
 * runs straight, unless the box above it leaves no room; then it goes a gap
 * below that box. A link whose ends are not both drawn, or that would make a
 * server a guest of its own guest, is left out, as is a second host for a
 * server that already has one.
 */
export function layoutBoxes(boxes: LayoutBox[], links: LayoutLink[]): Map<string, Placed> {
  const byId = new Map(boxes.map((b) => [b.id, b]))
  const hostOf = new Map<string, LayoutLink>()
  for (const l of links) {
    const host = byId.get(l.host)
    if (!host || !byId.has(l.node) || l.host === l.node || hostOf.has(l.node) || rowTop(host, l.tile) === null) continue
    let up: string | undefined = l.host
    while (up !== undefined && up !== l.node) up = hostOf.get(up)?.host
    if (up === l.node) continue
    hostOf.set(l.node, l)
  }

  const depth = new Map<string, number>()
  const depthOf = (id: string): number => {
    const known = depth.get(id)
    if (known !== undefined) return known
    const link = hostOf.get(id)
    const d = link ? depthOf(link.host) + 1 : 0
    depth.set(id, d)
    return d
  }

  const columns: LayoutBox[][] = []
  for (const b of boxes) (columns[depthOf(b.id)] ??= []).push(b)

  const placed = new Map<string, Placed>()
  columns.forEach((column, index) => {
    const wanted = column.map((b, order) => {
      const link = hostOf.get(b.id)
      if (!link) return { b, order, y: 0 }
      const host = placed.get(link.host)!
      const hostBox = byId.get(link.host)!
      const tileRow = hostBox.rows.find((r) => r.key === link.tile)!
      const middle = host.y + rowTop(hostBox, link.tile)! + rowHeight(tileRow) / 2
      return { b, order, y: middle - anchorY(b) }
    })
    if (index > 0) wanted.sort((p, q) => p.y - q.y || p.order - q.order)
    let next = 0
    for (const { b, y: want } of wanted) {
      const y = Math.max(want, next)
      const height = boxHeight(b)
      placed.set(b.id, { x: index * (CANVAS.boxWidth + CANVAS.columnGap), y, width: CANVAS.boxWidth, height, column: index })
      next = y + height + CANVAS.boxGap
    }
  })
  return placed
}

/** Where boxes were moved to, by node id. */
export type Positions = Record<string, { x: number; y: number }>

/** Positions as stored; a value that cannot be read is no positions at all, and an entry that is no place is dropped. */
export function readPositions(raw: string | null): Positions {
  let value: unknown
  try {
    value = JSON.parse(raw ?? '')
  } catch {
    return {}
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  const out: Positions = {}
  for (const [id, p] of Object.entries(value)) {
    if (!p || typeof p !== 'object') continue
    const { x, y } = p as Record<string, unknown>
    if (typeof x === 'number' && typeof y === 'number' && Number.isFinite(x) && Number.isFinite(y)) out[id] = { x, y }
  }
  return out
}

/** The positions of the nodes that still exist: one that left the cluster takes its place with it. */
export function keepPositions(saved: Positions, ids: Iterable<string>): Positions {
  const out: Positions = {}
  for (const id of ids) if (Object.hasOwn(saved, id)) out[id] = saved[id]
  return out
}
