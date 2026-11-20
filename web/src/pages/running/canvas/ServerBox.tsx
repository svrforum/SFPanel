import { memo } from 'react'
import { useTranslation } from 'react-i18next'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { StatusPill } from '@/components/StatusPill'
import { CANVAS, boxHeight, nameLines, rowHeight } from '@/lib/runningLayout'
import { cn } from '@/lib/utils'
import { KindBadge } from '@/pages/cluster/components/KindBadge'
import { NodeStatus } from '@/pages/cluster/components/NodeCard'
import { Heading, type HeadingLevel } from '../Heading'
import { NodeLink } from '../NodeLink'
import { containerTarget, stackTarget, type Scope } from '../targets'
import { containerWord, guestWord, stackWord } from '../words'
import type { CanvasRow, NoteKind, ServerNode } from './boxes'

// Class names Tailwind can see, for the line counts a name is given.
const CLAMP: Record<number, string> = { 1: 'line-clamp-1', 2: 'line-clamp-2' }

// A tile is a link or plain text, never a place to drag the box from or pan
// the canvas from: nodrag and nopan are the library's names for that.
const tileClass = 'nodrag nopan relative flex items-start justify-between gap-2 rounded-lg border border-border bg-background'
const tileAttention = 'border-destructive/40 bg-destructive/5'
const nameClass = 'min-w-0 break-words text-[13px] font-semibold leading-[18px]'
const linkClass = 'outline-none hover:underline after:absolute after:inset-0 after:rounded-lg focus-visible:after:ring-2 focus-visible:after:ring-ring'

function Tile({ row, nodeId, scope, localId, linked }: {
  row: Extract<CanvasRow, { kind: 'tile' }>
  nodeId: string
  scope: Scope
  localId: string
  linked: boolean
}) {
  const { t } = useTranslation()
  const { item } = row
  const lines = nameLines(row.name, CANVAS.tileNameChars)
  const style = { height: rowHeight(row), padding: CANVAS.tilePadding }
  const name = cn(nameClass, CLAMP[lines])

  if (item.type === 'guest') {
    const word = guestWord(item.guest, t)
    return (
      <li className={tileClass} style={style}>
        <p className={name} title={row.name}>{row.name}</p>
        <StatusPill tone={word.tone} className="shrink-0">{word.text}</StatusPill>
        {linked && <Handle type="source" position={Position.Right} id={row.key} isConnectable={false} />}
      </li>
    )
  }
  const attention = item.attention
  const word = item.type === 'stack' ? stackWord(item.stack, attention, t) : containerWord(item.container, t)
  const target = item.type === 'stack' ? stackTarget(scope, nodeId, item.stack) : containerTarget(scope, item.container.id)
  return (
    <li className={cn(tileClass, attention && tileAttention)} style={style}>
      <NodeLink target={target} nodeId={nodeId} localId={localId} className={cn(name, linkClass)} title={row.name}>{row.name}</NodeLink>
      <StatusPill tone={word.tone} className="shrink-0">{word.text}</StatusPill>
    </li>
  )
}

function Note({ note }: { note: NoteKind }) {
  const { t } = useTranslation()
  const text = {
    notAsked: t('running.node.notAsked'),
    loading: t('common.loading'),
    loadError: t('running.node.loadError'),
    noDocker: t('running.node.noDocker'),
    empty: t('running.empty.title'),
    guestsError: t('running.canvas.guestsError'),
  }[note]
  const failed = note === 'loadError' || note === 'guestsError'
  return (
    <li
      className={cn('line-clamp-2 break-words text-xs leading-[18px]', failed ? 'text-destructive' : 'text-muted-foreground')}
      style={{ height: CANVAS.noteHeight }}
      title={text}
    >
      {text}
    </li>
  )
}

function GroupTitle({ row, level }: { row: Extract<CanvasRow, { kind: 'title' }>; level: HeadingLevel }) {
  const { t } = useTranslation()
  const text = {
    guests: t('running.groups.guests', { count: row.count }),
    stacks: t('running.groups.stacks', { count: row.count }),
    standalone: t('running.groups.standalone', { count: row.count }),
  }[row.group]
  return (
    <li className="flex items-end" style={{ height: CANVAS.titleHeight }}>
      <Heading level={level} className="text-xs font-semibold text-muted-foreground">{text}</Heading>
    </li>
  )
}

/**
 * One server on the canvas: the board's node header and, under it, compact
 * tiles of its guests, stacks and standalone containers, in the board's words.
 * Every row is drawn at the height runningLayout gives it, which is what the
 * layout placed the box by.
 */
function ServerBoxView({ data }: NodeProps<ServerNode>) {
  const { t } = useTranslation()
  const { section, rows, scope, localId, level, linked } = data
  const { node, kind, metrics } = section
  const lines = nameLines(node.name, CANVAS.headerNameChars)
  const groupLevel = Math.min(level + 1, 4) as HeadingLevel
  const pad = CANVAS.boxPadding
  return (
    <div
      className="flex flex-col rounded-2xl border border-border bg-card text-card-foreground card-shadow"
      style={{ width: CANVAS.boxWidth, height: boxHeight({ id: node.id, name: node.name, rows }), padding: pad }}
    >
      {/* Spans the box's padding, so the incoming edge meets the box's own edge. */}
      <div className="relative" style={{ height: lines * CANVAS.nameLineHeight, marginInline: -pad, paddingInline: pad }}>
        <Heading level={level} className={cn('break-words text-[15px] font-semibold leading-5', CLAMP[lines])}>
          <span title={node.name}>{node.name}</span>
        </Heading>
        <Handle type="target" position={Position.Left} id="in" isConnectable={false} />
      </div>
      <div className="flex items-center gap-1.5 overflow-hidden" style={{ height: CANVAS.headerRowHeight }}>
        {kind && <KindBadge kind={kind} />}
        <NodeStatus status={node.status} />
      </div>
      <p className="flex items-center overflow-hidden whitespace-nowrap text-xs text-muted-foreground" style={{ height: CANVAS.headerRowHeight }}>
        {metrics && [
          `${t('running.node.cpu')} ${Math.round(metrics.cpu)}%`,
          `${t('running.node.memory')} ${Math.round(metrics.memory)}%`,
          `${t('running.node.disk')} ${Math.round(metrics.disk)}%`,
        ].join(' · ')}
      </p>
      {rows.length > 0 && (
        <ul className="flex flex-col" style={{ marginTop: CANVAS.headerGap, gap: CANVAS.rowGap }}>
          {rows.map((row) => {
            if (row.kind === 'title') return <GroupTitle key={row.key} row={row} level={groupLevel} />
            if (row.kind === 'note') return <Note key={row.key} note={row.note} />
            return <Tile key={row.key} row={row} nodeId={node.id} scope={scope} localId={localId} linked={linked.has(row.key)} />
          })}
        </ul>
      )}
    </div>
  )
}

export const ServerBox = memo(ServerBoxView)
