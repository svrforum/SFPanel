/** What a container's state means for whether anything needs looking at. */
export type ContainerHealth = 'running' | 'unhealthy' | 'restarting' | 'stopped' | 'crashed'

/**
 * Classify a container from the two fields the Docker API already returns.
 *
 * The dashboard computed `stopped = total - running`, which put a container in
 * a crash loop in the same grey box as one deliberately stopped three weeks
 * ago, and painted a running-but-unhealthy container green. Both distinctions
 * are already in hand: `State` carries "restarting", and `Status` carries the
 * "(unhealthy)" suffix and the exit code — the dashboard just never read the
 * second field.
 *
 * "crashed" is a non-zero exit, which is the one an operator wants surfaced;
 * `Exited (0)` is somebody stopping something on purpose and is not news.
 */
export function containerHealth(state: string, status?: string): ContainerHealth {
  const s = (state || '').toLowerCase()
  const text = (status || '').toLowerCase()

  if (s === 'restarting') return 'restarting'
  if (s === 'running' || s === 'up') {
    // "Up 2 hours (unhealthy)" — a healthcheck the container itself declared.
    // "(health: starting)" is not a failure; it is a container still booting.
    return text.includes('(unhealthy)') ? 'unhealthy' : 'running'
  }
  if (s === 'exited' || s === 'dead') {
    const code = exitCode(text)
    return code !== null && code !== 0 ? 'crashed' : 'stopped'
  }
  return 'stopped'
}

/**
 * The code out of "Exited (137) 6 days ago", or null when absent.
 *
 * Lower-cases here rather than trusting the caller to have done it: this is
 * exported, and a helper that silently returns null for correctly-cased input
 * is worse than one that throws.
 */
export function exitCode(status: string): number | null {
  const m = /exited \((\d+)\)/.exec((status || '').toLowerCase())
  return m ? Number(m[1]) : null
}

/** True for the states worth pulling to the top of a five-row summary. */
export function needsAttention(health: ContainerHealth): boolean {
  return health === 'crashed' || health === 'unhealthy' || health === 'restarting'
}

/** Ordering for the dashboard's short list: trouble first, then busiest. */
export function compareForSummary(
  a: { health: ContainerHealth; cpu?: number | null },
  b: { health: ContainerHealth; cpu?: number | null },
): number {
  const at = needsAttention(a.health) ? 0 : 1
  const bt = needsAttention(b.health) ? 0 : 1
  if (at !== bt) return at - bt
  // Containers with no samples yet report null and sort last rather than
  // sorting as zero, which would bury a busy container behind them.
  const ac = a.cpu ?? -1
  const bc = b.cpu ?? -1
  return bc - ac
}
