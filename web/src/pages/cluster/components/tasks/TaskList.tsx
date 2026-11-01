import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ChevronDown, GitBranch, History, ListChecks, Plus, RefreshCw, Search } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { taskSections, type ListedTask } from './taskList'
import { pullSources, type ImportFrom } from './taskImport'

/** What the right pane shows, as far as the list marks it chosen. */
export interface TaskListChoice {
  taskId?: string
  history?: boolean
}

interface TaskListProps {
  tasks: ListedTask[]
  chosen: TaskListChoice
  /** Called after any entry is followed: the phone's picker folds. */
  onPick?: () => void
  /** Opens the Git import: empty, or on a folder its tasks came from ([Pull again]). */
  onImport: (from?: ImportFrom) => void
}

const taskPath = (id: string) => `/cluster/tasks/${encodeURIComponent(id)}`

const entryClass = (on: boolean) =>
  cn(
    'flex items-center gap-2 w-full px-3 py-2 rounded-xl text-[13px] transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
    on ? 'bg-primary/10 ring-1 ring-primary/20 text-primary font-medium' : 'text-foreground/80 hover:bg-secondary/50',
  )

// The tasks this cluster has, in three groups (built-in, the operator's own,
// Git by repository), with a search over the name and description on screen,
// a way to start a new task or import some from Git, and the run history at
// the foot. Each repository can be pulled again from the folder its tasks
// came from (one button per branch and folder, when they differ).
export function TaskList({ tasks, chosen, onPick, onImport }: TaskListProps) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const sections = taskSections(tasks, query)
  const none = sections.builtin.length + sections.mine.length + sections.git.length === 0

  const entries = (xs: ListedTask[]) =>
    xs.map((x) => {
      const on = chosen.taskId === x.task.id
      return (
        <Link
          key={x.task.id}
          to={taskPath(x.task.id)}
          onClick={onPick}
          aria-current={on ? 'page' : undefined}
          title={x.description || undefined}
          className={entryClass(on)}
        >
          <span className="truncate min-w-0">{x.name}</span>
        </Link>
      )
    })

  const heading = (label: string) => (
    <p className="px-3 pt-2 pb-1 text-[11px] font-semibold text-muted-foreground">{label}</p>
  )

  return (
    <nav aria-label={t('cluster.tasks.list.label')} className="space-y-2">
      <div className="flex items-center gap-2">
        <div className="relative flex-1 min-w-0">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none" />
          <Input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t('cluster.tasks.list.search')}
            aria-label={t('cluster.tasks.list.search')}
            className="pl-9 rounded-xl"
          />
        </div>
        <Link
          to="/cluster/tasks/new"
          onClick={onPick}
          aria-label={t('cluster.tasks.list.new')}
          title={t('cluster.tasks.list.new')}
          className="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-xl hover:bg-secondary outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <Plus className="h-4 w-4" />
        </Link>
        <button
          type="button"
          onClick={() => onImport()}
          aria-label={t('cluster.tasks.list.importGit')}
          title={t('cluster.tasks.list.importGit')}
          className="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-xl hover:bg-secondary outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <GitBranch className="h-4 w-4" />
        </button>
      </div>

      <div className="space-y-1">
        {sections.builtin.length > 0 && (
          <section aria-label={t('cluster.tasks.list.builtin')}>
            {heading(t('cluster.tasks.list.builtin'))}
            {entries(sections.builtin)}
          </section>
        )}
        {sections.mine.length > 0 && (
          <section aria-label={t('cluster.tasks.list.mine')}>
            {heading(t('cluster.tasks.list.mine'))}
            {entries(sections.mine)}
          </section>
        )}
        {sections.git.length > 0 && (
          <section aria-label={t('cluster.tasks.list.git')}>
            {heading(t('cluster.tasks.list.git'))}
            {sections.git.map((repo) => {
              const sources = pullSources(repo.tasks.map((x) => x.task))
              return (
                <section key={repo.label.toLowerCase()} aria-label={repo.label || t('cluster.tasks.list.git')}>
                  <div className="flex items-center gap-1 px-3 pt-1 pb-0.5">
                    <p className="min-w-0 flex-1 text-[11px] text-muted-foreground/80 truncate">{repo.label}</p>
                    {sources.map((from) => {
                      const where = sources.length > 1
                        ? t('cluster.tasks.list.pullFrom', { branch: from.branch, path: from.path || '/' })
                        : undefined
                      return (
                        <button
                          key={`${from.branch}\u0000${from.path}`}
                          type="button"
                          onClick={() => onImport(from)}
                          aria-label={where}
                          title={where ?? `${from.branch} · ${from.path || '/'}`}
                          className="inline-flex shrink-0 items-center gap-1 rounded-lg px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-secondary hover:text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                        >
                          <RefreshCw className="h-3 w-3" />
                          {t('cluster.tasks.list.pullAgain')}
                        </button>
                      )
                    })}
                  </div>
                  {entries(repo.tasks)}
                </section>
              )
            })}
          </section>
        )}
        {none && <p className="px-3 py-2 text-[13px] text-muted-foreground">{t('cluster.tasks.list.noMatch')}</p>}
      </div>

      <div className="border-t border-border pt-2">
        <Link
          to="/cluster/tasks/runs"
          onClick={onPick}
          aria-current={chosen.history ? 'page' : undefined}
          className={entryClass(!!chosen.history)}
        >
          <History className="h-4 w-4 shrink-0" />
          <span className="truncate">{t('cluster.tasks.list.history')}</span>
        </Link>
      </div>
    </nav>
  )
}

interface TaskPickerProps extends TaskListProps {
  /** What is chosen, in words; null when nothing is, and the list is shown whole. */
  label: string | null
}

// The list on a phone: folded under a button naming what is chosen, as the
// More drawer's node picker is. With nothing chosen it stands open.
export function TaskPicker({ label, tasks, chosen, onImport }: TaskPickerProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  if (label === null) return <TaskList tasks={tasks} chosen={chosen} onImport={onImport} />
  return (
    <div className="space-y-1">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="flex items-center gap-2 w-full px-3 py-2.5 rounded-xl bg-secondary/50 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        <ListChecks className="h-4 w-4 text-muted-foreground shrink-0" />
        <span className="text-[13px] font-medium truncate">{label}</span>
        <span className="sr-only">{t('cluster.tasks.list.choose')}</span>
        <ChevronDown className={cn('h-3.5 w-3.5 text-muted-foreground ml-auto shrink-0 transition-transform', open && 'rotate-180')} />
      </button>
      {open && (
        <div className="rounded-xl bg-secondary/30 p-2">
          <TaskList tasks={tasks} chosen={chosen} onPick={() => setOpen(false)} onImport={onImport} />
        </div>
      )}
    </div>
  )
}
