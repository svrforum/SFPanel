import '@xyflow/react/dist/style.css'
import './canvas.css'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type FocusEvent } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Background,
  BackgroundVariant,
  Controls,
  Panel,
  ReactFlow,
  ReactFlowProvider,
  applyNodeChanges,
  useReactFlow,
  useUpdateNodeInternals,
  type NodeChange,
  type OnNodeDrag,
} from '@xyflow/react'
import { Button } from '@/components/ui/button'
import { CANVAS, boxHeight, keepPositions, layoutBoxes, linkKey, readPositions, type Positions } from '@/lib/runningLayout'
import type { HeadingLevel } from '../Heading'
import type { Scope } from '../targets'
import type { BoardSection } from '../useRunningBoard'
import { boxRows, guestLinks, layoutBox, type CanvasRow, type ServerBoxData, type ServerNode } from './boxes'
import { GuestEdgeView, type GuestEdge } from './GuestEdge'
import { ServerBox } from './ServerBox'

const nodeTypes = { server: ServerBox }
const edgeTypes = { guest: GuestEdgeView }

const FIT = { padding: 0.12, maxZoom: 1 }

// The app's theme is the class on <html>; theme.ts announces every change of
// it, the operator's and the system's alike.
const subscribeTheme = (onChange: () => void) => {
  window.addEventListener('sfpanel:themechange', onChange)
  return () => window.removeEventListener('sfpanel:themechange', onChange)
}
const isDark = () => document.documentElement.classList.contains('dark')

const REDUCED = '(prefers-reduced-motion: reduce)'
const subscribeMotion = (onChange: () => void) => {
  const query = window.matchMedia(REDUCED)
  query.addEventListener('change', onChange)
  return () => query.removeEventListener('change', onChange)
}
const prefersReduced = () => window.matchMedia(REDUCED).matches

function readStored(key: string): Positions {
  try {
    return readPositions(localStorage.getItem(key))
  } catch {
    return {}
  }
}

function writeStored(key: string, positions: Positions | null) {
  try {
    if (positions && Object.keys(positions).length > 0) localStorage.setItem(key, JSON.stringify(positions))
    else localStorage.removeItem(key)
  } catch {
    // Storage refused: the boxes stay where they are for this visit.
  }
}

/** An element's width over its height; 0 for one with no size yet, which lays out as a narrow area. */
function shapeOf(el: HTMLElement | null): number {
  return el && el.clientHeight > 0 ? el.clientWidth / el.clientHeight : 0
}

/** "자동 정렬": forgets where the boxes were moved to, and fits the computed layout to the view. */
function ArrangePanel({ onArrange, duration }: { onArrange: () => void; duration: number }) {
  const { t } = useTranslation()
  const { fitView } = useReactFlow()
  return (
    <Panel position="top-right">
      <Button
        variant="outline"
        size="sm"
        className="rounded-xl bg-card"
        onClick={() => {
          onArrange()
          void fitView({ ...FIT, duration })
        }}
      >
        {t('running.canvas.arrange')}
      </Button>
    </Panel>
  )
}

interface CanvasProps {
  sections: BoardSection[]
  scope: Scope
  localId: string
  query: string
  /** Where the positions of moved boxes are kept: one key per scope. */
  storageKey: string
  /** The level of a box's heading. */
  level: HeadingLevel
}

/**
 * The running board drawn as a map: a box per server, the guests that are
 * servers joined to their boxes. It shows what the board shows and leads
 * where the board leads; it can be panned, zoomed and rearranged, and
 * nothing in it can be connected, edited or deleted.
 */
export default function RunningCanvas(props: CanvasProps) {
  return (
    <ReactFlowProvider>
      <Flow {...props} />
    </ReactFlowProvider>
  )
}

