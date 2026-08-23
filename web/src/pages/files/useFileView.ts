import { useCallback, useMemo, useState } from 'react'
import type { FileEntry } from '@/types/api'

export type SortKey = 'name' | 'size' | 'modTime'
export type SortDirection = 'asc' | 'desc'

export interface SortState {
  key: SortKey
  direction: SortDirection
}

const SORT_STORAGE_KEY = 'sfpanel-files-sort'
const HIDDEN_STORAGE_KEY = 'sfpanel-files-hidden'
const MODE_STORAGE_KEY = 'sfpanel-files-mode'

/** How the listing is drawn. */
export type ViewMode = 'list' | 'grid'

function readStored<T>(key: string, fallback: T, parse: (raw: string) => T | null): T {
  try {
    const raw = localStorage.getItem(key)
    if (raw === null) return fallback
    return parse(raw) ?? fallback
  } catch {
    // Private windows and blocked site data throw on access rather than
    // returning null, and a preference is not worth failing the page over.
    return fallback
  }
}

function store(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Same reason as above: losing a preference is acceptable, throwing is not.
  }
}

/**
 * View state for a directory listing: how it is ordered and whether dotfiles
 * are shown.
 *
 * The listing was ordered by the server and then re-sorted here in a fixed
 * way — directories first, then name — with no way to change it. Finding the
 * file you just edited in a directory of two hundred meant reading every row,
 * because "most recently modified" was the one order the page could not
 * produce. Dotfiles had the opposite problem: `.env` and `.gitignore` were
 * listed among everything else with no way to hide the noise, and on a home
 * directory that is most of the rows.
 *
 * Both preferences persist. They are per-browser conveniences, not state
 * anyone else needs, so localStorage is the right home for them.
 */
export function useFileView() {
  const [sort, setSort] = useState<SortState>(() =>
    readStored<SortState>(SORT_STORAGE_KEY, { key: 'name', direction: 'asc' }, (raw) => {
      const parsed = JSON.parse(raw) as SortState
      const keys: SortKey[] = ['name', 'size', 'modTime']
      if (!keys.includes(parsed?.key)) return null
      return { key: parsed.key, direction: parsed.direction === 'desc' ? 'desc' : 'asc' }
    }),
  )

  const [showHidden, setShowHidden] = useState<boolean>(() =>
    readStored(HIDDEN_STORAGE_KEY, false, (raw) => raw === 'true'),
  )

  // Clicking the active column flips direction; clicking another switches to
  // it. Size and time start descending — asking for "biggest" or "newest" and
  // getting the smallest and oldest first is never what was meant.
  const toggleSort = useCallback((key: SortKey) => {
    setSort((prev) => {
      const next: SortState =
        prev.key === key
          ? { key, direction: prev.direction === 'asc' ? 'desc' : 'asc' }
          : { key, direction: key === 'name' ? 'asc' : 'desc' }
      store(SORT_STORAGE_KEY, JSON.stringify(next))
      return next
    })
  }, [])

  const toggleHidden = useCallback(() => {
    setShowHidden((prev) => {
      store(HIDDEN_STORAGE_KEY, String(!prev))
      return !prev
    })
  }, [])

  // The grid exists to look at images; the list is better for everything else,
  // so list stays the default and the choice is remembered per browser.
  const [mode, setModeState] = useState<ViewMode>(() =>
    readStored<ViewMode>(MODE_STORAGE_KEY, 'list', (raw) => (raw === 'grid' ? 'grid' : 'list')),
  )
  const setMode = useCallback((next: ViewMode) => {
    store(MODE_STORAGE_KEY, next)
    setModeState(next)
  }, [])

  return { sort, toggleSort, showHidden, toggleHidden, mode, setMode }
}

/** True for dotfiles, which the hidden toggle filters out. */
export function isHiddenEntry(entry: FileEntry) {
  return entry.name.startsWith('.')
}

/**
 * Order a listing.
 *
 * Directories stay first regardless of the column. Sorting a directory listing
 * strictly by size would scatter the folders through it — they all report the
 * same handful of bytes — and navigation is the primary job of this page, so
 * the folders stay where they can be found.
 */
export function sortEntries(entries: FileEntry[], sort: SortState): FileEntry[] {
  const factor = sort.direction === 'asc' ? 1 : -1
  return [...entries].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
    switch (sort.key) {
      case 'size':
        // Directories have no meaningful size, so they fall back to name and
        // keep a stable order rather than shuffling on every re-sort.
        if (a.isDir && b.isDir) return a.name.localeCompare(b.name)
        return (a.size - b.size) * factor
      case 'modTime': {
        const diff = new Date(a.modTime).getTime() - new Date(b.modTime).getTime()
        if (diff !== 0) return diff * factor
        return a.name.localeCompare(b.name)
      }
      default:
        return a.name.localeCompare(b.name) * factor
    }
  })
}

/** Apply the hidden-file preference and the chosen order together. */
export function useVisibleEntries(entries: FileEntry[], sort: SortState, showHidden: boolean) {
  return useMemo(() => {
    const visible = showHidden ? entries : entries.filter((e) => !isHiddenEntry(e))
    return sortEntries(visible, sort)
  }, [entries, sort, showHidden])
}
