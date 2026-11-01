import { useCallback, useEffect, useId, useMemo, useState, type ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Copy, Eye, Loader2, Pencil, Play, Trash2 } from 'lucide-react'
import { api, type ApiError } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { formatDate } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { StatusPill } from '@/components/StatusPill'
import type { ClusterNode, RunRecord, TaskModel, TaskPlan, TaskRunRequest, TaskSource, TaskView } from '@/types/api'
import { builtinText } from './taskBuiltins'
import { TaskTargetPicker, type PickedTargets } from './TaskTargetPicker'
import { TaskPlanDialog, type FilledInput } from './TaskPlanDialog'
import { checkInputs, countTargets, runInputs, type InputProblem } from './taskTargets'
import { planNeedsReview, runErrorText, type RunErrorText } from './taskPlan'
import { runStatusText, runStatusTone } from './taskRunStatus'
import { clonePath, editPath } from './taskForm'
import { shortSha } from './taskImport'
import type { ListHolds } from './taskList'
import { useMounted } from './useMounted'
import { useTaskDelete } from './useTaskDelete'

interface TaskRunSetupProps {
  /** The task as the list has it; undefined when the list has no such ID. */
  task: TaskView | undefined
  /** Reads the list again once the task is deleted, until it no longer has it. */
  onDeleted: (holds?: ListHolds) => Promise<void> | void
}

const RECENT_RUNS = 5

const byName = (a: ClusterNode, b: ClusterNode) => a.name.localeCompare(b.name, undefined, { numeric: true })

function picked(model: TaskModel): PickedTargets {
  return { groups: model.targets.groups ?? [], kinds: model.targets.kinds ?? [], nodes: model.targets.nodes ?? [] }
}

/** The plan under review, and the request it was taken for: the start sends that request again. */
interface Review {
  plan: TaskPlan
  request: TaskRunRequest
  changed: boolean
}

// The chosen task, set up to run: what it does, the values it asks for, the
// nodes it takes and how, then [Preview] and [Run]. [Run] always takes a
// preview first and starts that plan straight away when there is nothing to
// show (no step to confirm or refused, no node left out or unchecked, every
// manager ready); otherwise it opens the preview. The last runs of the task
// sit underneath. Above them, what can be done to the task itself: the
// operator's own tasks are edited and deleted; a built-in is only cloned;
// a Git task is cloned or deleted (it changes only when fetched again).
export function TaskRunSetup({ task, onDeleted }: TaskRunSetupProps) {
  const { t } = useTranslation()
  if (!task) {
    return (
      <section data-testid="task-pane-setup" className="rounded-2xl bg-card p-5 card-shadow">
        <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.errors.notFound')}</p>
      </section>
    )
  }
  const description = builtinText(t, task.id, 'description', task.description ?? '')
  const name = builtinText(t, task.id, 'name', task.name)
  return (
    <section data-testid="task-pane-setup" className="rounded-2xl bg-card p-5 card-shadow space-y-5">
      <div className="space-y-2">
        <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-2">
          <h2 className="min-w-0 text-[18px] font-bold break-words">{name}</h2>
          <TaskActions task={task} name={name} onDeleted={onDeleted} />
        </div>
        {description && <p className="text-[13px] text-muted-foreground whitespace-pre-wrap break-words">{description}</p>}
        {task.source.kind === 'git' && <GitSource source={task.source} />}
      </div>
      <RunForm task={task} />
      <RecentRuns taskId={task.id} />
    </section>
  )
}

// Where a Git task was imported from: the repository, branch, file and the
// commit it was read at (its first seven characters; all of it on hover).
function GitSource({ source }: { source: TaskSource }) {
  const { t } = useTranslation()
  const row = (label: string, value: ReactNode) => (
    <div className="flex min-w-0 gap-2">
      <dt className="w-20 shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-all">{value}</dd>
    </div>
  )
  return (
    <dl data-testid="task-source" className="rounded-xl bg-secondary/40 px-3 py-2 text-[12px] space-y-0.5">
      {row(t('cluster.tasks.setup.source.repo'), source.repo)}
      {row(t('cluster.tasks.setup.source.branch'), source.branch)}
      {row(t('cluster.tasks.setup.source.path'), <span className="font-mono">{source.path}</span>)}
      {row(t('cluster.tasks.setup.source.commit'), <span className="font-mono" title={source.sha}>{shortSha(source.sha)}</span>)}
    </dl>
  )
}

