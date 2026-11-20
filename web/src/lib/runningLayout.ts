// Where the running board's canvas draws its server boxes. Pure: the boxes'
// sizes come from what is in them, worked out with the same constants the
// canvas draws them with, so the first layout needs no measuring and the
// layout and the drawing cannot drift apart. Pinned by runningLayout.test.ts.

/** Sizes in CSS pixels at zoom 1. The canvas draws every row at exactly these heights. */
export const CANVAS = {
  boxWidth: 288,
  /** Borders are drawn inside the sizes here (border-box), so they are counted in them. */
  boxBorder: 1,
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
  tileBorder: 1,
  tilePadding: 8,
  pillHeight: 22,
  /** A line that says why a box has no tiles: two lines of text. */
  noteHeight: 36,
  rowGap: 6,
  columnGap: 120,
  boxGap: 32,
  /** Between one host's family (the host and its guests' servers) and the next. */
  blockGap: 48,
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
  return 2 * (CANVAS.tileBorder + CANVAS.tilePadding) + Math.max(CANVAS.pillHeight, nameLines(row.name, CANVAS.tileNameChars) * CANVAS.tileLineHeight)
}

export function boxHeight(box: LayoutBox): number {
  const rows = box.rows.reduce((sum, r) => sum + rowHeight(r), 0)
  const body = box.rows.length > 0 ? CANVAS.headerGap + rows + (box.rows.length - 1) * CANVAS.rowGap : 0
  return 2 * (CANVAS.boxBorder + CANVAS.boxPadding) + headerHeight(box.name) + body
}

/** Where the row keyed `key` starts, from the top of its box; null for a row the box does not have. */
export function rowTop(box: LayoutBox, key: string): number | null {
  let y = CANVAS.boxBorder + CANVAS.boxPadding + headerHeight(box.name) + CANVAS.headerGap
  for (const row of box.rows) {
    if (row.key === key) return y
    y += rowHeight(row) + CANVAS.rowGap
  }
  return null
}

/** Where an edge comes into a box: level with the middle of its name, on the left. */
export function anchorY(box: LayoutBox): number {
  return CANVAS.boxBorder + CANVAS.boxPadding + (nameLines(box.name, CANVAS.headerNameChars) * CANVAS.nameLineHeight) / 2
}

/** A host and the servers that are its guests, and theirs: laid out as one block. */
interface Block {
  placed: Map<string, Placed>
  width: number
  height: number
}

/**
 * The blocks in rows, in the order given, each row as wide as `limit` lets it
 * be (a row always takes at least one block).
 */
function rowsWithin(blocks: Block[], limit: number): Block[][] {
  const rows: Block[][] = []
  let width = 0
  for (const block of blocks) {
    const row = rows[rows.length - 1]
    if (row && width + CANVAS.blockGap + block.width <= limit) {
      row.push(block)
      width += CANVAS.blockGap + block.width
    } else {
      rows.push([block])
      width = block.width
    }
  }
  return rows
}

const rowWidth = (row: Block[]) => row.reduce((sum, b) => sum + b.width, 0) + (row.length - 1) * CANVAS.blockGap
const rowHeightOf = (row: Block[]) => Math.max(...row.map((b) => b.height))

/**
 * The rows that show the blocks largest in an area `aspect` times as wide as
 * it is high. Every width a run of neighbouring blocks comes to is tried as
 * the limit of a row, so nothing a greedy single pass would miss is missed;
 * there are few servers, and this is done once per layout.
 */
