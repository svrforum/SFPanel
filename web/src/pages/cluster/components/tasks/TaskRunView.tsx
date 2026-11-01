import { useCallback, useEffect, useId, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Loader2, OctagonX, RotateCcw, Square } from 'lucide-react'
import { api } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { formatDate } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { StatusPill } from '@/components/StatusPill'
import { useConfirm } from '@/components/ConfirmDialog'
import type { RunRecord } from '@/types/api'
import { builtinText } from './taskBuiltins'
import { runMatrix, type RunMatrix, type RunState } from './taskRunProgress'
import { runStatusText, runStatusTone } from './taskRunStatus'
import {
  CELL_ORDER, cellText, cellTone, focusStep, nodeStatusText, nodeStatusTone, runActions, runStepCount, stepHeader, unsuccessfulNodes,
} from './taskRunView'
import { useTaskRun } from './useTaskRun'
import { useMounted } from './useMounted'
import { TaskRunMatrix, type ChosenCell } from './TaskRunMatrix'
import { TaskNodeOutput } from './TaskNodeOutput'
import { INSTALL_STEP_COUNT, installRowName, isInstallRun } from './install/installRun'
import { InstallRunHead } from './install/InstallRunHead'
import { InstallBanners } from './install/InstallBanners'
import { InstallRowActions } from './install/InstallRowActions'

interface TaskRunViewProps {
  runId: string
  /** The run's status is first known, or changed: the band and the history read the runs again. */
  onStatus?: () => void
}