function TaskActions({ task, name, onDeleted }: { task: TaskView; name: string; onDeleted: (holds?: ListHolds) => Promise<void> | void }) {
  const { t } = useTranslation()
  const { remove, deleting } = useTaskDelete(onDeleted)
  return (
    <div className="flex flex-wrap gap-2">
      {task.read_only ? (
        <Button asChild variant="outline" size="sm">
          <Link to={clonePath(task.id)}>
            <Copy className="h-4 w-4" />
            {t('cluster.tasks.editor.clone')}
          </Link>
        </Button>
      ) : (
        <Button asChild variant="outline" size="sm">
          <Link to={editPath(task.id)}>
            <Pencil className="h-4 w-4" />
            {t('cluster.tasks.editor.edit')}
          </Link>
        </Button>
      )}
      {task.source.kind !== 'builtin' && (
        <Button variant="outline" size="sm" className="text-destructive" loading={deleting} onClick={() => remove(task.id, name)}>
          <Trash2 className="h-4 w-4" />
          {t('common.delete')}
        </Button>
      )}
    </div>
  )
}

function RunForm({ task }: { task: TaskView }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const mounted = useMounted()
  const id = useId()
  const name = builtinText(t, task.id, 'name', task.name)

  const [detail, setDetail] = useState<TaskView | null>(null)
  const [detailError, setDetailError] = useState<string | null>(null)
  const [nodes, setNodes] = useState<ClusterNode[] | null>(null)
  const [nodesError, setNodesError] = useState<string | null>(null)

  const [values, setValues] = useState<Record<string, string>>({})
  const [targets, setTargets] = useState<PickedTargets>({ groups: [], kinds: [], nodes: [] })
  const [batch, setBatch] = useState('1')
  const [stopOnFailure, setStopOnFailure] = useState(true)

  const [attempted, setAttempted] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<RunErrorText | null>(null)
  const [review, setReview] = useState<Review | null>(null)
  const [reviewError, setReviewError] = useState<RunErrorText | null>(null)
  const [confirmed, setConfirmed] = useState<number[]>([])

  // The document's model: the inputs, the task's own targets and how it runs.
  useEffect(() => {
    let live = true
    api.getTask(task.id).then(
      (d) => {
        if (!live) return
        setDetail(d)
        if (d.task) {
          setTargets(picked(d.task))
          setBatch(String(d.task.batch || 1))
          setStopOnFailure(d.task.stop_on_failure)
        }
      },
      (err) => live && setDetailError(taskErrorMessage(err, t)),
    )
    return () => { live = false }
  }, [task.id, t])

  useEffect(() => {
    let live = true
    api.getClusterNodes().then(
      (r) => live && setNodes([...r.nodes].sort(byName)),
      (err) => {
        if (!live) return
        setNodes([])
        setNodesError(taskErrorMessage(err, t))
      },
    )
    return () => { live = false }
  }, [t])

  const model = detail?.task
  const nodeName = useCallback((nodeId: string) => nodes?.find((n) => n.id === nodeId)?.name ?? nodeId, [nodes])
  const inputLabel = useCallback(
    (inputName: string, label: string | undefined) => builtinText(t, task.id, `inputs.${inputName}`, label || inputName),
    [t, task.id],
  )
  // A step's name in a preview, from the document the plan was taken from:
  // the task may have changed since it was read above. Only a plan that
  // names no steps is labelled from this screen's model.
  const stepName = useCallback(
    (plan: TaskPlan, step: number) => {
      const own = (plan.steps ? plan.steps[step]?.name : model?.steps[step]?.name) ?? ''
      return builtinText(t, task.id, `steps.${step}`, own) || t('cluster.tasks.plan.step', { n: step + 1 })
    },
    [model, t, task.id],
  )

  const problems = useMemo<InputProblem[]>(() => (model ? checkInputs(model, values) : []), [model, values])
  const problemOf = (inputName: string) => problems.find((p) => p.name === inputName)

  if (detailError) {
    return <p role="alert" className="text-[13px] text-destructive">{t('cluster.tasks.setup.loadError')}: {detailError}</p>
  }
  if (!detail) return <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
  if (!model) return <p role="alert" className="text-[13px] text-destructive">{t('cluster.tasks.setup.unparsable')}</p>

  const batchNumber = (() => {
    const n = Number(batch)
    return Number.isInteger(n) && n >= 1 ? n : model.batch || 1
  })()
  // Nothing to take: Preview and Run wait for a target. When the nodes could
  // not be read the count means nothing, and the server is left to judge.
  const noTargets = nodes !== null && !nodesError && countTargets(nodes, targets) === 0

  const request = (): TaskRunRequest => ({
    task_id: task.id,
    inputs: runInputs(model, values),
    targets,
    batch: batchNumber,
    stop_on_failure: stopOnFailure,
  })

  const filled = (req: TaskRunRequest): FilledInput[] =>
    (model.inputs ?? []).map((input) => {
      const given = req.inputs?.[input.name] ?? ''
      return {
        name: input.name,
        label: inputLabel(input.name, input.label),
        value: given || input.default || '',
        isDefault: given === '' && !!input.default,
        raw: !!input.raw,
      }
    })

  const openReview = (plan: TaskPlan, req: TaskRunRequest, changed: boolean) => {
    setReview({ plan, request: req, changed })
    setConfirmed([])
    setReviewError(null)
  }

  // Starts the plan the operator saw. A plan the server finds changed since
  // (409 TASK_PLAN_CHANGED) means the document changed, since the start sends
  // the preview's request as it was: the task is read again, the request is
  // made from the new document (the values typed for inputs it still has,
  // the targets as picked), and its preview is shown, with the reason, for
  // the operator to look at and confirm again.
  const start = async (plan: TaskPlan, req: TaskRunRequest, steps: number[], fromReview: boolean) => {
    const fail = fromReview ? setReviewError : setError
    try {
      const { run_id } = await api.startTaskRun({ ...req, plan_id: plan.plan_id, ...(steps.length > 0 ? { confirmed: steps } : {}) })
      if (mounted.current) navigate(`/cluster/tasks/runs/${encodeURIComponent(run_id)}`)
    } catch (err) {
      if ((err as ApiError).code !== 'TASK_PLAN_CHANGED') {
        fail(runErrorText(err, t, nodeName))
        return
      }
      try {
        const fresh = await api.getTask(task.id)
        setDetail(fresh)
        if (!fresh.task) {
          // The form says the document no longer parses.
          setReview(null)
          return
        }
        const names = new Set((fresh.task.inputs ?? []).map((input) => input.name))
        const kept = Object.fromEntries(Object.entries(values).filter(([inputName]) => names.has(inputName)))
        setValues(kept)
        const again = { ...req, inputs: runInputs(fresh.task, kept) }
        openReview(await api.planTaskRun(again), again, true)
      } catch (again) {
        fail(runErrorText(again, t, nodeName))
      }
    }
  }

  // [Preview] always opens the preview; [Run] opens it only when there is
  // something to see, and otherwise starts what it previewed. A preview that
  // answers once the operator has left this task does neither.
  const go = async (andRun: boolean) => {
    setAttempted(true)
    if (problems.length > 0) return
    const req = request()
    setBusy(true)
    setError(null)
    try {
      const plan = await api.planTaskRun(req)
      if (!mounted.current) return
      if (andRun && !planNeedsReview(plan)) await start(plan, req, [], false)
      else openReview(plan, req, false)
    } catch (err) {
      setError(runErrorText(err, t, nodeName))
    } finally {
      setBusy(false)
    }
  }

  const runReviewed = async () => {
    if (!review) return
    setBusy(true)
    setReviewError(null)
    try {
      await start(review.plan, review.request, confirmed, true)
    } finally {
      setBusy(false)
    }
  }

  const inputs = model.inputs ?? []
  const total = problemOf('*')

  return (
    <div className="space-y-5">
      {inputs.length > 0 && (
        <section aria-labelledby={`${id}-inputs`} className="space-y-3">
          <h3 id={`${id}-inputs`} className="text-[14px] font-semibold">{t('cluster.tasks.setup.inputs')}</h3>
          <p className="flex items-start gap-2 rounded-xl bg-warning/10 px-3 py-2 text-[12px] text-warning">
            <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" aria-hidden />
            {t('cluster.tasks.setup.inputsWarning')}
          </p>
          {inputs.map((input) => {
            const field = `${id}-input-${input.name}`
            const problem = problemOf(input.name)
            const shown = problem && (problem.reason !== 'required' || attempted) ? problem : undefined
            return (
              <div key={input.name} data-testid={`task-input-${input.name}`} className="space-y-1.5">
                <div className="flex flex-wrap items-center gap-1.5">
                  <label htmlFor={field} className="text-[13px] font-medium break-words">{inputLabel(input.name, input.label)}</label>
                  {input.required && <StatusPill tone="secondary">{t('cluster.tasks.setup.required')}</StatusPill>}
                  {input.raw && <StatusPill tone="warning">{t('cluster.tasks.setup.raw')}</StatusPill>}
                </div>
                <Input
                  id={field}
                  value={values[input.name] ?? ''}
                  placeholder={input.default ? t('cluster.tasks.setup.defaultValue', { value: input.default }) : undefined}
                  aria-invalid={shown ? true : undefined}
                  disabled={busy}
                  onChange={(e) => setValues((v) => ({ ...v, [input.name]: e.target.value }))}
                  className="font-mono"
                />
                {shown && (
                  <p className="text-[12px] text-destructive">
                    {shown.reason === 'required' ? t('cluster.tasks.setup.inputRequired') : t('cluster.tasks.setup.inputTooLarge')}
                  </p>
                )}
              </div>
            )
          })}
          {total && <p className="text-[12px] text-destructive">{t('cluster.tasks.setup.inputsTooLarge')}</p>}
        </section>
      )}

      <TaskTargetPicker nodes={nodes} nodesError={nodesError} targets={targets} onChange={setTargets} disabled={busy} />

      <section aria-labelledby={`${id}-how`} className="space-y-3">
        <h3 id={`${id}-how`} className="text-[14px] font-semibold">{t('cluster.tasks.setup.howItRuns')}</h3>
        <div className="flex flex-wrap gap-x-6 gap-y-3">
          <div className="space-y-1">
            <label htmlFor={`${id}-batch`} className="text-[13px] font-medium">{t('cluster.tasks.setup.batch')}</label>
            <Input
              id={`${id}-batch`}
              type="number"
              min={1}
              step={1}
              inputMode="numeric"
              value={batch}
              disabled={busy}
              onChange={(e) => setBatch(e.target.value)}
              className="w-28"
            />
            <p className="text-[12px] text-muted-foreground max-w-xs">{t('cluster.tasks.setup.batchHint')}</p>
          </div>
          <div className="space-y-1">
            <div className="flex items-center gap-2 min-h-9">
              <Checkbox id={`${id}-stop`} checked={stopOnFailure} disabled={busy} onCheckedChange={(on) => setStopOnFailure(on === true)} />
              <label htmlFor={`${id}-stop`} className="text-[13px] font-medium cursor-pointer">{t('cluster.tasks.setup.stopOnFailure')}</label>
            </div>
            <p className="text-[12px] text-muted-foreground max-w-xs">{t('cluster.tasks.setup.stopOnFailureHint')}</p>
          </div>
        </div>
      </section>

      <div className="flex flex-wrap gap-2">
        <Button variant="outline" onClick={() => go(false)} disabled={busy || noTargets}>
          <Eye className="h-4 w-4" />
          {t('cluster.tasks.setup.preview')}
        </Button>
        <Button onClick={() => go(true)} disabled={busy || noTargets}>
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
          {t('cluster.tasks.setup.run')}
        </Button>
      </div>

      {error && (
        <div role="alert" data-testid="task-run-error" className="rounded-xl bg-destructive/10 px-3 py-2 text-[13px] text-destructive space-y-1">
          <p className="break-words">{error.text}</p>
          {error.server && <p className="text-[12px] font-mono break-words opacity-80">{error.server}</p>}
        </div>
      )}

      {review && (
        <TaskPlanDialog
          open
          onOpenChange={(open) => !open && !busy && setReview(null)}
          name={name}
          plan={review.plan}
          inputs={filled(review.request)}
          stepName={(step) => stepName(review.plan, step)}
          confirmed={confirmed}
          onConfirmedChange={setConfirmed}
          changed={review.changed}
          error={reviewError}
          busy={busy}
          onRun={runReviewed}
        />
      )}
    </div>
  )
}

