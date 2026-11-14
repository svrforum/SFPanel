import { useSyncExternalStore } from 'react'

/**
 * When the terminal page takes its phone layout: sessions in a drawer, the key
 * bar, and no app sidebar. A narrow window, or any device whose pointer is a
 * finger — width alone gave a phone held sideways (915px) and a tablet (820px)
 * the desktop layout, where the two sidebars left the terminal 42 and 30
 * columns and the key bar a touch keyboard needs was hidden.
 */
export const COMPACT_QUERY = '(max-width: 767px), (pointer: coarse)'

function subscribeCompact(onChange: () => void): () => void {
  const mql = window.matchMedia(COMPACT_QUERY)
  mql.addEventListener('change', onChange)
  return () => mql.removeEventListener('change', onChange)
}

export function useTerminalCompact(): boolean {
  return useSyncExternalStore(subscribeCompact, () => window.matchMedia(COMPACT_QUERY).matches)
}

const FOCUS_KEY = 'sfpanel_terminal_focus'
const FOCUS_EVENT = 'sfpanel:terminal-focus'

// null: not read yet, or another tab wrote it. Held here as well as in
// localStorage so the toggle still works where storage throws (private mode).
let focus: boolean | null = null

export function readTerminalFocus(): boolean {
  if (focus === null) {
    try { focus = localStorage.getItem(FOCUS_KEY) === 'true' } catch { focus = false }
  }
  return focus
}

export function setTerminalFocus(on: boolean): void {
  focus = on
  try { localStorage.setItem(FOCUS_KEY, String(on)) } catch { /* this tab only */ }
  window.dispatchEvent(new Event(FOCUS_EVENT))
}

function subscribeFocus(onChange: () => void): () => void {
  const onStorage = (e: Event) => {
    if ((e as StorageEvent).key !== FOCUS_KEY) return
    focus = null
    onChange()
  }
  window.addEventListener(FOCUS_EVENT, onChange)
  window.addEventListener('storage', onStorage)
  return () => {
    window.removeEventListener(FOCUS_EVENT, onChange)
    window.removeEventListener('storage', onStorage)
  }
}

/**
 * Focus: the operator's choice, on a desktop, to give the app sidebar's width
 * to the terminal. The page's header sets it and the app shell reads it, two
 * components with no parent in common but the router — hence a store, kept in
 * localStorage so the choice outlives a reload.
 */
export function useTerminalFocus(): [boolean, (on: boolean) => void] {
  return [useSyncExternalStore(subscribeFocus, readTerminalFocus), setTerminalFocus]
}
