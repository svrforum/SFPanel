import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PtyTab } from '@/lib/sessionRail'

let tabCounter = 0

function generateTabId() {
  tabCounter++
  return `term-${tabCounter}`
}

/**
 * Pushes the generator past an id that arrived from the server. A PTY session
 * is created by the tab that connects to it, so a session the server reports
 * is named `term-N` by this very generator — and the counter is module state
 * that restarts at zero on every page load. Without this, adopting `term-1`
 * after a reload and then opening a temporary shell hands out `term-1` again:
 * `add` appends unconditionally, so the operator gets two rows and two
 * sockets onto one shell instead of a new one, and closing either drops both.
 * tmux mode never adopts, and the same id would land the operator inside a
 * live shell instead of a new one there, so it notes the list too (`note`).
 */
function noteTabId(id: string) {
  const m = /^term-(\d+)$/.exec(id)
  if (m) tabCounter = Math.max(tabCounter, parseInt(m[1], 10))
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
    noteTabId(sessionId)
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
    // Outside the updater: React may call an updater twice, and the counter
    // must move by what the server said, not by how often it is applied.
    sessionIds.forEach(noteTabId)
    setTabs((prev) => {
      const fresh = sessionIds.filter((id) => !prev.some((tb) => tb.id === id))
      if (fresh.length === 0) return prev
      return [...prev, ...fresh.map((id) => ({
        id,
        title: t('terminal.reattachedTab', { id: id.slice(0, 8), defaultValue: 'Reattached {{id}}' }),
      }))]
    })
  }, [t])

  /**
   * Keeps the id generator past the sessions the server already has, without
   * making tabs of them. This is the tmux-mode half of the same problem
   * `adopt` solves in fallback mode: there the sessions become tabs, here they
   * must not — a temporary shell is a door the operator opens on purpose — but
   * the ids come from this same generator, so `term-1` from an earlier page
   * load (or another device signed in as the same operator) is exactly what
   * the next temporary shell would ask the server for, and the server hands
   * back that live shell, scrollback and all, instead of a new one. `adopt`
   * keeps its own noting rather than leaning on this one, so it cannot be
   * called without it; noting an id twice costs nothing, the counter only
   * ever moves up.
   */
  const note = useCallback((sessionIds: string[]) => {
    sessionIds.forEach(noteTabId)
  }, [])

  const close = useCallback((id: string) => {
    setTabs((prev) => prev.filter((tb) => tb.id !== id))
  }, [])

  const rename = useCallback((id: string, title: string) => {
    const trimmed = title.trim()
    if (!trimmed) return
    setTabs((prev) => prev.map((tb) => (tb.id === id ? { ...tb, title: trimmed } : tb)))
  }, [])

  return { tabs, add, adopt, note, reattach, close, rename }
}
