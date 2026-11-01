import type { RunRecord, TaskView } from '@/types/api'

/** A task as the list shows it: name and description in the screen's words. */
export interface ListedTask {
  task: TaskView
  name: string
  description: string
}

export interface GitRepoSection {
  /** The repository as the operator reads it: acme/ops for https://github.com/acme/ops. */
  label: string
  tasks: ListedTask[]
}

export interface TaskSections {
  builtin: ListedTask[]
  mine: ListedTask[]
  git: GitRepoSection[]
}

/** acme/ops for https://github.com/acme/ops (a trailing slash or .git dropped). */
export function repoLabel(repo: string | undefined): string {
  if (!repo) return ''
  return repo.replace(/^https?:\/\/(www\.)?github\.com\//i, '').replace(/\.git$/i, '').replace(/\/+$/, '')
}

/**
 * Splits the list into built-in, the operator's own and Git tasks, the Git
 * ones by repository (one repository whatever the case it was typed in, as
 * the server matches it), keeping the server's order inside each. query
 * matches the name or description on screen, case aside.
 */
export function taskSections(tasks: ListedTask[], query: string): TaskSections {
  const q = query.trim().toLowerCase()
  const shown = q
    ? tasks.filter((x) => x.name.toLowerCase().includes(q) || x.description.toLowerCase().includes(q))
    : tasks
  const out: TaskSections = { builtin: [], mine: [], git: [] }
  const repos = new Map<string, GitRepoSection>()
  for (const x of shown) {
    const kind = x.task.source.kind
    if (kind === 'builtin') out.builtin.push(x)
    else if (kind === 'git') {
      const label = repoLabel(x.task.source.repo)
      const key = label.toLowerCase()
      let repo = repos.get(key)
      if (!repo) {
        repo = { label, tasks: [] }
        repos.set(key, repo)
        out.git.push(repo)
      }
      repo.tasks.push(x)
    } else out.mine.push(x)
  }
  return out
}

const ACTIVE: ReadonlySet<string> = new Set(['running', 'stopping', 'handing_over'])

/** The runs not yet ended, in the server's order (newest first). */
export function activeRuns(runs: RunRecord[]): RunRecord[] {
  return runs.filter((r) => ACTIVE.has(r.status))
}
