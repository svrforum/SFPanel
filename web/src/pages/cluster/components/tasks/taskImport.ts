import type { ApiWarning } from '@/lib/warnings'
import type { TaskImportFile, TaskImportPreview, TaskImportRequest, TaskImportSource, TaskView } from '@/types/api'

// The Git import dialog's arithmetic: what address the server takes, the
// folders a repository's tasks came from, which files a preview lets the
// operator pick, and the import request a preview leads to.

/** The repository address the server reads (githubapi.ParseRepoURL); a trailing .git is fine. */
const GITHUB_REPO_RE = /^https:\/\/github\.com\/[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/

export function repoUrlValid(url: string): boolean {
  return GITHUB_REPO_RE.test(url.trim())
}

/** The folder a repository path is in, "" for the root. */
export function folderOf(path: string | undefined): string {
  if (!path) return ''
  const cut = path.lastIndexOf('/')
  return cut < 0 ? '' : path.slice(0, cut)
}

/** Where to read again: a repository, a branch and a folder in it. */
export type ImportFrom = Pick<TaskImportSource, 'url' | 'branch' | 'path'>

/**
 * The folders a repository's tasks were imported from, each branch and folder
 * once, in the order the list shows them: what [Pull again] reads. The
 * repository is one whatever case it was typed in (the section is grouped so,
 * as the server matches it), and an empty branch is main, as the server reads it.
 */
export function pullSources(tasks: TaskView[]): ImportFrom[] {
  const out: ImportFrom[] = []
  const seen = new Set<string>()
  for (const task of tasks) {
    const src = task.source
    if (src.kind !== 'git' || !src.repo) continue
    const from = { url: src.repo, branch: src.branch || 'main', path: folderOf(src.path) }
    const key = `${from.branch}\u0000${from.path}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(from)
  }
  return out
}

/** Only a new or changed file can be imported: an unchanged one has nothing to bring, a too large one would fail. */
export function pickable(file: TaskImportFile): boolean {
  return file.state === 'new' || file.state === 'changed'
}

/** The files a preview starts with picked: every new and changed one. */
export function initialPicks(preview: TaskImportPreview): string[] {
  return preview.files.filter(pickable).map((f) => f.path)
}

/**
 * The import of the picked files, sent as the preview answered it: the
 * repository, branch and path the server read, and its commit, so a push in
 * between brings nothing nobody looked at. Files go in the preview's order,
 * and only those it lets the operator pick. The token goes only if given.
 */
export function importRequest(preview: TaskImportPreview, picked: string[], token: string): TaskImportRequest {
  const chosen = new Set(picked)
  const req: TaskImportRequest = {
    url: preview.repo,
    branch: preview.branch,
    path: preview.path,
    commit: preview.commit,
    files: preview.files.filter((f) => pickable(f) && chosen.has(f.path)).map((f) => f.path),
  }
  if (token) req.token = token
  return req
}

/** A commit as the screen shows it: the first seven characters. */
export function shortSha(sha: string | undefined): string {
  return (sha ?? '').slice(0, 7)
}

/** The file a failed import entry names, and its line when the server gave one. */
export function failedLine(w: ApiWarning): { path: string; line: number | null } {
  const line = Number(w.details?.line)
  return { path: w.details?.path ?? '', line: Number.isInteger(line) && line > 0 ? line : null }
}
