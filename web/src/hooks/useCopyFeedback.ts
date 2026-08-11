import { useCallback, useEffect, useRef, useState } from 'react'
import { copyText } from '@/lib/utils'

/**
 * copyText + the "copied!" flash that every copy button re-implemented with
 * its own useState + setTimeout (WireGuard, ClusterTokens, …). `copiedKey`
 * lets one hook instance serve a list of copy buttons — pass a stable key per
 * button and compare against it for the flash state.
 */
export function useCopyFeedback(resetMs = 2000) {
  const [copiedKey, setCopiedKey] = useState<string | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  useEffect(() => () => clearTimeout(timer.current), [])

  const copy = useCallback(
    async (text: string, key = 'default'): Promise<boolean> => {
      const ok = await copyText(text)
      if (ok) {
        setCopiedKey(key)
        clearTimeout(timer.current)
        timer.current = setTimeout(() => setCopiedKey(null), resetMs)
      }
      return ok
    },
    [resetMs]
  )

  return { copy, copiedKey }
}
