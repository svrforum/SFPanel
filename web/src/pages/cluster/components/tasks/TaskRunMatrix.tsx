import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { LayoutDashboard, SquareTerminal } from 'lucide-react'
import { StatusPill } from '@/components/StatusPill'
import { cn } from '@/lib/utils'
import type { RunRow } from './taskRunProgress'
import { cellText, cellTone, nodeStatusText, nodeStatusTone } from './taskRunViewModel'

/** The node and step whose output is open. */
export interface ChosenCell {
  node: string
  step: number
}

interface TaskRunMatrixProps {
  rows: RunRow[]
  /** A header per column, in step order. */
  steps: string[]
  chosen: ChosenCell | null
  /** A cell chosen: its node and step. */
  onChoose: (node: string, step: number) => void
  /** A node's name chosen: the page picks its step. */
  onChooseNode: (node: string) => void
  /** The first column's header: "Node" unless the rows are something else (an install's servers). */
  rowsHeader?: string
  /** Where a row's server opens: its terminal and its dashboard; null for a row with nowhere to go. Absent, no row has links. */
  serverLinks?: (rowId: string) => { terminal: string; open: string } | null
}

const isChosen = (chosen: ChosenCell | null, node: string, step: number) => chosen?.node === node && chosen.step === step

const ICON_LINK = 'inline-flex size-6 items-center justify-center rounded-md text-muted-foreground hover:bg-secondary hover:text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring'
const TEXT_LINK = 'inline-flex min-h-10 items-center gap-1.5 rounded-md text-[13px] font-medium text-primary hover:underline outline-none focus-visible:ring-2 focus-visible:ring-ring'

// The run as a node × step table on a desktop, its first column held in place
// while a long run scrolls sideways; on a phone, a card per node with its
// steps listed. Every cell is a button that opens that step's output. Why a
// node stopped or failed is written under its name in both, and a row the
// page gives links to leads to that server's terminal and dashboard.
export function TaskRunMatrix({ rows, steps, chosen, onChoose, onChooseNode, rowsHeader, serverLinks }: TaskRunMatrixProps) {
  const { t } = useTranslation()
  const label = (row: RunRow, i: number) =>
    t('cluster.tasks.run.cellLabel', { node: row.name, step: steps[i], state: cellText(row.cells[i], t) })
  const toTerminal = t('cluster.tasks.run.toTerminal')
  const toServer = t('cluster.tasks.run.toServer')
  const linkLabel = (action: string, row: RunRow) => t('cluster.tasks.run.linkFor', { action, name: row.name })

  return (
    <>
      <div data-testid="task-run-matrix" className="hidden md:block max-h-[70vh] overflow-auto rounded-xl border border-border">
        <table className="border-separate border-spacing-0 text-[12px]">
          <thead>
            <tr>
              <th scope="col" className="sticky top-0 left-0 z-20 bg-card px-3 py-2 text-left font-semibold border-b border-r border-border">
                {rowsHeader ?? t('cluster.tasks.run.node')}
              </th>
              {steps.map((name, i) => (
                <th
                  key={i}
                  scope="col"
                  title={name}
                  className="sticky top-0 z-10 bg-card px-2 py-2 text-left font-semibold border-b border-border max-w-[160px] truncate"
                >
                  {name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const links = serverLinks?.(row.id)
              return (
                <tr key={row.id}>
                  <th scope="row" className="sticky left-0 z-10 bg-card px-3 py-1.5 text-left font-normal border-r border-border">
                    <div className={cn('flex items-center gap-2 min-w-[160px]', links ? 'max-w-[280px]' : 'max-w-[220px]')}>
                      <button
                        type="button"
                        onClick={() => onChooseNode(row.id)}
                        className={cn('truncate text-[13px] font-medium hover:underline', chosen?.node === row.id && 'text-primary')}
                      >
                        {row.name}
                      </button>
                      <StatusPill tone={nodeStatusTone(row.status)} className="shrink-0">{nodeStatusText(row.status, t)}</StatusPill>
                      {links && (
                        <span className="ml-auto flex shrink-0 items-center">
                          <Link to={links.terminal} aria-label={linkLabel(toTerminal, row)} title={toTerminal} className={ICON_LINK}>
                            <SquareTerminal className="size-3.5" aria-hidden="true" />
                          </Link>
                          <Link to={links.open} aria-label={linkLabel(toServer, row)} title={toServer} className={ICON_LINK}>
                            <LayoutDashboard className="size-3.5" aria-hidden="true" />
                          </Link>
                        </span>
                      )}
                    </div>
                    {row.reason && <p className="mt-0.5 max-w-[220px] text-[12px] text-muted-foreground break-words">{row.reason}</p>}
                  </th>
                  {row.cells.map((c, i) => (
                    <td key={i} data-cell={`${row.id}/${i}`} className="px-1.5 py-1.5">
                      <button
                        type="button"
                        aria-label={label(row, i)}
                        aria-pressed={isChosen(chosen, row.id, i)}
                        onClick={() => onChoose(row.id, i)}
                        className={cn('rounded-full', isChosen(chosen, row.id, i) && 'ring-2 ring-primary ring-offset-1 ring-offset-card')}
                      >
                        <StatusPill tone={cellTone(c)} className="whitespace-nowrap">{cellText(c, t)}</StatusPill>
                      </button>
                    </td>
                  ))}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      <ul className="md:hidden space-y-2">
        {rows.map((row) => {
          const links = serverLinks?.(row.id)
          return (
            <li key={row.id} data-testid="task-run-node-card" className="rounded-xl border border-border p-3 space-y-2">
              <div className="flex items-center gap-2">
                <span className="flex-1 min-w-0 break-words text-[14px] font-semibold">{row.name}</span>
                <StatusPill tone={nodeStatusTone(row.status)}>{nodeStatusText(row.status, t)}</StatusPill>
              </div>
              {links && (
                <div className="flex flex-wrap gap-x-4">
                  <Link to={links.terminal} aria-label={linkLabel(toTerminal, row)} className={TEXT_LINK}>
                    <SquareTerminal className="size-4" aria-hidden="true" />
                    {toTerminal}
                  </Link>
                  <Link to={links.open} aria-label={linkLabel(toServer, row)} className={TEXT_LINK}>
                    <LayoutDashboard className="size-4" aria-hidden="true" />
                    {toServer}
                  </Link>
                </div>
              )}
              {row.reason && <p className="text-[12px] text-muted-foreground break-words">{row.reason}</p>}
              <ol className="space-y-1">
                {row.cells.map((c, i) => (
                  <li key={i}>
                    <button
                      type="button"
                      aria-pressed={isChosen(chosen, row.id, i)}
                      onClick={() => onChoose(row.id, i)}
                      className={cn(
                        'flex w-full min-h-10 items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[13px] hover:bg-secondary/60',
                        isChosen(chosen, row.id, i) && 'bg-primary/10 ring-1 ring-primary/20',
                      )}
                    >
                      <span className="flex-1 min-w-0 break-words">{steps[i]}</span>
                      <StatusPill tone={cellTone(c)} className="shrink-0">{cellText(c, t)}</StatusPill>
                    </button>
                  </li>
                ))}
              </ol>
            </li>
          )
        })}
      </ul>
    </>
  )
}
