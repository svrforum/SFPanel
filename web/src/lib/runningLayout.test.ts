import { describe, expect, it } from 'vitest'
import {
  CANVAS,
  boxHeight,
  edgePath,
  edgePoints,
  headerHeight,
  keepPositions,
  layoutBoxes,
  linkKey,
  nameLines,
  readPositions,
  rowHeight,
  rowTop,
  type BoxRow,
  type Layout,
  type LayoutBox,
  type LayoutLink,
  type Placed,
} from './runningLayout'

const title = (key: string): BoxRow => ({ kind: 'title', key })
const tile = (key: string, name = key): BoxRow => ({ kind: 'tile', key, name })
const note = (key: string): BoxRow => ({ kind: 'note', key })
const box = (id: string, rows: BoxRow[] = [], name = id): LayoutBox => ({ id, name, rows })
const tiles = (prefix: string, n: number) => Array.from({ length: n }, (_, i) => tile(`${prefix}${i}`))

const overlaps = (a: Placed, b: Placed) =>
  a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height

describe('nameLines', () => {
  it('gives a name that fits one line, and a longer one two', () => {
    expect(nameLines('photos', 20)).toBe(1)
    expect(nameLines('a'.repeat(20), 20)).toBe(1)
    expect(nameLines('a'.repeat(21), 20)).toBe(2)
  })

  it('never gives more lines than a tile draws: the rest is cut and read from the title', () => {
    expect(nameLines('a'.repeat(200), 20)).toBe(CANVAS.maxNameLines)
  })

  it('counts a Hangul or CJK letter as two narrow ones', () => {
    expect(nameLines('가'.repeat(10), 20)).toBe(1)
    expect(nameLines('가'.repeat(11), 20)).toBe(2)
    expect(nameLines('写真'.repeat(6), 20)).toBe(2)
  })

  it('takes an empty name as one line', () => {
    expect(nameLines('', 20)).toBe(1)
  })
})

describe('box sizes', () => {
  it('sizes a box with nothing in it as its header', () => {
    expect(boxHeight(box('a'))).toBe(2 * (CANVAS.boxBorder + CANVAS.boxPadding) + headerHeight('a'))
    expect(headerHeight('a')).toBe(CANVAS.nameLineHeight + 2 * CANVAS.headerRowHeight)
    expect(headerHeight('n'.repeat(CANVAS.headerNameChars + 1))).toBe(2 * CANVAS.nameLineHeight + 2 * CANVAS.headerRowHeight)
  })

  it('adds each row and the gaps between them', () => {
    const rows = [title('stacks'), tile('s1'), tile('s2', 'x'.repeat(CANVAS.tileNameChars + 1)), note('n')]
    const sum = rowHeight(rows[0]) + rowHeight(rows[1]) + rowHeight(rows[2]) + rowHeight(rows[3])
    expect(boxHeight(box('a', rows))).toBe(2 * (CANVAS.boxBorder + CANVAS.boxPadding) + headerHeight('a') + CANVAS.headerGap + sum + 3 * CANVAS.rowGap)
  })

  it('gives each kind of row its own height, a two-line tile taller than a one-line one', () => {
    expect(rowHeight(title('t'))).toBe(CANVAS.titleHeight)
    expect(rowHeight(note('n'))).toBe(CANVAS.noteHeight)
    const one = rowHeight(tile('a'))
    const two = rowHeight(tile('b', 'x'.repeat(CANVAS.tileNameChars + 1)))
    expect(one).toBe(2 * (CANVAS.tileBorder + CANVAS.tilePadding) + Math.max(CANVAS.pillHeight, CANVAS.tileLineHeight))
    expect(two).toBe(2 * (CANVAS.tileBorder + CANVAS.tilePadding) + Math.max(CANVAS.pillHeight, 2 * CANVAS.tileLineHeight))
    expect(two).toBeGreaterThan(one)
  })

  // The numbers the drawing is built on today, so a change to them is a
  // change someone meant.
  it('pins the sizes as they are drawn', () => {
    expect(boxHeight(box('alpha'))).toBe(92)
    expect(boxHeight(box('alpha', [title('stacks'), tile('media'), tile('photos')]))).toBe(186)
  })

  // A box is as tall as what it lists, and the tallest box sets how small
  // the whole canvas is drawn: twenty stacks at 46 pixels each put a real
  // cluster's letters at 8 pixels. A row is the height of its state word and
  // little more.
  it('keeps a row close to the height of its text', () => {
    expect(rowHeight(tile('a'))).toBeLessThanOrEqual(30)
    expect(rowHeight(tile('a')) + CANVAS.rowGap).toBeLessThanOrEqual(34)
    // Twenty stacks and their title, in a box with its header.
    expect(boxHeight(box('n', [title('stacks'), ...tiles('s', 20)]))).toBeLessThanOrEqual(800)
  })

  it('leaves the channels beside a host room for eight edges at a full step each', () => {
    expect(CANVAS.columnGap).toBeGreaterThanOrEqual(2 * CANVAS.channelBase + 7 * CANVAS.channelStep)
  })

  it('tells where a row starts in its box', () => {
    const b = box('a', [title('g'), tile('vm-1'), tile('vm-2')])
    const first = CANVAS.boxBorder + CANVAS.boxPadding + headerHeight('a') + CANVAS.headerGap
    expect(rowTop(b, 'g')).toBe(first)
    expect(rowTop(b, 'vm-1')).toBe(first + CANVAS.titleHeight + CANVAS.rowGap)
    expect(rowTop(b, 'vm-2')).toBe(first + CANVAS.titleHeight + rowHeight(tile('vm-1')) + 2 * CANVAS.rowGap)
    expect(rowTop(b, 'missing')).toBeNull()
  })
})

