import { describe, expect, it } from 'vitest'
import {
  CANVAS,
  anchorY,
  boxHeight,
  headerHeight,
  keepPositions,
  layoutBoxes,
  nameLines,
  readPositions,
  rowHeight,
  rowTop,
  type BoxRow,
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
const columnX = (column: number) => column * (CANVAS.boxWidth + CANVAS.columnGap)

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
    expect(boxHeight(box('a'))).toBe(2 * CANVAS.boxPadding + headerHeight('a'))
    expect(headerHeight('a')).toBe(CANVAS.nameLineHeight + 2 * CANVAS.headerRowHeight)
    expect(headerHeight('n'.repeat(CANVAS.headerNameChars + 1))).toBe(2 * CANVAS.nameLineHeight + 2 * CANVAS.headerRowHeight)
  })

  it('adds each row and the gaps between them', () => {
    const rows = [title('stacks'), tile('s1'), tile('s2', 'x'.repeat(CANVAS.tileNameChars + 1)), note('n')]
    const sum = rowHeight(rows[0]) + rowHeight(rows[1]) + rowHeight(rows[2]) + rowHeight(rows[3])
    expect(boxHeight(box('a', rows))).toBe(2 * CANVAS.boxPadding + headerHeight('a') + CANVAS.headerGap + sum + 3 * CANVAS.rowGap)
  })

  it('gives each kind of row its own height, a two-line tile taller than a one-line one', () => {
    expect(rowHeight(title('t'))).toBe(CANVAS.titleHeight)
    expect(rowHeight(note('n'))).toBe(CANVAS.noteHeight)
    const one = rowHeight(tile('a'))
    const two = rowHeight(tile('b', 'x'.repeat(CANVAS.tileNameChars + 1)))
    expect(one).toBe(2 * CANVAS.tilePadding + Math.max(CANVAS.pillHeight, CANVAS.tileLineHeight))
    expect(two).toBe(2 * CANVAS.tilePadding + Math.max(CANVAS.pillHeight, 2 * CANVAS.tileLineHeight))
    expect(two).toBeGreaterThan(one)
  })

  // The numbers the drawing is built on today, so a change to them is a
  // change someone meant.
  it('pins the sizes as they are drawn', () => {
    expect(boxHeight(box('alpha'))).toBe(92)
    expect(boxHeight(box('alpha', [title('stacks'), tile('media'), tile('photos')]))).toBe(210)
  })

  it('tells where a row starts in its box', () => {
    const b = box('a', [title('g'), tile('vm-1'), tile('vm-2')])
    const first = CANVAS.boxPadding + headerHeight('a') + CANVAS.headerGap
    expect(rowTop(b, 'g')).toBe(first)
    expect(rowTop(b, 'vm-1')).toBe(first + CANVAS.titleHeight + CANVAS.rowGap)
    expect(rowTop(b, 'vm-2')).toBe(first + CANVAS.titleHeight + rowHeight(tile('vm-1')) + 2 * CANVAS.rowGap)
    expect(rowTop(b, 'missing')).toBeNull()
  })
})

