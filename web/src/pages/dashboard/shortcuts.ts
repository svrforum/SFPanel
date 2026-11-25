import { shortcutCandidates } from '@/lib/navigation'

export const SHORTCUTS_KEY = 'sfpanel-dashboard-shortcuts'
export const DEFAULT_SHORTCUTS = ['/terminal', '/files', '/docker', '/logs']

/** The saved dashboard shortcuts: pages the menu still offers, each once, in the saved order, at most four; the defaults when none is left. */
export function readShortcuts(storage: Pick<Storage, 'getItem'>, cluster: boolean): string[] {
  const choices = shortcutCandidates(cluster)
  try {
    const saved: unknown = JSON.parse(storage.getItem(SHORTCUTS_KEY) || 'null')
    if (Array.isArray(saved)) {
      const valid = [...new Set(saved.filter((to): to is string => typeof to === 'string' && choices.some((item) => item.to === to)))].slice(0, 4)
      if (valid.length) return valid
    }
  } catch { /* Restricted storage must not prevent opening the dashboard. */ }
  return DEFAULT_SHORTCUTS
}