function arrange(blocks: Block[], aspect: number): Block[][] {
  const narrowest = Math.max(...blocks.map((b) => b.width))
  if (!Number.isFinite(aspect) || aspect <= 0) return rowsWithin(blocks, narrowest)
  const limits = new Set<number>()
  for (let i = 0; i < blocks.length; i++) {
    let width = blocks[i].width
    limits.add(width)
    for (let j = i + 1; j < blocks.length; j++) limits.add((width += CANVAS.blockGap + blocks[j].width))
  }
  let best: Block[][] = []
  let bestScale = -1
  for (const limit of [...limits].filter((l) => l >= narrowest).sort((p, q) => p - q)) {
    const rows = rowsWithin(blocks, limit)
    const width = Math.max(...rows.map(rowWidth))
    const height = rows.reduce((sum, r) => sum + rowHeightOf(r), 0) + (rows.length - 1) * CANVAS.blockGap
    const scale = Math.min(aspect / width, 1 / height)
    // Strictly better only: of two that show the same, the narrower stays.
    if (scale > bestScale) {
      best = rows
      bestScale = scale
    }
  }
  return best
}

/**
 * Where each box goes. A server that is no known guest heads a block: itself
 * in the block's first column, the servers that are its guests in the next,
 * theirs in the one after. A guest's server goes level with its guest tile,
 * so the edge between them runs straight, unless the box above it leaves no
 * room; then it goes a gap below that box. The blocks are then set out in
 * rows, in the order given, the way that shows them largest in an area
 * `aspect` times as wide as it is high: side by side on a wide screen, one
 * under the other on a phone.
 *
 * A link whose ends are not both drawn, or that would make a server a guest
 * of its own guest, is left out, as is a second host for a server that
 * already has one. `column` is a box's depth in its block.
 */
export function layoutBoxes(boxes: LayoutBox[], links: LayoutLink[], aspect: number): Map<string, Placed> {
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

  // Every box's head of block and depth in it, and the blocks' columns in
  // the order the boxes were given.
  const where = new Map<string, { root: string; depth: number }>()
  const whereOf = (id: string): { root: string; depth: number } => {
    const known = where.get(id)
    if (known) return known
    const link = hostOf.get(id)
    const up = link ? whereOf(link.host) : null
    const found = up ? { root: up.root, depth: up.depth + 1 } : { root: id, depth: 0 }
    where.set(id, found)
    return found
  }
  const columnsOf = new Map<string, LayoutBox[][]>()
  for (const b of boxes) {
    const { root, depth } = whereOf(b.id)
    let columns = columnsOf.get(root)
    if (!columns) columnsOf.set(root, (columns = []))
    ;(columns[depth] ??= []).push(b)
  }

  const blocks: Block[] = []
  for (const b of boxes) {
    const columns = columnsOf.get(b.id)
    if (!columns) continue
    const placed = new Map<string, Placed>()
    let height = 0
    columns.forEach((column, index) => {
      const wanted = column.map((box, order) => {
        const link = hostOf.get(box.id)
        if (!link) return { box, order, y: 0 }
        const host = placed.get(link.host)!
        const hostBox = byId.get(link.host)!
        const tileRow = hostBox.rows.find((r) => r.key === link.tile)!
        const middle = host.y + rowTop(hostBox, link.tile)! + rowHeight(tileRow) / 2
        return { box, order, y: middle - anchorY(box) }
      })
      if (index > 0) wanted.sort((p, q) => p.y - q.y || p.order - q.order)
      let next = 0
      for (const { box, y: want } of wanted) {
        const y = Math.max(want, next)
        const h = boxHeight(box)
        placed.set(box.id, { x: index * (CANVAS.boxWidth + CANVAS.columnGap), y, width: CANVAS.boxWidth, height: h, column: index })
        next = y + h + CANVAS.boxGap
        height = Math.max(height, y + h)
      }
    })
    blocks.push({ placed, width: columns.length * CANVAS.boxWidth + (columns.length - 1) * CANVAS.columnGap, height })
  }

  const out = new Map<string, Placed>()
  if (blocks.length === 0) return out
  let y = 0
  for (const row of arrange(blocks, aspect)) {
    let x = 0
    for (const block of row) {
      for (const [id, p] of block.placed) out.set(id, { ...p, x: p.x + x, y: p.y + y })
      x += block.width + CANVAS.blockGap
    }
    y += rowHeightOf(row) + CANVAS.blockGap
  }
  return out
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