describe('layoutBoxes', () => {
  // pve hosts vm-a (node a) and vm-b (node b); a hosts inner (node c).
  // d and e are no one's guests.
  const pve = box('pve', [title('guests'), tile('vm-a'), tile('vm-b'), tile('vm-x')])
  const a = box('a', [title('guests'), tile('c-vm'), title('stacks'), ...tiles('s', 4)])
  const b = box('b', [title('stacks'), ...tiles('s', 2)])
  const c = box('c', [note('nodocker')])
  const d = box('d', [title('stacks'), ...tiles('s', 6)])
  const e = box('e')
  const boxes = [d, pve, a, b, c, e]
  const links: LayoutLink[] = [
    { host: 'pve', tile: 'vm-a', node: 'a' },
    { host: 'pve', tile: 'vm-b', node: 'b' },
    { host: 'a', tile: 'c-vm', node: 'c' },
  ]
  const placed = layoutBoxes(boxes, links)

  it('places every box, in the column of its depth: hosts first, then their guests', () => {
    expect([...placed.keys()].sort()).toEqual(['a', 'b', 'c', 'd', 'e', 'pve'])
    const columns = Object.fromEntries([...placed].map(([id, p]) => [id, p.column]))
    expect(columns).toEqual({ d: 0, pve: 0, e: 0, a: 1, b: 1, c: 2 })
    for (const p of placed.values()) expect(p.x).toBe(columnX(p.column))
  })

  it('stacks the first column in the order given, a gap apart', () => {
    const [pd, pp, pe] = [placed.get('d')!, placed.get('pve')!, placed.get('e')!]
    expect(pd.y).toBe(0)
    expect(pp.y).toBe(pd.y + pd.height + CANVAS.boxGap)
    expect(pe.y).toBe(pp.y + pp.height + CANVAS.boxGap)
  })

  it('sizes each box as boxHeight does', () => {
    for (const bx of boxes) expect(placed.get(bx.id)).toMatchObject({ width: CANVAS.boxWidth, height: boxHeight(bx) })
  })

  it('lets no two boxes overlap', () => {
    const all = [...placed.values()]
    for (let i = 0; i < all.length; i++) {
      for (let j = i + 1; j < all.length; j++) expect(overlaps(all[i], all[j])).toBe(false)
    }
  })

  it("puts a guest's server to the right of its host", () => {
    for (const l of links) expect(placed.get(l.node)!.x).toBeGreaterThanOrEqual(placed.get(l.host)!.x + CANVAS.boxWidth + CANVAS.columnGap)
  })

  it("puts a guest's server level with its guest tile where there is room", () => {
    // a's header is level with the vm-a tile of pve: its edge runs straight.
    const pp = placed.get('pve')!
    const tileMiddle = pp.y + rowTop(pve, 'vm-a')! + rowHeight(tile('vm-a')) / 2
    expect(placed.get('a')!.y + anchorY(a)).toBe(tileMiddle)
  })

  it('pushes a server down when the one above it leaves no room, a gap below', () => {
    // b wants to be level with vm-b, which is just under vm-a, but a is tall.
    const pa = placed.get('a')!
    expect(placed.get('b')!.y).toBe(pa.y + pa.height + CANVAS.boxGap)
  })

  it('orders a column by where its servers want to be, not by the order given', () => {
    const h = box('h', [tile('t1'), tile('t2')])
    const p1 = box('p1')
    const q1 = box('q1')
    const result = layoutBoxes([h, q1, p1], [{ host: 'h', tile: 't1', node: 'p1' }, { host: 'h', tile: 't2', node: 'q1' }])
    const [pp, pq] = [result.get('p1')!, result.get('q1')!]
    expect(pp.y + anchorY(p1)).toBe(rowTop(h, 't1')! + rowHeight(tile('t1')) / 2)
    expect(pq.y).toBe(pp.y + pp.height + CANVAS.boxGap)
  })

  it('gives the same answer for the same input', () => {
    expect(layoutBoxes(boxes, links)).toEqual(placed)
  })

  it('ignores a link to or from a box that is not drawn, to its own host, or from a tile that is not there', () => {
    const result = layoutBoxes([d, e], [
      { host: 'd', tile: 's0', node: 'gone' },
      { host: 'gone', tile: 'x', node: 'e' },
      { host: 'd', tile: 's0', node: 'd' },
      { host: 'd', tile: 'nope', node: 'e' },
    ])
    expect([...result.values()].map((p) => p.column)).toEqual([0, 0])
  })

  it('draws a loop of links as hosts and guests without one, and ends', () => {
    const x = box('x', [tile('y-vm')])
    const y = box('y', [tile('x-vm')])
    const result = layoutBoxes([x, y], [{ host: 'x', tile: 'y-vm', node: 'y' }, { host: 'y', tile: 'x-vm', node: 'x' }])
    expect(result.get('x')!.column).toBe(0)
    expect(result.get('y')!.column).toBe(1)
  })

  it('takes the first host given for a server two hosts claim', () => {
    const h1 = box('h1', [tile('g')])
    const h2 = box('h2', [tile('g')])
    const g = box('g')
    const result = layoutBoxes([h1, h2, g], [{ host: 'h2', tile: 'g', node: 'g' }, { host: 'h1', tile: 'g', node: 'g' }])
    const tileMiddle = result.get('h2')!.y + rowTop(h2, 'g')! + rowHeight(tile('g')) / 2
    expect(result.get('g')!.y + anchorY(g)).toBe(tileMiddle)
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
