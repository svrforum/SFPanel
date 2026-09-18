import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { prunePtyTabs, type PtyTab } from '@/lib/sessionRail'

// Tabs map 1:1 to server PTY sessions and each node keeps its own session
// map, so they are persisted PER NODE: one global key reused the same tab id
// as the session_id on every node and spawned a duplicate PTY per tab on each
// node switch (orphaned until the 5-min idle reaper).
const STORAGE_KEY_BASE = 'sfpanel_terminal_tabs'
const tabsKey = () => `${STORAGE_KEY_BASE}:${api.currentNode || 'local'}`

let tabCounter = 0

function generateTabId() {
  tabCounter++
  return `term-${tabCounter}`
}

function loadTabs(): PtyTab[] {
  try {
    const raw = localStorage.getItem(tabsKey())
    if (!raw) return []
    const tabs = JSON.parse(raw) as PtyTab[]
    if (!Array.isArray(tabs)) return []
    for (const t of tabs) {
      const match = /^term-(\d+)$/.exec(t.id)
      if (match) tabCounter = Math.max(tabCounter, parseInt(match[1], 10))
    }
    return tabs
  } catch {
    return []
  }
}

/**
 * The PTY engine's tab list — the browser's own, one tab per server PTY
 * session id. Unlike the old page it does not seed a first tab: a PTY tab is
 * either the emergency fallback (no tmux) or a deliberate temporary shell,
 * and neither should appear on its own.
 */
export function usePtyTabs() {
  const { t } = useTranslation()
  const [tabs, setTabs] = useState<PtyTab[]>(loadTabs)
  // The ids that came from storage: the only tabs prunePtyTabs may drop, and
  // the only ones held back from the pane below. State with a lazy
  // initializer rather than a ref — this is read during render, and the
  // initializer already captures it once, before anything can add a tab.
  const [restored] = useState<string[]>(() => tabs.map((tb) => tb.id))
  // Whether the server's session list has answered about those restored ids.
  // A browser with none of them is already answered.
  const [checked, setChecked] = useState(restored.length === 0)

  useEffect(() => {
    try { localStorage.setItem(tabsKey(), JSON.stringify(tabs)) } catch { /* private mode */ }
  }, [tabs])

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

  const close = useCallback((id: string) => {
    setTabs((prev) => prev.filter((tb) => tb.id !== id))
  }, [])

  const rename = useCallback((id: string, title: string) => {
    const trimmed = title.trim()
    if (!trimmed) return
    setTabs((prev) => prev.map((tb) => (tb.id === id ? { ...tb, title: trimmed } : tb)))
  }, [])

  /**
   * Drops restored tabs the server no longer has. Called with the ids from a
   * SUCCESSFUL GET /terminal/sessions; `null` says the request failed, which
   * must leave the list alone rather than throw away tabs that may still be
   * alive. Either answer ends the hold on `mountable`.
   */
  const reconcile = useCallback((serverIds: string[] | null) => {
    setChecked(true)
    if (serverIds) setTabs((prev) => prunePtyTabs(prev, serverIds, restored))
  }, [restored])

  /**
   * The tabs the pane may mount. Connecting to a PTY id the server does not
   * know makes it CREATE that session, so mounting a restored tab before the
   * list has answered manufactures exactly the shell prunePtyTabs exists to
   * avoid — and it did, in one run of the e2e regression out of three. Tabs
   * this page created are never held back; nor is anything once checked, so
   * the common case hands the pane the same array identity as `tabs`.
   */
  const mountable = checked ? tabs : tabs.filter((tb) => !restored.includes(tb.id))

  return { tabs, mountable, add, reattach, close, rename, reconcile }
}
