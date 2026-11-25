/** The settings tabs, in their order on the page; the id is the ?tab= value. */
export const SETTINGS_TABS = ['account', 'browser', 'system', 'https', 'alerts', 'audit'] as const
export type SettingsTab = (typeof SETTINGS_TABS)[number]

function isSettingsTab(value: string | null): value is SettingsTab {
  return (SETTINGS_TABS as readonly (string | null)[]).includes(value)
}

/** The tab an address opens: ?tab= when it names one; the system tab for the old ?scope=node; else account. */
export function settingsTab(search: string): SettingsTab {
  const params = new URLSearchParams(search)
  const tab = params.get('tab')
  if (isSettingsTab(tab)) return tab
  return params.get('scope') === 'node' ? 'system' : 'account'
}

/**
 * Where a tab's values live. The account is the replicated admin row; the
 * language and theme are this browser's. Alerts are one server's like the
 * rest: their rules and channels are local tables nothing replicates, and
 * each server judges with its own.
 */
export function settingsTabScope(tab: SettingsTab): 'fleet' | 'browser' | 'server' {
  if (tab === 'account') return 'fleet'
  if (tab === 'browser') return 'browser'
  return 'server'
}

/** The search an old settings address is replaced by — scope dropped, the tab it meant written, every other parameter kept in order; null when nothing to change. */
export function settingsRedirect(search: string): string | null {
  const params = new URLSearchParams(search)
  const scope = params.get('scope')
  if (scope === null) return null
  params.delete('scope')
  if (scope === 'node' && !isSettingsTab(params.get('tab'))) params.set('tab', 'system')
  const query = params.toString()
  return query ? `?${query}` : ''
}
