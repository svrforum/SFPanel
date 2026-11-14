import { useCallback, useEffect, useState } from 'react'
import { api } from '@/lib/api'
import type { TerminalPrefs, TerminalSnippet } from '@/types/api'

const NONE: TerminalPrefs = { snippets: [], keysets: {} }

// What the panel answered, held to its shape: a panel that has none answers
// with nothing at all.
function shaped(prefs: Partial<TerminalPrefs> | null | undefined): TerminalPrefs {
  return {
    snippets: Array.isArray(prefs?.snippets) ? prefs.snippets.filter((s) => typeof s?.body === 'string' && (s.kind === 'agent' || s.kind === 'shell')) : [],
    keysets: prefs?.keysets && typeof prefs.keysets === 'object' ? prefs.keysets : {},
  }
}

/**
 * The operator's snippets and key rows, from the node the page is served
 * from. Until they arrive — and if they never do — there are none, and the
 * terminal has the panel's own keys. A save answers whether it was kept.
 */
export function useTerminalPrefs() {
  const [prefs, setPrefs] = useState<TerminalPrefs>(NONE)
  useEffect(() => {
    let live = true
    api.getTerminalPrefs().then((p) => { if (live) setPrefs(shaped(p)) }).catch(() => { /* the panel's own keys, no snippets */ })
    return () => { live = false }
  }, [])
  const saveSnippets = useCallback(async (snippets: TerminalSnippet[]) => {
    try {
      const kept = shaped(await api.putTerminalSnippets(snippets)).snippets
      setPrefs((p) => ({ ...p, snippets: kept }))
      return true
    } catch { return false }
  }, [])
  const saveKeysets = useCallback(async (keysets: TerminalPrefs['keysets']) => {
    try {
      const kept = shaped(await api.putTerminalKeysets(keysets)).keysets
      setPrefs((p) => ({ ...p, keysets: kept }))
      return true
    } catch { return false }
  }, [])
  return { prefs, saveSnippets, saveKeysets }
}
