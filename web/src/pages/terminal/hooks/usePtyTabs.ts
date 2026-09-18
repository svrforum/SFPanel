import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PtyTab } from '@/lib/sessionRail'

let tabCounter = 0

function generateTabId() {
  tabCounter++
  return `term-${tabCounter}`
}

/**
 * The PTY engine's tab list — one tab per server PTY session id, for this
 * page's life only. Nothing is persisted and nothing is read back from
 * storage: a tab is a pointer to a server-side session the idle reaper takes
 * five minutes after its last reader, and the server creates a NEW session
 * for an id it does not know, so a stored tab would open a phantom shell
 * rather than report itself gone. In fallback mode the list is derived from
 * the server instead (see `adopt`); otherwise a tab exists only because the
 * operator just opened or reattached one. Unlike the old page it never seeds
 * a first tab: a PTY tab is either the emergency fallback (no tmux) or a
 * deliberate temporary shell, and neither should appear on its own.
 */
export function usePtyTabs() {
  const { t } = useTranslation()
  const [tabs, setTabs] = useState<PtyTab[]>([])

  const add = useCallback(() => {
    const id = generateTabId()
    const num = tabCounter
    setTabs((prev) => [...prev, { id, title: t('terminal.tabTitle', { n: num, defaultValue: 'Terminal {{n}}' }) }])
    return id
  }, [t])

  const reattach = useCallback((sessionId: string) => {
    setTabs((prev) => prev.some((tb) => tb.id === sessionId)
      ? prev
      : [...prev, { id: sessionId, title: t('terminal.reattachedTab', { id: sessionId.slice(0, 8), defaultValue: 'Reattached {{id}}' }) }])
    return sessionId
  }, [t])

  /**
   * Adopts the server's PTY sessions as tabs — the fallback mode's reload
   * path. The list is authoritative: a session the server reports is alive
   * and reattaching it resumes that shell, while a tab read back from
   * localStorage could name a session reaped five minutes ago, and the server
   * creates a new session for an id it does not know. Ids already open are
   * left alone so an adopt cannot duplicate or reset a tab.
   */
  const adopt = useCallback((sessionIds: string[]) => {
    setTabs((prev) => {
      const fresh = sessionIds.filter((id) => !prev.some((tb) => tb.id === id))
      if (fresh.length === 0) return prev
      return [...prev, ...fresh.map((id) => ({
        id,
        title: t('terminal.reattachedTab', { id: id.slice(0, 8), defaultValue: 'Reattached {{id}}' }),
      }))]
    })
  }, [t])

  const close = useCallback((id: string) => {
    setTabs((prev) => prev.filter((tb) => tb.id !== id))
  }, [])

  const rename = useCallback((id: string, title: string) => {
    const trimmed = title.trim()
    if (!trimmed) return
    setTabs((prev) => prev.map((tb) => (tb.id === id ? { ...tb, title: trimmed } : tb)))
  }, [])

  return { tabs, add, adopt, reattach, close, rename }
}
