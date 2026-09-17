import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import type { PtyTab } from '@/lib/sessionRail'

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

  return { tabs, add, reattach, close, rename }
}
