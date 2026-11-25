import { memo } from 'react'
import { useTranslation } from 'react-i18next'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { AlertTriangle } from 'lucide-react'
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
const tileClass = 'nodrag nopan relative flex items-start justify-between gap-2 rounded-lg border-solid border-border bg-background'
// The state word at the height the rows are sized for: the pill's own line is taller.
const pillClass = 'shrink-0 leading-4'
const tileAttention = 'border-destructive/40 bg-destructive/5'
const nameClass = 'min-w-0 break-words text-[14px] font-semibold leading-5'
const linkClass = 'outline-none hover:underline after:absolute after:inset-0 after:rounded-lg focus-visible:after:ring-2 focus-visible:after:ring-ring'

function Tile({ row, nodeId, scope, linked }: {
  row: Extract<CanvasRow, { kind: 'tile' }>
  nodeId: string
  scope: Scope
  linked: boolean
}) {
  const { t } = useTranslation()
  const { item } = row
  const lines = nameLines(row.name, CANVAS.tileNameChars)
  const style = { height: rowHeight(row), padding: `${CANVAS.tilePadding}px ${CANVAS.tileInset}px`, borderWidth: CANVAS.tileBorder }
  const name = cn(nameClass, CLAMP[lines])

  if (item.type === 'guest') {
    const word = guestWord(item.guest, t)
    return (
      <li className={tileClass} style={style}>
        <p className={name} title={row.name}>{row.name}</p>
        <StatusPill tone={word.tone} className={pillClass}>{word.text}</StatusPill>
        {linked && <Handle type="source" position={Position.Right} id={row.key} isConnectable={false} />}
      </li>
    )
  }
  const attention = item.attention
  const word = item.type === 'stack' ? stackWord(item.stack, attention, t) : containerWord(item.container, t)
  const target = item.type === 'stack' ? stackTarget(scope, item.stack) : containerTarget(scope, item.container.id)
  return (
    <li className={cn(tileClass, attention && tileAttention)} style={style}>
      <NodeLink target={target} nodeId={nodeId} className={cn(name, linkClass)} title={row.name}>{row.name}</NodeLink>
      <StatusPill tone={word.tone} className={pillClass}>{word.text}</StatusPill>
      {/* The board says what is wrong in a list above it; here the tile has
          to, and not by its red edge alone: a mark on its corner, and the
          words for whoever does not see it. It takes no room from the name. */}
      {attention && (
        <span className="pointer-events-none absolute -left-1.5 -top-1.5 flex size-4 items-center justify-center rounded-full bg-card text-destructive">
          <AlertTriangle className="size-3.5" aria-hidden="true" />
          <span className="sr-only">{t('running.totals.attention')}</span>
        </span>
      )}
    </li>
  )
}

function Note({ note, at }: { note: NoteKind; at?: number }) {
  const { t, i18n } = useTranslation()
  const text = {
    notAsked: t('running.node.notAsked'),
    loading: t('common.loading'),
    loadError: t('running.node.loadError'),
    noDocker: t('running.node.noDocker'),
    empty: t('running.empty.title'),
    guestsError: t('running.canvas.guestsError'),
    stale: t('running.node.refreshError', { time: at === undefined ? '' : new Date(at).toLocaleTimeString(i18n.language) }),
  }[note]
  const failed = note === 'loadError' || note === 'guestsError' || note === 'stale'
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
  const { section, rows, scope, level, linked } = data
  const { node, kind, metrics } = section
  const lines = nameLines(node.name, CANVAS.headerNameChars)
  const groupLevel = Math.min(level + 1, 4) as HeadingLevel
  const pad = CANVAS.boxPadding
  return (
    <div
      className="relative flex flex-col rounded-2xl border-solid border-border bg-card text-card-foreground card-shadow"
      style={{ width: CANVAS.boxWidth, height: boxHeight({ id: node.id, name: node.name, rows }), padding: pad, borderWidth: CANVAS.boxBorder }}
    >
      {/* The edge of the host this server is a guest of comes down into the top of the box. */}
      <Handle type="target" position={Position.Top} id="in" isConnectable={false} style={{ left: CANVAS.anchorX }} />
      <div style={{ height: lines * CANVAS.nameLineHeight }}>
        <Heading level={level} className={cn('break-words text-[17px] font-semibold leading-[22px]', CLAMP[lines])}>
          <span title={node.name}>{node.name}</span>
        </Heading>
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
            if (row.kind === 'note') return <Note key={row.key} note={row.note} at={row.at} />
            return <Tile key={row.key} row={row} nodeId={node.id} scope={scope} linked={linked.has(row.key)} />
          })}
        </ul>
      )}
    </div>
  )
}

export const ServerBox = memo(ServerBoxView)