// One run, under way or ended, followed from the run ID in the URL: a reload,
// a trip to another menu or a node switch comes back to the same table. The
// head says what ran, who started it, how it stands and which batch is going;
// the table has a row per node and a column per step; a node or a cell opens
// that step's output beside it (below, on a phone). A run under way can be
// stopped, or stopped by force; an ended one can run again on the nodes that
// did not succeed, or on nodes chosen.
//
// An agent install is a run too, of its own kind: its rows are servers, named
// as entered, through five fixed phases. Its head has no rerun; what it waits
// on the operator for is asked above the table, and why a server ended, with
// what can be done about it, is said below.
export function TaskRunView({ runId, onStatus }: TaskRunViewProps) {
  const { t } = useTranslation()
  const { state, steps, outcome, setHead } = useTaskRun(runId)
  // They read the runs every ten seconds otherwise: a run seen ended here
  // stayed "under way" above it until then.
  const status = state.run?.status
  useEffect(() => {
    if (status) onStatus?.()
  }, [status, onStatus])
  const [names, setNames] = useState<Record<string, string>>({})
  const [localId, setLocalId] = useState('')
  const [chosen, setChosen] = useState<ChosenCell | null>(null)

  // Names from the node list; a node no longer in the cluster shows its ID.
  useEffect(() => {
    let live = true
    api.getClusterNodes().then(
      (r) => {
        if (!live) return
        setNames(Object.fromEntries(r.nodes.map((n) => [n.id, n.name])))
        setLocalId(r.local_id)
      },
      () => {},
    )
    return () => { live = false }
  }, [])

  const run = state.run
  const install = run !== null && isInstallRun(run)
  // An install's rows are servers, not nodes: named as the operator entered them.
  const nodeName = useCallback(
    (id: string) => (install ? installRowName(id, state.nodes[id]) : names[id] ?? id),
    [install, state.nodes, names],
  )
  const count = install ? INSTALL_STEP_COUNT : runStepCount(steps, state)
  const matrix = useMemo(() => {
    const m = runMatrix(state, count, nodeName)
    // An install's row reason is a code, worded below the table.
    return install ? { ...m, rows: m.rows.map((row) => ({ ...row, reason: undefined })) } : m
  }, [state, count, nodeName, install])
  const headers = useMemo(
    () => (run ? Array.from({ length: count }, (_, i) => stepHeader(t, run.task_id, steps, i)) : []),
    [run, count, steps, t],
  )

  if (!run) {
    return (
      <section data-testid="task-pane-run" className="rounded-2xl bg-card p-5 card-shadow">
        {outcome === 'not_found' ? (
          <p role="alert" className="text-[13px] text-muted-foreground">{t('cluster.tasks.errors.runNotFound')}</p>
        ) : (
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        )}
      </section>
    )
  }

  const chosenRecord = chosen ? state.nodes[chosen.node]?.steps?.[chosen.step] : undefined

  return (
    <section data-testid="task-pane-run" className="rounded-2xl bg-card p-5 card-shadow space-y-4 min-w-0">
      {install ? (
        <>
          <InstallRunHead run={run} onHead={setHead} />
          <InstallBanners run={run} state={state} rowName={nodeName} />
        </>
      ) : (
        <>
          <RunHead run={run} state={state} wave={matrix.wave} counts={matrix.counts} />
          <RunActions run={run} state={state} nodeName={nodeName} onHead={setHead} />
        </>
      )}
      {count === 0 && <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.run.noSteps')}</p>}
      <div className="flex flex-col lg:flex-row gap-4 min-w-0">
        <div className="flex-1 min-w-0">
          <TaskRunMatrix
            rows={matrix.rows}
            steps={headers}
            chosen={chosen}
            onChoose={(node, step) => setChosen({ node, step })}
            onChooseNode={(node) => setChosen({ node, step: focusStep(state.nodes[node]) })}
            rowsHeader={install ? t('cluster.tasks.install.run.server') : undefined}
          />
        </div>
        {chosen && chosen.step < count && (
          <div className="lg:w-[420px] lg:shrink-0 min-w-0">
            <TaskNodeOutput
              key={`${chosen.node}/${chosen.step}/${chosenRecord?.job_id ?? ''}`}
              runId={run.id}
              nodeId={chosen.node}
              nodeName={nodeName(chosen.node)}
              step={chosen.step}
              stepName={headers[chosen.step]}
              record={chosenRecord}
              onClose={() => setChosen(null)}
            />
          </div>
        )}
      </div>
      {install && <InstallRowActions run={run} state={state} rowName={nodeName} localId={localId} />}
    </section>
  )
}

interface RunHeadProps {
  run: RunRecord
  state: RunState
  wave: RunMatrix['wave']
  counts: RunMatrix['counts']
}

function RunHead({ run, state, wave, counts }: RunHeadProps) {
  const { t } = useTranslation()
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h2 className="text-[18px] font-bold break-words min-w-0">{builtinText(t, run.task_id, 'name', run.task_name)}</h2>
        <span data-testid="task-run-status">
          <StatusPill tone={runStatusTone(run.status)}>{runStatusText(run.status, t)}</StatusPill>
        </span>
        {/* The batch going now: an ended run has none. */}
        {wave && !state.ended && (
          <span data-testid="task-run-wave" className="text-[13px] font-medium text-primary">
            {t('cluster.tasks.run.wave', { index: wave.index, total: wave.total })}
          </span>
        )}
      </div>
      <dl className="flex flex-wrap gap-x-5 gap-y-1 text-[12px] text-muted-foreground">
        <div className="flex gap-1.5">
          <dt>{t('cluster.tasks.run.startedBy')}</dt>
          <dd className="text-foreground break-all">{run.started_by}</dd>
        </div>
        <div className="flex gap-1.5">
          <dt>{t('cluster.tasks.run.startedAt')}</dt>
          <dd className="text-foreground">{formatDate(run.started_at)}</dd>
        </div>
        {run.ended_at && (
          <div className="flex gap-1.5">
            <dt>{t('cluster.tasks.run.endedAt')}</dt>
            <dd className="text-foreground">{formatDate(run.ended_at)}</dd>
          </div>
        )}
        {run.rerun_of && (
          <div className="flex gap-1.5">
            <dt>{t('cluster.tasks.run.rerunOf')}</dt>
            <dd>
              <Link to={`/cluster/tasks/runs/${encodeURIComponent(run.rerun_of)}`} className="font-mono underline underline-offset-2 hover:text-primary break-all">
                {run.rerun_of}
              </Link>
            </dd>
          </div>
        )}
      </dl>
      {run.reason && <p className="text-[12px] text-muted-foreground break-words">{run.reason}</p>}
      <ul data-testid="task-run-counts" aria-label={t('cluster.tasks.run.counts')} className="flex flex-wrap gap-1.5">
        {CELL_ORDER.filter((c) => counts[c] > 0).map((c) => (
          <li key={c}>
            <StatusPill tone={cellTone(c)}>
              {cellText(c, t)} <span className="ml-1 tabular-nums">{counts[c]}</span>
            </StatusPill>
          </li>
        ))}
      </ul>
    </div>
  )
}

interface RunActionsProps {
  run: RunRecord
  state: RunState
  nodeName: (id: string) => string
  onHead: (run: RunRecord) => void
}

