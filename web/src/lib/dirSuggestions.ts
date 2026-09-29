import type { AIDirs } from '@/types/api'

export type DirSource = 'recent' | 'stack' | 'home'

/** One suggested working directory, with every list the server put it in. */
export interface DirSuggestion { path: string; sources: DirSource[] }

/**
 * The server's three lists as one, each path once. A directory is often both
 * a recent one and a compose stack; listing it twice read as two different
 * places. Order is the lists' own — recent, then stacks, then home — because
 * that is the order the prefill already trusts.
 */
export function mergeDirs(dirs: AIDirs | null): DirSuggestion[] {
  if (!dirs) return []
  const out: DirSuggestion[] = []
  const at = new Map<string, DirSuggestion>()
  const add = (path: string, source: DirSource) => {
    if (!path) return
    const seen = at.get(path)
    if (seen) { if (!seen.sources.includes(source)) seen.sources.push(source); return }
    const s = { path, sources: [source] }
    at.set(path, s); out.push(s)
  }
  dirs.recent.forEach((p) => add(p, 'recent'))
  dirs.stacks.forEach((p) => add(p, 'stack'))
  add(dirs.home, 'home')
  return out
}

/** The last path segment, or the path itself for "/". */
export function dirName(path: string): string {
  const trimmed = path.length > 1 ? path.replace(/\/+$/, '') : path
  return trimmed.slice(trimmed.lastIndexOf('/') + 1) || trimmed
}

/**
 * Narrow to what was typed. A match at the start of the directory's own name
 * ranks first, anywhere in its name second, anywhere in the path last; the
 * merged order breaks ties. A query with a slash is a path being typed, so it
 * is matched against the whole path, prefix first. An empty query keeps
 * everything.
 */
export function filterDirs(list: DirSuggestion[], query: string): DirSuggestion[] {
  const q = query.trim().toLowerCase()
  if (!q) return list
  const rank = (s: DirSuggestion): number => {
    const path = s.path.toLowerCase()
    if (q.includes('/')) return path.startsWith(q) ? 0 : path.includes(q) ? 2 : -1
    const name = dirName(s.path).toLowerCase()
    if (name.startsWith(q)) return 0
    if (name.includes(q)) return 1
    return path.includes(q) ? 2 : -1
  }
  return list
    .map((s, i) => ({ s, i, r: rank(s) }))
    .filter((x) => x.r >= 0)
    .sort((a, b) => a.r - b.r || a.i - b.i)
    .map((x) => x.s)
}
