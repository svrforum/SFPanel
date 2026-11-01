import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useMatch, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Loader2, Server } from 'lucide-react'
import { api } from '@/lib/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { ListLoadState } from '@/components/ListLoadState'
import type { ClusterStatus, RunRecord, TaskView } from '@/types/api'
import { TaskList, TaskPicker, type TaskListChoice } from './components/tasks/TaskList'
import { TaskRunSetup } from './components/tasks/TaskRunSetup'
import { TaskRunView } from './components/tasks/TaskRunView'
import { TaskRunHistory } from './components/tasks/TaskRunHistory'
import { TaskGitImportDialog } from './components/tasks/TaskGitImportDialog'
import type { ImportFrom } from './components/tasks/taskImport'
import { builtinText } from './components/tasks/taskBuiltins'
import { activeRuns, type ListedTask } from './components/tasks/taskList'

// The editor carries Monaco: loaded only when a route opens it.
const TaskEditor = lazy(() => import('./components/tasks/TaskEditor'))

const RUNS_POLL_MS = 10_000

// The cluster's Tasks tab. Every tasks/* route lands here and the URL picks
// the right pane: nothing (tasks), a task's run setup (tasks/:taskId), the
// editor (tasks/new, tasks/:taskId/edit), the history (tasks/runs) or one run
// (tasks/runs/:runId). Static segments win over :taskId, and a built-in's ID
// (builtin/command) travels escaped once. /cluster/* is never sent to a
// chosen node: any manager answers from its replicated state.
export default function ClusterTasks() {
  const { t } = useTranslation()
  const { taskId, runId } = useParams()
  const isNew = useMatch('/cluster/tasks/new') !== null
  const isHistory = useMatch('/cluster/tasks/runs') !== null
  const isEdit = useMatch('/cluster/tasks/:taskId/edit') !== null

  const [status, setStatus] = useState<ClusterStatus | null>(null)
  const [tasks, setTasks] = useState<TaskView[] | null>(null)
  const [tasksError, setTasksError] = useState<string | null>(null)
  const [tasksLoading, setTasksLoading] = useState(true)
  // Every run the cluster keeps: the band's and the history's one source.
  const [runs, setRuns] = useState<RunRecord[] | null>(null)
  const [runsError, setRunsError] = useState<string | null>(null)
  // Outside a cluster there are no runs to ask for every ten seconds.
  const standalone = useRef(false)
  // The Git import dialog, while open: empty, or on a folder to pull again.
  const [importing, setImporting] = useState<{ from?: ImportFrom } | null>(null)

  // Also what the editor and a delete wait on before the page moves.
  const loadTasks = useCallback(() =>
    Promise.allSettled([api.getClusterStatus(), api.listTasks()]).then(([s, l]) => {
      if (s.status === 'fulfilled') {
        setStatus(s.value)
        standalone.current = s.value.enabled === false
      }
      if (l.status === 'fulfilled') {
        setTasks(l.value)
        setTasksError(null)
      } else {
        setTasksError(taskErrorMessage(l.reason, t))
      }
      setTasksLoading(false)
    }), [t])

  useEffect(() => {
    loadTasks()
  }, [loadTasks])

  // Runs under way, for the band: a run started in another browser, or this
  // one before it was closed, is a click away. The history lists the same.
  const loadRuns = useCallback(() => {
    if (standalone.current) return
    api.listTaskRuns().then(
      (list) => {
        setRuns(list)
        setRunsError(null)
      },
      (err) => setRunsError(taskErrorMessage(err, t)),
    )
  }, [t])
  useVisibleInterval(loadRuns, RUNS_POLL_MS)

  const listed = useMemo<ListedTask[]>(
    () =>
      (tasks ?? []).map((task) => ({
        task,
        name: builtinText(t, task.id, 'name', task.name),
        description: builtinText(t, task.id, 'description', task.description ?? ''),
      })),
    [tasks, t],
  )
  const nameOf = (id: string) => listed.find((x) => x.task.id === id)?.name

  if (status?.enabled === false) {
    return (
      <div className="bg-card rounded-2xl p-8 card-shadow text-center">
        <Server className="h-12 w-12 text-muted-foreground mx-auto mb-3" />
        <p className="text-[13px] text-muted-foreground">{t('cluster.notEnabled.title')}</p>
      </div>
    )
  }

  const chosen: TaskListChoice = { taskId, history: isHistory || !!runId }
  const pickerLabel = runId || isHistory
    ? t('cluster.tasks.list.history')
    : isNew
      ? t('cluster.tasks.list.new')
      : taskId
        ? (nameOf(taskId) ?? taskId)
        : null

  const task = taskId ? tasks?.find((x) => x.id === taskId) : undefined
  let pane
  if (runId) pane = <TaskRunView key={runId} runId={runId} />
  else if (isHistory) {
    pane = (
      <TaskRunHistory
        runs={runs}
        error={runsError}
        onRetry={() => {
          setRunsError(null)
          loadRuns()
        }}
      />
    )
  }
  else if (isNew || isEdit) {
    pane = (
      <Suspense fallback={<Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />}>
        <TaskEditor taskId={isEdit ? taskId : undefined} name={taskId ? nameOf(taskId) : undefined} onSaved={loadTasks} />
      </Suspense>
    )
  } else if (taskId) {
    // A list read again that shows the document changed (pulled again from
    // Git, saved, imported) sets the pane up afresh from the new document.
    const version = task ? `${task.id}\n${task.updated_at ?? ''}\n${task.source.sha ?? ''}` : taskId
    pane = tasks === null ? null : <TaskRunSetup key={version} task={task} onDeleted={loadTasks} />
  }
  else {
    pane = (
      <section data-testid="task-pane-none" className="hidden md:block rounded-2xl bg-card p-5 card-shadow space-y-2">
        <h2 className="text-lg font-semibold">{t('cluster.tasks.empty.title')}</h2>
        <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.empty.description')}</p>
      </section>
    )
  }

  const listState = tasks === null && (
    <ListLoadState
      loading={tasksLoading}
      error={tasksError}
      errorTitle={t('cluster.tasks.list.loadError')}
      onRetry={() => {
        setTasksError(null)
        setTasksLoading(true)
        loadTasks()
      }}
      skeletonCount={6}
    />
  )
  const active = activeRuns(runs ?? [])

  return (
    <div className="space-y-4">
      {active.length > 0 && (
        <div
          role="status"
          data-testid="task-runs-active"
          className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl bg-primary/10 px-4 py-2.5 text-[13px]"
        >
          <span className="inline-flex items-center gap-2 font-medium text-primary">
            <Loader2 className="h-4 w-4 animate-spin" />
            {t('cluster.tasks.active.count', { count: active.length })}
          </span>
          {active.map((run) => (
            <Link
              key={run.id}
              to={`/cluster/tasks/runs/${encodeURIComponent(run.id)}`}
              className="underline underline-offset-2 hover:text-primary truncate max-w-full"
            >
              {builtinText(t, run.task_id, 'name', run.task_name)}
            </Link>
          ))}
        </div>
      )}
      <div className="flex flex-col md:flex-row gap-4">
        <aside className="hidden md:block md:w-[260px] shrink-0">
          {listState || <TaskList tasks={listed} chosen={chosen} onImport={(from) => setImporting({ from })} />}
        </aside>
        <div className="md:hidden">
          {listState || <TaskPicker label={pickerLabel} tasks={listed} chosen={chosen} onImport={(from) => setImporting({ from })} />}
        </div>
        <div className="flex-1 min-w-0">{pane}</div>
      </div>
      {importing && <TaskGitImportDialog from={importing.from} onClose={() => setImporting(null)} onChanged={loadTasks} />}
    </div>
  )
}
