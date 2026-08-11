import { api } from '@/lib/api'

export interface WaitForServerBackOptions {
  /** Milliseconds before the first probe (server needs a beat to go down). */
  initialDelayMs?: number
  /** Probe interval. */
  intervalMs?: number
  /** Give up after this many failed probes. 0 = never (avoid — see callers). */
  maxAttempts?: number
  /** Called once the server answers again. Default: full page reload. */
  onBack?: () => void
  /** Called when maxAttempts is exhausted. */
  onTimeout?: () => void
}

/**
 * Poll the unauthenticated /auth/setup-status probe until the panel answers
 * again after a self-restart (update, cluster init/join, restore …), then run
 * onBack (default: reload). The same setTimeout+setInterval loop used to be
 * copy-pasted — with diverging timeouts, including one unbounded variant — in
 * Maintenance (×2) and ClusterOverview (×5).
 *
 * Returns a cancel function; callers unmounting mid-wait should invoke it.
 */
export function waitForServerBack(opts: WaitForServerBackOptions = {}): () => void {
  const {
    initialDelayMs = 3000,
    intervalMs = 2000,
    maxAttempts = 150, // 5 minutes at the default interval
    onBack = () => window.location.reload(),
    onTimeout,
  } = opts

  let interval: ReturnType<typeof setInterval> | undefined
  let cancelled = false
  let attempts = 0

  const timer = setTimeout(() => {
    if (cancelled) return
    interval = setInterval(() => {
      attempts++
      fetch(`${api.apiBase}/auth/setup-status`)
        .then(() => {
          if (cancelled) return
          clearInterval(interval)
          onBack()
        })
        .catch(() => {
          if (cancelled) return
          if (maxAttempts > 0 && attempts >= maxAttempts) {
            clearInterval(interval)
            onTimeout?.()
          }
        })
    }, intervalMs)
  }, initialDelayMs)

  return () => {
    cancelled = true
    clearTimeout(timer)
    if (interval) clearInterval(interval)
  }
}
