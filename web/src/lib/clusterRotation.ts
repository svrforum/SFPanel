import type { TFunction } from 'i18next'
import type { CARotation } from '@/types/api'

// What the security page reads from GET /cluster/rotation and from a refused
// rotation, kept out of the components so it can be tested without a DOM.

/**
 * Refusals that say why nothing started: the leader's gate names the nodes it
 * waits for, and a CA rotation waits for a running update. The screen shows
 * them under one title with the reason.
 */
export const ROTATION_REFUSED_CODES = ['CLUSTER_ROTATION_REFUSED', 'UPDATE_IN_PROGRESS']

export interface RotationFailure {
  refused: boolean
  message: string
  /** A refusal's own title, when it is not that the rotation did not start. */
  title?: string
}

export function rotationFailure(err: unknown): RotationFailure {
  const { code } = (err ?? {}) as { code?: string }
  const message = err instanceof Error ? err.message : String(err)
  return { refused: !!code && ROTATION_REFUSED_CODES.includes(code), message }
}

// The reasons the leader gives for a node a rotation waits for, word for word
// (agent_register.go, proxy_rotation.go, ca_rotation.go). Any other is shown
// as sent: a newer leader may give one this page does not know.
const WAITING_REASONS: Record<string, string> = {
  'offline': 'cluster.security.waitingLine.offline',
  'not heard from': 'cluster.security.waitingLine.notHeard',
  'has not taken the next secret yet': 'cluster.security.waitingLine.nextSecret',
  'does not trust the next CA yet': 'cluster.security.waitingLine.trustNext',
  'still presents a certificate from the current CA': 'cluster.security.waitingLine.certCurrent',
  'still trusts the old CA': 'cluster.security.waitingLine.trustsOld',
  'still presents a certificate from the next CA': 'cluster.security.waitingLine.certNext',
  'still trusts the next CA': 'cluster.security.waitingLine.trustsNext',
}

/**
 * One of the leader's waiting lines, "<name> (<reason>)", in the page's
 * language. A bare version as the reason means the node runs a release that
 * cannot rotate. The phase is the leader's word, as the CA stepper shows it.
 */
export function waitingLine(line: string, t: TFunction): string {
  const m = /^(.+) \(([^()]+)\)$/.exec(line)
  if (!m) return line
  const [, name, reason] = m
  const key = WAITING_REASONS[reason]
  if (key) return t(key, { name })
  const since = /^not heard from since the (\S+) phase began$/.exec(reason)
  if (since) return t('cluster.security.waitingLine.notHeardSince', { name, phase: since[1] })
  if (/^v?\d[\w.+-]*$/.test(reason)) return t('cluster.security.waitingLine.oldVersion', { name, version: reason })
  return line
}

/** The way forward, and the branch a stopped or timed-out rotation takes back. */
export const CA_STEPS = ['trust', 'reissue', 'promote', 'done'] as const
export const CA_BACK_STEPS = ['rollback', 'drop', 'aborted'] as const

/** The phase timeouts the start dialog offers; the leader's default is 30m. */
export const PHASE_TIMEOUTS = ['15m', '30m', '1h', '2h'] as const
export const DEFAULT_PHASE_TIMEOUT = '30m'

export interface CAStepper {
  branch: 'forward' | 'back'
  steps: readonly string[]
  /** -1: a phase this release does not know. */
  current: number
}

export function caStepper(phase: CARotation['phase']): CAStepper {
  const back = (CA_BACK_STEPS as readonly string[]).indexOf(phase)
  if (back >= 0) return { branch: 'back', steps: CA_BACK_STEPS, current: back }
  return { branch: 'forward', steps: CA_STEPS, current: (CA_STEPS as readonly string[]).indexOf(phase) }
}

/**
 * Before promote every node still trusts the current CA, so a stop goes back
 * to it. Past promote a node may have dropped it, and the way back is
 * another rotation: the server refuses the stop then.
 */
export function caCanAbort(phase: CARotation['phase'] | undefined): boolean {
  return phase === 'trust' || phase === 'reissue'
}

/** Go's CARotation.Active: a new rotation is refused while one of these runs. */
export function caActive(phase: CARotation['phase'] | undefined): boolean {
  return phase === 'trust' || phase === 'reissue' || phase === 'promote' || phase === 'rollback' || phase === 'drop'
}

/** Time left until deadline as mm:ss or h:mm:ss; 00:00 once it has passed. */
export function countdown(deadline: string, now: number): string {
  const ms = Date.parse(deadline) - now
  const total = Number.isFinite(ms) && ms > 0 ? Math.floor(ms / 1000) : 0
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const ss = String(s).padStart(2, '0')
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${String(m).padStart(2, '0')}:${ss}`
}

/**
 * Which CA a pin is to the rotation. Until it ends the old one is the
 * current CA and the other the next; once done the next one is the cluster's
 * and the old one previous, and an aborted rotation's next CA is no one's.
 */
export function pinRole(
  pin: string,
  rotation: Pick<CARotation, 'phase' | 'old_pin' | 'next_pin'>,
): 'current' | 'next' | 'previous' | null {
  if (!pin) return null
  const isOld = pin === rotation.old_pin
  const isNext = pin === rotation.next_pin
  if (rotation.phase === 'done') return isNext ? 'current' : isOld ? 'previous' : null
  if (rotation.phase === 'aborted') return isOld ? 'current' : null
  return isOld ? 'current' : isNext ? 'next' : null
}

/** A pin short enough for a table cell: the first 12 hex digits. */
export function shortPin(pin: string): string {
  const hex = pin.startsWith('sha256:') ? pin.slice('sha256:'.length) : pin
  return hex.length > 12 ? `${hex.slice(0, 12)}…` : hex
}