// How an edge runs, as the canvas draws it: out of the guest tile's right
// side, up (or down) a channel beside the host, along a lane above the row
// of servers, and down into the top of the server's box.
type Point = [number, number]
function edgeOf(boxes: LayoutBox[], layout: Layout, link: LayoutLink): Point[] {
  const hostBox = boxes.find((b) => b.id === link.host)!
  const host = layout.placed.get(link.host)!
  const node = layout.placed.get(link.node)!
  const row = hostBox.rows.find((r) => r.key === link.tile)!
  const sx = host.x + host.width - CANVAS.boxBorder - CANVAS.boxPadding
  const sy = host.y + rowTop(hostBox, link.tile)! + rowHeight(row) / 2
  return edgePoints(sx, sy, node.x + CANVAS.anchorX, node.y, layout.routes.get(linkKey(link.host, link.tile))!)
}
const segments = (points: Point[]) => points.slice(1).map((p, i) => [points[i], p] as [Point, Point])
const between = (v: number, a: number, b: number) => v > Math.min(a, b) && v < Math.max(a, b)
/** Two axis-parallel segments that cross, or lie along each other. */
function meet([a1, a2]: [Point, Point], [b1, b2]: [Point, Point]): boolean {
  const aH = a1[1] === a2[1], bH = b1[1] === b2[1]
  if (aH !== bH) {
    const [h1, h2] = aH ? [a1, a2] : [b1, b2]
    const [v1, v2] = aH ? [b1, b2] : [a1, a2]
    return between(v1[0], h1[0], h2[0]) && between(h1[1], v1[1], v2[1])
  }
  const k = aH ? 1 : 0
  if (a1[k] !== b1[k]) return false
  const o = aH ? 0 : 1
  return Math.max(Math.min(a1[o], a2[o]), Math.min(b1[o], b2[o])) < Math.min(Math.max(a1[o], a2[o]), Math.max(b1[o], b2[o]))
}
/** A segment that runs through the inside of a box. */
function through([p, q]: [Point, Point], box: Placed): boolean {
  if (p[1] === q[1]) return p[1] > box.y && p[1] < box.y + box.height && Math.max(Math.min(p[0], q[0]), box.x) < Math.min(Math.max(p[0], q[0]), box.x + box.width)
  return p[0] > box.x && p[0] < box.x + box.width && Math.max(Math.min(p[1], q[1]), box.y) < Math.min(Math.max(p[1], q[1]), box.y + box.height)
}
const noOverlap = (layout: Layout) => {
  const all = [...layout.placed.values()]
  for (let i = 0; i < all.length; i++) {
    for (let j = i + 1; j < all.length; j++) expect(overlaps(all[i], all[j])).toBe(false)
  }
}