function Flow({ sections, scope, localId, query, storageKey, level }: CanvasProps) {
  const { t } = useTranslation()
  const { fitView, getViewport, setViewport } = useReactFlow()
  const dark = useSyncExternalStore(subscribeTheme, isDark)
  const reduced = useSyncExternalStore(subscribeMotion, prefersReduced)
  const duration = reduced ? 0 : 200

  // Laid out from everything there is, so a search shrinks boxes in place
  // instead of moving them; drawn from what the search leaves.
  const full = useMemo(() => new Map(sections.map((s) => [s.node.id, boxRows(s, '')])), [sections])
  const drawn = useMemo(
    () => (query.trim() ? new Map(sections.map((s) => [s.node.id, boxRows(s, query)])) : full),
    [sections, query, full],
  )
  const ids = useMemo(() => sections.map((s) => s.node.id), [sections])
  // The boxes are set out to the shape of the area they are drawn in: side by
  // side on a wide screen, one under the other on a phone. The shape is taken
  // when the canvas opens and when it is asked to arrange itself, not while
  // the window is being resized, when boxes would jump under the pointer.
  const frame = useRef<HTMLDivElement>(null)
  const [shape, setShape] = useState<number | null>(null)
  useLayoutEffect(() => setShape(shapeOf(frame.current)), [])
  const computed = useMemo(
    () => layoutBoxes(sections.map((s) => layoutBox(s, full.get(s.node.id)!)), guestLinks(sections, full), shape ?? 0),
    [sections, full, shape],
  )
  const links = useMemo(() => guestLinks(sections, drawn), [sections, drawn])

  const [saved, setSaved] = useState<Positions>(() => readStored(storageKey))

  const data = useMemo(() => {
    const linked = new Map<string, Set<string>>()
    for (const l of links) {
      const set = linked.get(l.host) ?? new Set<string>()
      set.add(l.tile)
      linked.set(l.host, set)
    }
    return new Map(sections.map((s): [string, ServerBoxData] => [s.node.id, {
      section: s,
      rows: drawn.get(s.node.id) ?? ([] as CanvasRow[]),
      scope,
      localId,
      level,
      linked: linked.get(s.node.id) ?? new Set(),
    }]))
  }, [sections, drawn, links, scope, localId, level])

  const base = useMemo<ServerNode[]>(() => sections.map((s) => {
    const d = data.get(s.node.id)!
    const at = saved[s.node.id] ?? computed.placed.get(s.node.id)!
    return {
      id: s.node.id,
      type: 'server',
      position: { x: at.x, y: at.y },
      data: d,
      width: CANVAS.boxWidth,
      height: boxHeight(layoutBox(s, d.rows)),
      draggable: true,
      selectable: false,
      connectable: false,
      deletable: false,
      focusable: false,
      ariaRole: 'group',
      ariaLabel: s.node.name,
      domAttributes: { 'aria-roledescription': t('running.canvas.box') },
    }
  }), [sections, data, saved, computed, t])

  // The library moves a box through these nodes while it is dragged. When the
  // boxes themselves change they are taken anew, keeping what the library
  // measured of each: a box handed over unmeasured is hidden until it is
  // measured again, which every refresh would do to every box.
  const [nodes, setNodes] = useState<ServerNode[]>(base)
  const [from, setFrom] = useState(base)
  if (from !== base) {
    setFrom(base)
    setNodes((prev) => base.map((n) => {
      const old = prev.find((p) => p.id === n.id)
      // Its size is the layout's own figure, which is what it is drawn at.
      return old?.measured && n.width && n.height ? { ...n, measured: { width: n.width, height: n.height } } : n
    }))
  }
  // An edge ends on a handle inside a box: on a guest's tile, which gets one
  // only once that guest is known to be a node, and moves with the rows
  // above it. The library reads a box's handles when it measures the box,
  // and measures it again only when its size changes. A tile that gained
  // its handle in a box of the same size was never read, and its edge was
  // never drawn: four visits in nine on a real cluster of six, where the
  // answer that names the node comes after the boxes are up. So the
  // handles are read again whenever the boxes change.
  const updateNodeInternals = useUpdateNodeInternals()
  useEffect(() => {
    updateNodeInternals(base.map((n) => n.id))
  }, [base, updateNodeInternals])

  // The canvas is up before the nodes have answered: the boxes are drawn
  // empty and fill in, and move when it turns out which guest is which
  // node. The view fitted once, to the empty boxes, showed the full ones
  // cut off. It follows the layout until the operator takes hold of it —
  // pans, zooms, moves a box, tabs to a tile out of view — and from then on
  // is theirs, until they ask for it arranged again. What a search leaves
  // out changes no place, so typing moves nothing.
  const held = useRef(false)
  const extent = useMemo(() => {
    let left = Infinity, top = Infinity, right = -Infinity, bottom = -Infinity
    for (const [id, p] of computed.placed) {
      const at = saved[id] ?? p
      left = Math.min(left, at.x)
      top = Math.min(top, at.y)
      right = Math.max(right, at.x + p.width)
      bottom = Math.max(bottom, at.y + p.height)
    }
    return [left, top, right, bottom].map(Math.round).join(',')
  }, [computed, saved])
  useEffect(() => {
    if (shape === null || held.current) return
    void fitView(FIT)
  }, [extent, shape, fitView])
  const onNodesChange = useCallback((changes: NodeChange<ServerNode>[]) => setNodes((prev) => applyNodeChanges(changes, prev)), [])

  const onNodeDragStop = useCallback<OnNodeDrag<ServerNode>>((_event, node) => {
    held.current = true
    const next = keepPositions({ ...saved, [node.id]: { x: node.position.x, y: node.position.y } }, ids)
    setSaved(next)
    writeStored(storageKey, next)
  }, [saved, ids, storageKey])

  const arrange = useCallback(() => {
    held.current = false
    setSaved({})
    writeStored(storageKey, null)
    setShape(shapeOf(frame.current))
  }, [storageKey])

  const names = useMemo(() => new Map(sections.map((s) => [s.node.id, s.node.name])), [sections])
  const edges = useMemo<GuestEdge[]>(() => links.map((l) => ({
    id: `${l.host}/${l.tile}`,
    source: l.host,
    sourceHandle: l.tile,
    target: l.node,
    targetHandle: 'in',
    type: 'guest',
    // How it goes round the boxes, as the layout worked it out.
    data: computed.routes.get(linkKey(l.host, l.tile)),
    animated: false,
    selectable: false,
    focusable: false,
    deletable: false,
    reconnectable: false,
    ariaLabel: t('running.canvas.edge', {
      host: names.get(l.host) ?? l.host,
      kind: l.guest.kind === 'lxc' ? 'LXC' : 'VM',
      guest: l.guest.title,
      node: names.get(l.node) ?? l.node,
    }),
    domAttributes: { 'aria-roledescription': t('running.canvas.edgeRole') },
  })), [links, names, computed, t])

  const ariaLabelConfig = useMemo(() => ({
    'controls.ariaLabel': t('running.canvas.controls'),
    'controls.zoomIn.ariaLabel': t('running.canvas.zoomIn'),
    'controls.zoomOut.ariaLabel': t('running.canvas.zoomOut'),
    'controls.fitView.ariaLabel': t('running.canvas.fitView'),
  }), [t])

  // A tile reached with Tab may lie outside the view: the view moves to it.
  const onFocus = useCallback((e: FocusEvent<HTMLDivElement>) => {
    const target = e.target as HTMLElement
    if (!target.closest('.react-flow__node')) return
    const frame = e.currentTarget.getBoundingClientRect()
    const r = target.getBoundingClientRect()
    const margin = 16
    const shift = (start: number, end: number, from: number, to: number) =>
      start < from + margin ? from + margin - start : end > to - margin ? to - margin - end : 0
    const dx = shift(r.left, r.right, frame.left, frame.right)
    const dy = shift(r.top, r.bottom, frame.top, frame.bottom)
    if (dx === 0 && dy === 0) return
    held.current = true
    const v = getViewport()
    void setViewport({ x: v.x + dx, y: v.y + dy, zoom: v.zoom }, { duration })
  }, [getViewport, setViewport, duration])

  return (
    <div ref={frame} className="running-canvas h-[70dvh] min-h-80 w-full overflow-hidden rounded-2xl card-shadow" onFocus={onFocus}>
      {/* Not before the area's shape is known: the first view is fitted to the layout made for it. */}
      {shape !== null && <ReactFlow<ServerNode, GuestEdge>
        aria-label={t('running.canvas.label')}
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onNodeDragStop={onNodeDragStop}
        // A move the library made itself (fitting the view) comes with no event.
        onMoveStart={(event) => { if (event) held.current = true }}
        colorMode={dark ? 'dark' : 'light'}
        fitView
        fitViewOptions={FIT}
        minZoom={0.2}
        maxZoom={1.5}
        nodesDraggable
        nodesConnectable={false}
        nodesFocusable={false}
        edgesFocusable={false}
        edgesReconnectable={false}
        elementsSelectable={false}
        deleteKeyCode={null}
        selectionKeyCode={null}
        multiSelectionKeyCode={null}
        // Nothing here is selected or moved by keys, so the library's
        // keyboard help ("press enter to select…") would be untrue.
        disableKeyboardA11y
        ariaLabelConfig={ariaLabelConfig}
        // The credit is drawn below, in the panel's own size and words.
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1.5} />
        <Controls
          showInteractive={false}
          fitViewOptions={{ ...FIT, duration }}
          // Zoomed by hand, the view is the operator's; fitted by hand, it follows the layout again.
          onZoomIn={() => { held.current = true }}
          onZoomOut={() => { held.current = true }}
          onFitView={() => { held.current = false }}
        />
        <ArrangePanel onArrange={arrange} duration={duration} />
        <Panel position="bottom-right" className="!m-2">
          <a
            href="https://reactflow.dev"
            target="_blank"
            rel="noopener noreferrer"
            className="rounded-sm text-xs text-muted-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            React Flow<span className="sr-only"> ({t('running.port.newTab')})</span>
          </a>
        </Panel>
      </ReactFlow>}
    </div>
  )
}