// The task's last runs, newest first: the history has no filter, so this
// one picks them out of the list.
function RecentRuns({ taskId }: { taskId: string }) {
  const { t } = useTranslation()
  const id = useId()
  const [runs, setRuns] = useState<RunRecord[] | null>(null)

  useEffect(() => {
    let live = true
    api.listTaskRuns().then(
      (all) => live && setRuns(all.filter((r) => r.task_id === taskId).slice(0, RECENT_RUNS)),
      () => live && setRuns([]),
    )
    return () => { live = false }
  }, [taskId])

  return (
    <section aria-labelledby={`${id}-recent`} className="space-y-2 border-t border-border pt-4">
      <h3 id={`${id}-recent`} className="text-[14px] font-semibold">{t('cluster.tasks.setup.recent')}</h3>
      {runs === null ? (
        <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
      ) : runs.length === 0 ? (
        <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.setup.noRecent')}</p>
      ) : (
        <ul className="space-y-1">
          {runs.map((run) => (
            <li key={run.id}>
              <Link
                to={`/cluster/tasks/runs/${encodeURIComponent(run.id)}`}
                className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl px-3 py-2 text-[13px] hover:bg-secondary/60"
              >
                <StatusPill tone={runStatusTone(run.status)}>{runStatusText(run.status, t)}</StatusPill>
                <span>{formatDate(run.started_at)}</span>
                <span className="text-muted-foreground break-all">{run.started_by}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