// A real cluster: one Proxmox host whose list of eighteen guests is taller
// than the screen, four of them nodes of the cluster, and one machine on its
// own. With each guest's server level with its tile they made one column
// 2,500 pixels high, shown at a third of its size on a wide screen.
describe('layoutBoxes', () => {
  // pve hosts vm-a (node a), vm-b (node b) and vm-d (node d); a hosts inner (node c).
  // e and f are no one's guests.
  const pve = box('pve', [title('guests'), tile('vm-a'), tile('vm-x'), tile('vm-b'), tile('vm-y'), tile('vm-d')])
  const a = box('a', [title('guests'), tile('c-vm'), title('stacks'), ...tiles('s', 4)])
  const b = box('b', [title('stacks'), ...tiles('s', 2)])
  const c = box('c', [note('nodocker')])
  const d = box('d', [title('stacks'), ...tiles('s', 6)])
  const e = box('e', [title('stacks'), ...tiles('s', 3)])
  const f = box('f')
  const boxes = [e, pve, a, b, c, d, f]
  const links: LayoutLink[] = [
    { host: 'pve', tile: 'vm-a', node: 'a' },
    { host: 'pve', tile: 'vm-b', node: 'b' },
    { host: 'pve', tile: 'vm-d', node: 'd' },
    { host: 'a', tile: 'c-vm', node: 'c' },
  ]
  // Wide: every block in one row.
  const WIDE = 100
  // Tall and narrow: every block gets a row of its own.
  const TALL = 0.01
  const layout = layoutBoxes(boxes, links, WIDE)
  const at = (id: string) => layout.placed.get(id)!

  it('places every box, sized as boxHeight does, with its depth below its host', () => {
    expect([...layout.placed.keys()].sort()).toEqual(['a', 'b', 'c', 'd', 'e', 'f', 'pve'])
    for (const bx of boxes) expect(at(bx.id)).toMatchObject({ width: CANVAS.boxWidth, height: boxHeight(bx) })
    expect(Object.fromEntries([...layout.placed].map(([id, p]) => [id, p.column]))).toEqual({ e: 0, pve: 0, f: 0, a: 1, b: 1, d: 1, c: 2 })
  })

  it("sets a host's guests' servers in a row beside it, their tops level with its own", () => {
    for (const id of ['a', 'b', 'd']) {
      expect(at(id).x).toBeGreaterThanOrEqual(at('pve').x + CANVAS.boxWidth + CANVAS.columnGap)
      // b and d have no guests of their own, so nothing stands above them.
      if (id !== 'a') expect(at(id).y).toBe(at('pve').y)
    }
    // Not one under the other: no two of them share a column.
    expect(new Set(['a', 'b', 'd'].map((id) => at(id).x)).size).toBe(3)
  })

  it("takes the lowest guest tile's server first, which is the order no two edges cross in", () => {
    // vm-d is the lowest tile, vm-a the highest.
    expect(at('d').x).toBeLessThan(at('b').x)
    expect(at('b').x).toBeLessThan(at('a').x)
  })

  it('leaves room above the row for the edges, as much as there are edges', () => {
    // Three edges: three lanes above pve's row.
    expect(at('pve').y - at('e').y).toBe(CANVAS.laneBase + 2 * CANVAS.laneStep + CANVAS.laneMargin)
    // A host with no guests that are nodes needs none.
    expect(at('e').y).toBe(0)
    expect(at('f').y).toBe(0)
  })

  it("puts a guest's own guests' servers beside it in turn, below the lanes of its own row", () => {
    expect(at('c').x).toBe(at('a').x + CANVAS.boxWidth + CANVAS.columnGap)
    expect(at('c').y).toBe(at('a').y)
    // a's one edge has a lane of its own above a, under pve's lanes.
    expect(at('a').y).toBe(at('pve').y + CANVAS.laneBase + CANVAS.laneMargin)
  })

  it('gives every edge a channel and a lane of its own', () => {
    expect([...layout.routes.keys()].sort()).toEqual(links.map((l) => linkKey(l.host, l.tile)).sort())
    const ofPve = ['vm-a', 'vm-b', 'vm-d'].map((tileKey) => layout.routes.get(linkKey('pve', tileKey))!)
    expect(new Set(ofPve.map((r) => r.channel)).size).toBe(3)
    // All channels fit in the gap beside the host.
    for (const r of ofPve) expect(r.channel - CANVAS.boxBorder - CANVAS.boxPadding).toBeLessThan(CANVAS.columnGap)
  })

  it('lets no two edges cross or run along each other', () => {
    const edges = links.map((l) => segments(edgeOf(boxes, layout, l)))
    for (let i = 0; i < edges.length; i++) {
      for (let j = i + 1; j < edges.length; j++) {
        for (const s1 of edges[i]) for (const s2 of edges[j]) expect(meet(s1, s2)).toBe(false)
      }
    }
  })

  it('runs no edge through a box', () => {
    for (const l of links) {
      segments(edgeOf(boxes, layout, l)).forEach((segment, index) => {
        for (const [id, placedBox] of layout.placed) {
          // It leaves from inside its host's box, where its tile is.
          if (index === 0 && id === l.host) continue
          expect(through(segment, placedBox), `${l.tile} → ${l.node}, segment ${index}, box ${id}`).toBe(false)
        }
      })
    }
  })

  it('keeps edges apart and out of boxes with many guests that are nodes', () => {
    const n = 9
    const host = box('h', [title('guests'), ...Array.from({ length: n }, (_, i) => tile(`vm${i}`))])
    const servers = Array.from({ length: n }, (_, i) => box(`n${i}`, tiles('s', i % 4)))
    const many: LayoutLink[] = servers.map((srv, i) => ({ host: 'h', tile: `vm${i}`, node: srv.id }))
    const all = [host, ...servers]
    const result = layoutBoxes(all, many, WIDE)
    noOverlap(result)
    const edges = many.map((l) => segments(edgeOf(all, result, l)))
    for (let i = 0; i < edges.length; i++) {
      for (let j = i + 1; j < edges.length; j++) {
        for (const s1 of edges[i]) for (const s2 of edges[j]) expect(meet(s1, s2)).toBe(false)
      }
      edges[i].forEach((segment, index) => {
        for (const [id, placedBox] of result.placed) {
          if (index === 0 && id === 'h') continue
          expect(through(segment, placedBox)).toBe(false)
        }
      })
    }
  })

  it('lets no two boxes overlap, whatever the shape', () => {
    for (const aspect of [TALL, 0.5, 1, 1.6, 2.5, 4, WIDE]) noOverlap(layoutBoxes(boxes, links, aspect))
  })

  it('sets the blocks side by side in the order given where the area is wide, tops level', () => {
    expect(at('e').x).toBe(0)
    expect(at('pve').x).toBe(CANVAS.boxWidth + CANVAS.blockGap)
    // After pve's whole block: itself, its three servers, and a's own.
    const right = Math.max(...['pve', 'a', 'b', 'c', 'd'].map((id) => at(id).x + CANVAS.boxWidth))
    expect(at('f').x).toBe(right + CANVAS.blockGap)
  })

  it('stacks the blocks where the area is narrow, each below all of the one before', () => {
    const tall = layoutBoxes(boxes, links, TALL)
    const y = (id: string) => tall.placed.get(id)!.y
    const bottom = (...ids: string[]) => Math.max(...ids.map((id) => y(id) + tall.placed.get(id)!.height))
    expect(y('e')).toBe(0)
    expect(tall.placed.get('pve')!.x).toBe(0)
    expect(y('pve')).toBeGreaterThanOrEqual(bottom('e') + CANVAS.blockGap)
    expect(y('f')).toBe(bottom('pve', 'a', 'b', 'c', 'd') + CANVAS.blockGap)
  })

  it('breaks into rows where one row would be shown smaller', () => {
    // Four tall boxes in a square area: two rows of two.
    const four = ['p', 'q', 'r', 's'].map((id) => box(id, [title('stacks'), ...tiles('s', 8)]))
    const result = layoutBoxes(four, [], 1).placed
    const [p1, p2, p3, p4] = four.map((bx) => result.get(bx.id)!)
    expect([p1.y, p2.y]).toEqual([0, 0])
    expect(p3.y).toBe(p1.height + CANVAS.blockGap)
    expect(p4.y).toBe(p3.y)
    expect([p1.x, p3.x]).toEqual([0, 0])
    expect([p2.x, p4.x]).toEqual([CANVAS.boxWidth + CANVAS.blockGap, CANVAS.boxWidth + CANVAS.blockGap])
  })

  it('is never shown smaller than one row or one column would be', () => {
    const extent = (l: Layout) => {
      const all = [...l.placed.values()]
      return { width: Math.max(...all.map((p) => p.x + p.width)), height: Math.max(...all.map((p) => p.y + p.height)) }
    }
    const scale = (l: Layout, aspect: number) => Math.min(aspect / extent(l).width, 1 / extent(l).height)
    for (const aspect of [0.4, 1, 1.6, 2.5, 4]) {
      const chosen = scale(layoutBoxes(boxes, links, aspect), aspect)
      expect(chosen).toBeGreaterThanOrEqual(scale(layoutBoxes(boxes, links, WIDE), aspect))
      expect(chosen).toBeGreaterThanOrEqual(scale(layoutBoxes(boxes, links, TALL), aspect))
    }
  })

  it('takes a shape that is no shape as a narrow one', () => {
    const narrow = layoutBoxes(boxes, links, TALL)
    for (const aspect of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) expect(layoutBoxes(boxes, links, aspect)).toEqual(narrow)
  })

  it('gives the same answer for the same input', () => {
    expect(layoutBoxes(boxes, links, WIDE)).toEqual(layout)
    expect(layoutBoxes(boxes, links, 1.6)).toEqual(layoutBoxes(boxes, links, 1.6))
  })

  it('ignores a link to or from a box that is not drawn, to its own host, or from a tile that is not there', () => {
    const result = layoutBoxes([d, f], [
      { host: 'd', tile: 's0', node: 'gone' },
      { host: 'gone', tile: 'x', node: 'f' },
      { host: 'd', tile: 's0', node: 'd' },
      { host: 'd', tile: 'nope', node: 'f' },
    ], TALL)
    expect([...result.placed.values()].map((p) => p.column)).toEqual([0, 0])
    expect(result.routes.size).toBe(0)
  })

  it('draws a loop of links as hosts and guests without one, and ends', () => {
    const x = box('x', [tile('y-vm')])
    const y = box('y', [tile('x-vm')])
    const result = layoutBoxes([x, y], [{ host: 'x', tile: 'y-vm', node: 'y' }, { host: 'y', tile: 'x-vm', node: 'x' }], TALL)
    expect(result.placed.get('x')!.column).toBe(0)
    expect(result.placed.get('y')!.column).toBe(1)
    expect([...result.routes.keys()]).toEqual([linkKey('x', 'y-vm')])
  })

  it('takes the first host given for a server two hosts claim', () => {
    const h1 = box('h1', [tile('g')])
    const h2 = box('h2', [tile('g')])
    const g = box('g')
    const result = layoutBoxes([h1, h2, g], [{ host: 'h2', tile: 'g', node: 'g' }, { host: 'h1', tile: 'g', node: 'g' }], WIDE)
    expect(result.placed.get('g')!.x).toBe(result.placed.get('h2')!.x + CANVAS.boxWidth + CANVAS.columnGap)
    expect([...result.routes.keys()]).toEqual([linkKey('h2', 'g')])
  })

  it('lays out nothing as nothing', () => {
    expect(layoutBoxes([], [], 1.6)).toEqual({ placed: new Map(), routes: new Map() })
  })
})