// Stop and force stop for a run under way; the reruns for one that ended. A
// rerun's 202 goes to the new run, if the operator is still on this one.
// Nothing is retried by itself: a 502 or 504 may have started it all the same.
function RunActions({ run, state, nodeName, onHead }: RunActionsProps) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const navigate = useNavigate()
  const mounted = useMounted()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [choosing, setChoosing] = useState(false)
  const can = runActions(run)
  const failed = unsuccessfulNodes(state)

  const stop = async (force: boolean) => {
    const yes = await confirm(
      force
        ? { title: t('cluster.tasks.run.forceStopTitle'), description: t('cluster.tasks.run.forceStopDescription'), confirmLabel: t('cluster.tasks.run.forceStop'), danger: true }
        : { title: t('cluster.tasks.run.stopTitle'), description: t('cluster.tasks.run.stopDescription'), confirmLabel: t('cluster.tasks.run.stop') },
    )
    if (!yes) return
    setBusy(true)
    setError(null)
    try {
      onHead(await api.stopTaskRun(run.id, force))
    } catch (err) {
      setError(taskErrorMessage(err, t))
    } finally {
      setBusy(false)
    }
  }

  const rerun = async (which: { onlyFailed: true } | { nodes: string[] }) => {
    setBusy(true)
    setError(null)
    try {
      const { run_id } = await api.rerunTaskRun(run.id, which)
      if (mounted.current) navigate(`/cluster/tasks/runs/${encodeURIComponent(run_id)}`)
    } catch (err) {
      setError(taskErrorMessage(err, t))
      setChoosing(false)
    } finally {
      setBusy(false)
    }
  }

  const rerunFailed = async () => {
    const yes = await confirm({
      title: t('cluster.tasks.run.rerunFailedTitle'),
      description: t('cluster.tasks.run.rerunFailedDescription', { count: failed.length }),
      confirmLabel: t('cluster.tasks.run.rerun'),
    })
    if (yes) await rerun({ onlyFailed: true })
  }

  if (!can.stop && !can.forceStop && !can.rerun) return null

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        {can.stop && (
          <Button variant="outline" onClick={() => stop(false)} disabled={busy}>
            <Square className="h-4 w-4" />
            {t('cluster.tasks.run.stop')}
          </Button>
        )}
        {can.forceStop && (
          <Button variant="destructive" onClick={() => stop(true)} disabled={busy}>
            <OctagonX className="h-4 w-4" />
            {t('cluster.tasks.run.forceStop')}
          </Button>
        )}
        {can.rerun && (
          <>
            <Button onClick={rerunFailed} disabled={busy || failed.length === 0}>
              {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <RotateCcw className="h-4 w-4" />}
              {t('cluster.tasks.run.rerunFailed')}
            </Button>
            <Button variant="outline" onClick={() => setChoosing(true)} disabled={busy}>
              {t('cluster.tasks.run.rerunChosen')}
            </Button>
          </>
        )}
      </div>
      {error && <p role="alert" data-testid="task-run-action-error" className="rounded-xl bg-destructive/10 px-3 py-2 text-[13px] text-destructive break-words">{error}</p>}
      {choosing && (
        <RerunDialog
          nodes={run.nodes}
          initial={failed}
          nodeName={nodeName}
          state={state}
          busy={busy}
          onCancel={() => setChoosing(false)}
          onRerun={(nodes) => rerun({ nodes })}
        />
      )}
    </div>
  )
}

interface RerunDialogProps {
  nodes: string[]
  initial: string[]
  nodeName: (id: string) => string
  state: RunState
  busy: boolean
  onCancel: () => void
  onRerun: (nodes: string[]) => void
}

// The run's nodes to pick from, the ones that did not succeed ticked first. A
// node picked that had succeeded runs every step again.
function RerunDialog({ nodes, initial, nodeName, state, busy, onCancel, onRerun }: RerunDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const [picked, setPicked] = useState<string[]>(initial)
  const toggle = (node: string, on: boolean) => setPicked((p) => (on ? [...p, node] : p.filter((x) => x !== node)))

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onCancel()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t('cluster.tasks.run.rerunChosenTitle')}</DialogTitle>
          <DialogDescription>{t('cluster.tasks.run.rerunChosenDescription')}</DialogDescription>
        </DialogHeader>
        <ul className="max-h-80 overflow-y-auto space-y-1">
          {nodes.map((node) => {
            const field = `${id}-${node}`
            const status = state.nodes[node]?.status ?? 'pending'
            return (
              <li key={node} className="flex items-center gap-2 min-h-9">
                <Checkbox id={field} checked={picked.includes(node)} disabled={busy} onCheckedChange={(on) => toggle(node, on === true)} />
                <label htmlFor={field} className="flex-1 min-w-0 truncate text-[13px] cursor-pointer">{nodeName(node)}</label>
                <StatusPill tone={nodeStatusTone(status)}>{nodeStatusText(status, t)}</StatusPill>
              </li>
            )
          })}
        </ul>
        <DialogFooter>
          <Button variant="outline" onClick={onCancel} disabled={busy}>{t('common.cancel')}</Button>
          {/* Run order, whatever order they were ticked in. */}
          <Button onClick={() => onRerun(nodes.filter((n) => picked.includes(n)))} disabled={busy || picked.length === 0}>
            {busy && <Loader2 className="h-4 w-4 animate-spin" />}
            {t('cluster.tasks.run.rerun')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
