import { useTranslation } from 'react-i18next'
import { StatusPill } from '@/components/StatusPill'
import { cn } from '@/lib/utils'
import type { RunRow } from './taskRunProgress'
import { cellText, cellTone, nodeStatusText, nodeStatusTone } from './taskRunView'

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
}

const isChosen = (chosen: ChosenCell | null, node: string, step: number) => chosen?.node === node && chosen.step === step

// The run as a node × step table on a desktop, its first column held in place
// while a long run scrolls sideways; on a phone, a card per node with its
// steps listed. Every cell is a button that opens that step's output.
export function TaskRunMatrix({ rows, steps, chosen, onChoose, onChooseNode, rowsHeader }: TaskRunMatrixProps) {
  const { t } = useTranslation()
  const label = (row: RunRow, i: number) =>
    t('cluster.tasks.run.cellLabel', { node: row.name, step: steps[i], state: cellText(row.cells[i], t) })

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
            {rows.map((row) => (
              <tr key={row.id}>
                <th scope="row" className="sticky left-0 z-10 bg-card px-3 py-1.5 text-left font-normal border-r border-border">
                  <div className="flex items-center gap-2 min-w-[160px] max-w-[220px]">
                    <button
                      type="button"
                      onClick={() => onChooseNode(row.id)}
                      title={row.reason}
                      className={cn('truncate text-[13px] font-medium hover:underline', chosen?.node === row.id && 'text-primary')}
                    >
                      {row.name}
                    </button>
                    <StatusPill tone={nodeStatusTone(row.status)} className="shrink-0">{nodeStatusText(row.status, t)}</StatusPill>
                  </div>
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
            ))}
          </tbody>
        </table>
      </div>

      <ul className="md:hidden space-y-2">
        {rows.map((row) => (
          <li key={row.id} data-testid="task-run-node-card" className="rounded-xl border border-border p-3 space-y-2">
            <div className="flex items-center gap-2">
              <span className="flex-1 min-w-0 truncate text-[14px] font-semibold">{row.name}</span>
              <StatusPill tone={nodeStatusTone(row.status)}>{nodeStatusText(row.status, t)}</StatusPill>
            </div>
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
        ))}
      </ul>
    </>
  )
}
