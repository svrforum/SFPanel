import { useCallback, useEffect, useRef, useState } from 'react'
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

/** 'loading' until the panel has answered once; 'failed' while it has not and the last asking failed. */
export type PrefsStatus = 'loading' | 'ready' | 'failed'
export type PrefsChange = 'saved' | 'unchanged' | 'failed'

/**
 * The operator's snippets and key rows, from the node the page is served
 * from. Until the panel has answered there are none to show, and the terminal
 * has the panel's own keys.
 *
 * A save is the whole value, so it must start from what the panel holds and
 * never from what the page happens to be showing:
 *
 *   - Nothing is saved before the panel has been asked successfully. A page
 *     that loaded while the panel was restarting shows an empty list, and
 *     "add one" to that would replace a hundred snippets with one.
 *   - Changes run one at a time, each from the answer to the one before it.
 *     Two taps inside one round trip would otherwise both start from the same
 *     list, and the second would undo the first.
 */
export function useTerminalPrefs() {
  const [prefs, setPrefs] = useState<TerminalPrefs>(NONE)
  const [status, setStatus] = useState<PrefsStatus>('loading')
  // What the panel last said; null until it has.
  const held = useRef<TerminalPrefs | null>(null)
  const queue = useRef<Promise<unknown>>(Promise.resolve())

  const run = useCallback(<T,>(job: (held: TerminalPrefs) => Promise<T>, failed: T): Promise<T> => {
    const took = (next: TerminalPrefs) => {
      held.current = next
      setPrefs(next)
      setStatus('ready')
      return next
    }
    const step = queue.current.then(async () => {
      let known = held.current
      if (!known) {
        try {
          known = took(shaped(await api.getTerminalPrefs()))
        } catch {
          setStatus('failed')
          return failed
        }
      }
      try {
        return await job(known)
      } catch {
        return failed
      }
    })
    queue.current = step
    return step
  }, [])
  const keep = useCallback((next: TerminalPrefs) => {
    held.current = next
    setPrefs(next)
  }, [])

  /** Asks the panel if it has not answered yet. Answers whether it has now. */
  const load = useCallback(() => run(async () => true, false), [run])
  useEffect(() => { void load() }, [load])

  /** Changes the list the panel holds; `change` answers null for nothing to do. */
  const changeSnippets = useCallback((change: (list: TerminalSnippet[]) => TerminalSnippet[] | null) => run<PrefsChange>(async (known) => {
    const next = change(known.snippets)
    if (next === null) return 'unchanged'
    const kept = shaped(await api.putTerminalSnippets(next)).snippets
    keep({ ...(held.current ?? known), snippets: kept })
    return 'saved'
  }, 'failed'), [run, keep])

  /** The key rows the panel holds, for the editor to start from; null when it cannot be asked. */
  const loadKeysets = useCallback(() => run<TerminalPrefs['keysets'] | null>(async (known) => known.keysets, null), [run])
  const saveKeysets = useCallback((keysets: TerminalPrefs['keysets']) => run(async (known) => {
    const kept = shaped(await api.putTerminalKeysets(keysets)).keysets
    keep({ ...(held.current ?? known), keysets: kept })
    return true
  }, false), [run, keep])

  return { prefs, status, load, changeSnippets, loadKeysets, saveKeysets }
}