describe('edgePath', () => {
  const route = { channel: 30, lane: 20 }

  it('turns at the channel and the lane, and ends on the target', () => {
    expect(edgePoints(100, 200, 400, 50, route)).toEqual([[100, 200], [130, 200], [130, 30], [400, 30], [400, 50]])
    const d = edgePath(100, 200, 400, 50, route)
    expect(d.startsWith('M 100 200')).toBe(true)
    expect(d.endsWith('L 400 50')).toBe(true)
  })

  it('writes no number that is not one, where two corners fall on the same point', () => {
    // The tile level with the lane, and the target right above the channel.
    for (const d of [edgePath(100, 30, 400, 50, route), edgePath(100, 200, 130, 50, route), edgePath(0, 0, 0, 0, { channel: 0, lane: 0 })]) {
      expect(d).not.toMatch(/NaN|Infinity/)
    }
  })
})

describe('stored positions', () => {
  it('reads what was stored, by node id', () => {
    expect(readPositions('{"a":{"x":10,"y":-20.5},"b":{"x":0,"y":0}}')).toEqual({ a: { x: 10, y: -20.5 }, b: { x: 0, y: 0 } })
  })

  it('ignores a value it cannot read, rather than failing on it', () => {
    expect(readPositions(null)).toEqual({})
    expect(readPositions('')).toEqual({})
    expect(readPositions('{not json')).toEqual({})
    expect(readPositions('[{"x":1,"y":2}]')).toEqual({})
    expect(readPositions('"text"')).toEqual({})
    expect(readPositions('null')).toEqual({})
  })

  it('drops an entry that is not a place', () => {
    expect(readPositions('{"a":{"x":1},"b":{"x":"1","y":2},"c":5,"d":{"x":1,"y":2},"e":{"x":1e999,"y":0}}')).toEqual({ d: { x: 1, y: 2 } })
  })

  it('keeps the places of nodes that still exist, and only those', () => {
    const saved = { a: { x: 1, y: 2 }, gone: { x: 3, y: 4 } }
    expect(keepPositions(saved, ['a', 'new'])).toEqual({ a: { x: 1, y: 2 } })
  })
})
