import type { RAIDArray } from '@/types/api'

/** How an array's state reads: fine, needs watching, broken, or not known. */
export type RaidTone = 'ok' | 'warn' | 'bad' | 'other'

const BAD = ['failed', 'inactive']
const WARN = ['degraded', 'rebuilding', 'recovering', 'resyncing']
const OK = ['active', 'clean']

// mdadm --detail joins what it knows with commas: "clean, degraded",
// "active, degraded, recovering", "clean, FAILED".
function stateWords(state: string): string[] {
  return state.toLowerCase().split(/[\s,]+/)
}

/** The colour of an mdadm state, read word by word ("clean, degraded"). */
export function raidTone(state: string): RaidTone {
  const words = stateWords(state)
  if (words.some((w) => BAD.includes(w))) return 'bad'
  if (words.some((w) => WARN.includes(w))) return 'warn'
  if (words.some((w) => OK.includes(w))) return 'ok'
  return 'other'
}

/** Broken enough for the dashboard: degraded, failed or inactive, or a failed member. */
export function raidNeedsAttention(a: Pick<RAIDArray, 'state' | 'failed'>): boolean {
  // A resync alone is the array checking itself, not a fault.
  return raidTone(a.state) === 'bad' || stateWords(a.state).includes('degraded') || a.failed > 0
}
