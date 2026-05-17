import { useState, useCallback } from 'react'
import { toast } from 'sonner'

export type UseApiActionOpts<TResult> = {
  successMsg?: string
  errorMsg?: string
  onSuccess?: (result: TResult) => void
}

/**
 * useApiAction — wraps an async API call with the toast + loading boilerplate
 * that every settings handler repeats.
 *
 * Use for simple "call API → toast → flip loading" flows. Do NOT use for
 * streams (SSE), blob downloads, or anything with a polling loop — those
 * need bespoke control flow.
 *
 *   const { run, loading } = useApiAction(api.changePassword, {
 *     successMsg: t('settings.passwordChanged'),
 *     errorMsg: t('settings.passwordChangeFailed'),
 *   })
 *   await run(currentPassword, newPassword)
 *
 * Caveats:
 *  - `errorMsg` is a fallback used only when the thrown value is *not* an
 *    Error — in practice api.ts always throws Error, so backend messages
 *    win. This matches the prior inline toast pattern, but means errorMsg
 *    is unreachable for normal API failures.
 *  - `run` is not referentially stable across renders — callers pass inline
 *    `onSuccess` arrows, so the dep list changes each render. Don't put
 *    `run` in another useEffect/useCallback dep array.
 */
export function useApiAction<TArgs extends unknown[], TResult>(
  fn: (...args: TArgs) => Promise<TResult>,
  opts?: UseApiActionOpts<TResult>,
): { run: (...args: TArgs) => Promise<TResult | undefined>; loading: boolean } {
  const [loading, setLoading] = useState(false)

  const run = useCallback(
    async (...args: TArgs): Promise<TResult | undefined> => {
      setLoading(true)
      try {
        const result = await fn(...args)
        if (opts?.successMsg) toast.success(opts.successMsg)
        opts?.onSuccess?.(result)
        return result
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : opts?.errorMsg || 'Error'
        toast.error(message)
        return undefined
      } finally {
        setLoading(false)
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [fn, opts?.successMsg, opts?.errorMsg, opts?.onSuccess],
  )

  return { run, loading }
}
