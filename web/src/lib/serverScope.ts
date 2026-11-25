import { navOwner } from '@/lib/navigation'

/** The last cluster status answer to "is this panel in a cluster": 'true' or 'false'; absent before the first one. */
export const CLUSTER_MODE_KEY = 'sfpanel-cluster-mode'

/**
 * The id of the server this panel runs on, as the last cluster status gave it.
 * A cached fact, not a choice: it lets the first paint tell the attached
 * server's own id in the address from another server's.
 */
export const LOCAL_ID_KEY = 'sfpanel-cluster-local-id'

/** The old cluster tree's saved selection and folds; cleared at boot. */
export const RETIRED_KEYS = ['sfpanel-cluster-selection', 'sfpanel-cluster-tree-collapsed', 'sfpanel-cluster-menu-collapsed', 'sfpanel-tree-collapsed']

/**
 * What a screen acts on: one server, all of them, or saved settings — the
 * ones every server shares, or one server's own.
 */
export type ScopeKind = 'server' | 'fleet' | 'savedFleet' | 'savedServer'

/** The server the address names; null without one or with an empty one. */
export function nodeParam(search: string): string | null {
  return new URLSearchParams(search).get('node') || null
}

/**
 * The stored choice for a server: null for the one this panel runs on, which
 * every request then reaches without ?node=, and which pages and per-server
 * keys read as "not remote".
 */
export function storeValue(id: string, localId: string | null): string | null {
  return localId && id === localId ? null : id
}

/** The server an address names for a stored choice: the attached one by its own id. */
export function urlValue(stored: string | null, localId: string): string {
  return stored ?? localId
}

/** A query string with node set to id, or without node for null; the page's own parameters keep their order. '' when nothing is left. */
export function searchWithNode(search: string, id: string | null): string {
  const params = new URLSearchParams(search)
  if (id === null) params.delete('node')
  else params.set('node', id)
  const query = params.toString()
  return query ? `?${query}` : ''
}

/** A path with an optional query string, naming the server id. */
export function hrefWithNode(href: string, id: string): string {
  const at = href.indexOf('?')
  const path = at < 0 ? href : href.slice(0, at)
  const search = at < 0 ? '' : href.slice(at)
  return path + searchWithNode(search, id)
}

/** What the screen at this address acts on; null for one no menu entry claims. Settings act on one server only with scope=node. */
export function scopeKind(pathname: string, search: string): ScopeKind | null {
  const owner = navOwner(pathname)
  if (!owner) return null
  switch (owner.entry.scope) {
    case 'server':
      return 'server'
    case 'fleet':
      return 'fleet'
    case 'mixed':
      return new URLSearchParams(search).get('scope') === 'node' ? 'savedServer' : 'savedFleet'
  }
}

/** Whether the address of this screen names the server it acts on. */
export function carriesNode(pathname: string, search: string): boolean {
  const kind = scopeKind(pathname, search)
  return kind === 'server' || kind === 'savedServer'
}

/**
 * Before the first paint: a server named in the address is the one the page
 * asks, over the one stored from an earlier visit, so a bookmark or a new tab
 * opens the server it names from the first request. A panel known to be a
 * single server takes no ?node= from the address; one whose mode is not known
 * yet (a first visit) does. The old tree's keys are cleared whatever the address.
 */
export function adoptUrlNode(search: string, storage: Storage, setNode: (id: string | null) => void): void {
  for (const key of RETIRED_KEYS) storage.removeItem(key)
  if (storage.getItem(CLUSTER_MODE_KEY) === 'false') return
  const id = nodeParam(search)
  if (id === null) return
  setNode(storeValue(id, storage.getItem(LOCAL_ID_KEY)))
}
