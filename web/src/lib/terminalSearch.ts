import type { ISearchOptions } from '@xterm/addon-search'

/** Enter in the search box goes towards the older output, as a terminal's
 *  search does: the newest match is where it starts. */
export type SearchDirection = 'older' | 'newer'

export interface SearchOutcome {
  found: boolean
  /** history: everything the session has kept. screen: only what it shows now
   *  — a program in the alternate screen keeps its history to itself, and a
   *  tmux session's is out of reach without the panel's copy of it. */
  scope: 'history' | 'screen'
}

/** The active session's search counted its matches (detail: { index, count },
 *  index -1 when there are too many to place the current one). */
export const SEARCH_RESULTS_EVENT = 'sfpanel:terminal-search-results'

/** Every match marked, the current one stronger; in the theme's own contrast. */
export function searchOptions(): ISearchOptions {
  const dark = document.documentElement.classList.contains('dark')
  return {
    decorations: dark
      ? { matchBackground: '#5b4a12', matchOverviewRuler: '#5b4a12', activeMatchBackground: '#b45309', activeMatchColorOverviewRuler: '#b45309' }
      : { matchBackground: '#fde68a', matchOverviewRuler: '#fde68a', activeMatchBackground: '#fdba74', activeMatchColorOverviewRuler: '#fdba74' },
  }
}
