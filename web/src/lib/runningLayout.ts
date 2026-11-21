// Where the running board's canvas draws its server boxes. Pure: the boxes'
// sizes come from what is in them, worked out with the same constants the
// canvas draws them with, so the first layout needs no measuring and the
// layout and the drawing cannot drift apart. Pinned by runningLayout.test.ts.

/**
 * Sizes in CSS pixels at zoom 1. The canvas draws every row at exactly these
 * heights. They are kept tight: the whole layout is fitted to the screen, so
 * every pixel a row or a gap does not need is taken from the size of the
 * letters. A real cluster drawn at the first, roomier sizes came out at 61 %,
 * its tile names 8 pixels high.
 */
export const CANVAS = {
  boxWidth: 288,
  /** Borders are drawn inside the sizes here (border-box), so they are counted in them. */
  boxBorder: 1,
  boxPadding: 12,
  /** The header: the name (one or two lines), then a row for kind and state, and one for the meters. */
  nameLineHeight: 22,
  headerRowHeight: 22,
  /** Between the header and the first row. */
  headerGap: 6,
  /** A group's title ("스택 4"). */
  titleHeight: 20,
  /** A tile: its name (one or two lines) beside its state word. */
  tileLineHeight: 20,
  tileBorder: 1,
  /** Above and below; a tile's sides have tileInset. */
  tilePadding: 4,
  tileInset: 8,
  pillHeight: 20,
  /** A line that says why a box has no tiles: two lines of text. */
  noteHeight: 36,
  rowGap: 4,
  columnGap: 88,
  boxGap: 20,
  /** Between one host's block (the host and its guests' servers) and the next. */
  blockGap: 36,
  /** Where an edge comes into a server's box: on its top edge, this far from its left. */
  anchorX: 28,
  /** An edge leaves its tile, turns into a channel this far past the host's box (each edge a step further), */
  channelBase: 14,
  channelStep: 8,
  /** and runs along a lane this far above the row of servers (each edge a step higher), under a margin. */
  laneBase: 14,
  laneStep: 8,
  laneMargin: 10,
  /** How round an edge's corners are. */
  edgeCorner: 6,
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

/**
 * How an edge runs from a guest tile to the box of the server that guest is.
 * (A type, not an interface: the canvas carries it as an edge's data, which
 * the library wants to be a plain record.)
 */
export type Route = {
  /** How far to the right of the tile's edge it turns up or down. */
  channel: number
  /** How far above the top of the server's box it runs before coming down into it. */
  lane: number
}

/** Where the boxes go, and how each edge runs: keyed by linkKey. */
export interface Layout {
  placed: Map<string, Placed>
  routes: Map<string, Route>
}

export const linkKey = (host: string, tile: string) => `${host}/${tile}`

/** A host and the servers that are its guests, and theirs: laid out as one block. */
interface Block extends Layout {
  width: number
  height: number
  /** Where the top of the host's own box is, below the lanes of its row. */
  top: number
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
 * Where each box goes, and how each edge runs. A server that is no known
 * guest heads a block: its box, and beside it, in a row with their tops
 * level with its own, the servers that are its guests, each heading a block
 * of its own in turn. One under the other, level with their guest tiles,
 * they made a column no screen could show: a Proxmox host's guest list is
 * long, and four of its guests being nodes meant four boxes stacked.
 *
 * In a row an edge cannot run straight, so it goes round: out of the tile,
 * up a channel in the gap beside the host, along a lane above the row, and
 * down into the top of the server's box. Every edge has a channel and a lane
 * of its own, and the servers are set out so that none crosses another: the
 * lowest tile's server first, with the channel furthest out and the lowest
 * lane; the highest tile's last, with the nearest channel and the top lane.
 *
 * The blocks are then set out in rows, in the order given, the way that
 * shows them largest in an area `aspect` times as wide as it is high: side
 * by side on a wide screen, one under the other on a phone.
 *
 * A link whose ends are not both drawn, or that would make a server a guest
 * of its own guest, is left out, as is a second host for a server that
 * already has one. `column` is a box's depth below the head of its block.
 */
export function layoutBoxes(boxes: LayoutBox[], links: LayoutLink[], aspect: number): Layout {
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

  // Each host's links, from its highest guest tile down.
  const guestsOf = new Map<string, LayoutLink[]>()
  for (const link of hostOf.values()) {
    const list = guestsOf.get(link.host)
    if (list) list.push(link)
    else guestsOf.set(link.host, [link])
  }
  for (const [host, list] of guestsOf) {
    const hostBox = byId.get(host)!
    list.sort((p, q) => rowTop(hostBox, p.tile)! - rowTop(hostBox, q.tile)!)
  }

  const blockOf = (box: LayoutBox, depth: number): Block => {
    const guests = guestsOf.get(box.id) ?? []
    const n = guests.length
    const top = n > 0 ? CANVAS.laneBase + (n - 1) * CANVAS.laneStep + CANVAS.laneMargin : 0
    const own = boxHeight(box)
    const placed = new Map<string, Placed>([[box.id, { x: 0, y: top, width: CANVAS.boxWidth, height: own, column: depth }]])
    const routes = new Map<string, Route>()
    // Every channel in the gap beside the host, however many there are.
    const step = n > 1 ? Math.min(CANVAS.channelStep, (CANVAS.columnGap - 2 * CANVAS.channelBase) / (n - 1)) : 0
    let x = CANVAS.boxWidth + CANVAS.columnGap
    let height = top + own
    for (let i = n - 1; i >= 0; i--) {
      const link = guests[i]
      const guest = blockOf(byId.get(link.node)!, depth + 1)
      for (const [id, p] of guest.placed) placed.set(id, { ...p, x: p.x + x, y: p.y + top })
      for (const [key, route] of guest.routes) routes.set(key, route)
      routes.set(linkKey(link.host, link.tile), {
        // From the tile's edge, which is inside the box by its padding.
        channel: CANVAS.boxBorder + CANVAS.boxPadding + CANVAS.channelBase + i * step,
        // From the top of the server's box, which is below its own row's lanes.
        lane: guest.top + CANVAS.laneBase + (n - 1 - i) * CANVAS.laneStep,
      })
      x += guest.width + CANVAS.boxGap
      height = Math.max(height, top + guest.height)
    }
    return { placed, routes, width: n > 0 ? x - CANVAS.boxGap : CANVAS.boxWidth, height, top }
  }

  const blocks = boxes.filter((b) => !hostOf.has(b.id)).map((b) => blockOf(b, 0))
  const out: Layout = { placed: new Map(), routes: new Map() }
  if (blocks.length === 0) return out
  let y = 0
  for (const row of arrange(blocks, aspect)) {
    let x = 0
    for (const block of row) {
      for (const [id, p] of block.placed) out.placed.set(id, { ...p, x: p.x + x, y: p.y + y })
      for (const [key, route] of block.routes) out.routes.set(key, route)
      x += block.width + CANVAS.blockGap
    }
    y += rowHeightOf(row) + CANVAS.blockGap
  }
  return out
}

/**
 * The corners of an edge from a guest tile's side (sx, sy) to the top of a
 * server's box (tx, ty): the turn into its channel, the turn into its lane,
 * the turn down.
 */
export function edgePoints(sx: number, sy: number, tx: number, ty: number, route: Route): [number, number][] {
  const channel = sx + route.channel
  const lane = ty - route.lane
  return [[sx, sy], [channel, sy], [channel, lane], [tx, lane], [tx, ty]]
}

/** The same as an SVG path, its corners rounded as far as the segments on either side allow. */
export function edgePath(sx: number, sy: number, tx: number, ty: number, route: Route): string {
  const points = edgePoints(sx, sy, tx, ty, route)
  let d = `M ${sx} ${sy}`
  for (let i = 1; i < points.length - 1; i++) {
    const [px, py] = points[i - 1]
    const [x, y] = points[i]
    const [nx, ny] = points[i + 1]
    const before = Math.hypot(x - px, y - py)
    const after = Math.hypot(nx - x, ny - y)
    const r = Math.min(CANVAS.edgeCorner, before / 2, after / 2)
    if (r <= 0) {
      d += ` L ${x} ${y}`
      continue
    }
    d += ` L ${x - ((x - px) / before) * r} ${y - ((y - py) / before) * r} Q ${x} ${y} ${x + ((nx - x) / after) * r} ${y + ((ny - y) / after) * r}`
  }
  return `${d} L ${tx} ${ty}`
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
