import '@xyflow/react/dist/style.css'
import './canvas.css'
import { useCallback, useMemo, useState, useSyncExternalStore, type FocusEvent } from 'react'
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
  type Edge,
  type NodeChange,
  type OnNodeDrag,
} from '@xyflow/react'
import { Button } from '@/components/ui/button'
import { CANVAS, boxHeight, keepPositions, layoutBoxes, readPositions, type Positions } from '@/lib/runningLayout'
import type { HeadingLevel } from '../Heading'
import type { Scope } from '../targets'
import type { BoardSection } from '../useRunningBoard'
import { boxRows, guestLinks, layoutBox, type CanvasRow, type ServerBoxData, type ServerNode } from './boxes'
import { ServerBox } from './ServerBox'

const nodeTypes = { server: ServerBox }

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
  const { getViewport, setViewport } = useReactFlow()
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
  const computed = useMemo(
    () => layoutBoxes(sections.map((s) => layoutBox(s, full.get(s.node.id)!)), guestLinks(sections, full)),
    [sections, full],
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
    const at = saved[s.node.id] ?? computed.get(s.node.id)!
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
  // measured of a box whose contents did not change.
  const [nodes, setNodes] = useState<ServerNode[]>(base)
  const [from, setFrom] = useState(base)
  if (from !== base) {
    setFrom(base)
    setNodes((prev) => base.map((n) => {
      const old = prev.find((p) => p.id === n.id)
      return old && old.data === n.data && old.measured ? { ...n, measured: old.measured } : n
    }))
  }
  const onNodesChange = useCallback((changes: NodeChange<ServerNode>[]) => setNodes((prev) => applyNodeChanges(changes, prev)), [])

  const onNodeDragStop = useCallback<OnNodeDrag<ServerNode>>((_event, node) => {
    const next = keepPositions({ ...saved, [node.id]: { x: node.position.x, y: node.position.y } }, ids)
    setSaved(next)
    writeStored(storageKey, next)
  }, [saved, ids, storageKey])

  const arrange = useCallback(() => {
    setSaved({})
    writeStored(storageKey, null)
  }, [storageKey])

  const names = useMemo(() => new Map(sections.map((s) => [s.node.id, s.node.name])), [sections])
  const edges = useMemo<Edge[]>(() => links.map((l) => ({
    id: `${l.host}/${l.tile}`,
    source: l.host,
    sourceHandle: l.tile,
    target: l.node,
    targetHandle: 'in',
    type: 'smoothstep',
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
  })), [links, names, t])

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
    const v = getViewport()
    void setViewport({ x: v.x + dx, y: v.y + dy, zoom: v.zoom }, { duration })
  }, [getViewport, setViewport, duration])

  return (
    <div className="running-canvas h-[70dvh] min-h-80 w-full overflow-hidden rounded-2xl card-shadow" onFocus={onFocus}>
      <ReactFlow<ServerNode>
        aria-label={t('running.canvas.label')}
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onNodeDragStop={onNodeDragStop}
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
        <Controls showInteractive={false} fitViewOptions={{ ...FIT, duration }} />
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
      </ReactFlow>
    </div>
  )
}
