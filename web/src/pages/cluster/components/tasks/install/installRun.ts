import type { TFunction } from 'i18next'
import type { RunNodeRecord, RunRecord, RunStatus } from '@/types/api'
import type { ApiError } from '@/lib/api'
import { CODE_DEFAULTS, CODE_DETAIL_FORMATS, CODE_MESSAGES } from '@/lib/codeMessages'
import { taskErrorMessage } from '@/lib/taskErrors'

// An agent install seen through its run record: rows are servers (s1, s2 …),
// and every row goes through the same five phases, which the screen names
// (cluster.tasks.builtins.agentInstall.steps.<i>): connect, pre-flight check,
// install, register, connection check.

export const AGENT_INSTALL_TASK_ID = 'builtin/agent-install'
export const INSTALL_STEP_COUNT = 5

export function isInstallRun(run: RunRecord): boolean {
  return run.kind === 'agent-install'
}

/** A row's name: the node name asked for, else the host as given, else its key. */
export function installRowName(key: string, record: RunNodeRecord | undefined): string {
  return record?.name || record?.host || key
}

// ---- What a waiting row shows ----

export interface CheckLink {
  href: string
  host: string
}

/**
 * The Tailscale SSH check address as a link the page may draw: an https
 * address, with the host to show beside it so the operator sees where it
 * goes. Anything else — another scheme, a relative address, an address with
 * a user in it (https://login.example@evil.example/) — is null. The address
 * comes from the server being installed, not from the panel.
 */
export function safeCheckURL(url: string | undefined): CheckLink | null {
  if (!url) return null
  let u: URL
  try {
    u = new URL(url)
  } catch {
    return null
  }
  if (u.protocol !== 'https:' || !u.hostname || u.username || u.password) return null
  return { href: u.href, host: u.host }
}

// What a row waiting on the operator asks: host_key the whole fingerprint to
// compare with the server's own (never shortened), check the sign-in link
// (null when the address is not one to follow), standalone and review a yes
// or no.
export type InstallBanner =
  | { kind: 'host_key'; hostKey: string }
  | { kind: 'check'; link: CheckLink | null }
  | { kind: 'standalone' }
  | { kind: 'review' }

/**
 * The question a row is waiting on, or null when it waits on nothing: a row
 * that is not running, a host key wait with no key to approve, or a question
 * this screen does not know (a newer leader's). A Tailscale check is asked
 * whatever the row's status: a revert's cleanup dials a failed server again,
 * and its tailnet may ask for a check there.
 */
export function installBanner(record: RunNodeRecord | undefined): InstallBanner | null {
  if (!record) return null
  if (record.awaiting === 'check') return { kind: 'check', link: safeCheckURL(record.check_url) }
  if (record.status !== 'running') return null
  switch (record.awaiting) {
    case 'host_key':
      return record.host_key ? { kind: 'host_key', hostKey: record.host_key } : null
    case 'standalone':
      return { kind: 'standalone' }
    case 'review':
      return { kind: 'review' }
    default:
      return null
  }
}

// ---- Credentials and cleanup ----

/**
 * Whole seconds until the leader drops the install's credentials (rounded up,
 * so it reads 0 only once they are gone), 0 after; null when the run keeps no
 * deadline: it is under way, or nothing is left to recheck or revert.
 */
export function credentialsLeft(until: string | undefined, now: number): number | null {
  if (!until) return null
  const at = Date.parse(until)
  if (Number.isNaN(at)) return null
  return Math.max(0, Math.ceil((at - now) / 1000))
}

/**
 * The command that removes SFPanel from a server by hand, once the
 * credentials are gone: the installed version's own install.sh, from its
 * release tag. A version that is no release tag (a development build) takes
 * main's script, and is never pasted into the shell line.
 */
export function cleanupCommand(version: string): string {
  const ref = /^\d+\.\d+\.\d+$/.test(version) ? `v${version}` : 'main'
  return `curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/${ref}/scripts/install.sh | sudo bash -s -- uninstall --purge`
}

/** Seconds as m:ss, for the credentials' countdown. */
export function clockText(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

// The run statuses under which an install still moves (tasks_store.go).
const UNDER_WAY: ReadonlySet<RunStatus> = new Set<RunStatus>(['running', 'stopping', 'handing_over'])

/**
 * Whether the leader has dropped the install's credentials: its deadline
 * passed, or the install ended keeping none (a success, a stop, a failure
 * no recheck or revert could use, or a leader change). The leader clears
 * the deadline when it drops them, so an ended install with no deadline has
 * none left.
 */
export function credentialsGone(run: RunRecord, now: number): boolean {
  const left = credentialsLeft(run.credentials_until, now)
  if (left !== null) return left === 0
  return !UNDER_WAY.has(run.status)
}

const LEADER_CHANGED = 'AGENT_INSTALL_LEADER_CHANGED'
const NOT_FOUND = 'AGENT_INSTALL_NOT_FOUND'
// The install phase: from its start, install.sh may have left SFPanel on the server.
const INSTALL_PHASE = 2

/** Whether a row's install phase began (the supervisor's installBegan). */
function installBegan(record: RunNodeRecord): boolean {
  const install = record.steps?.[INSTALL_PHASE]?.status
  return install !== undefined && install !== 'pending' && install !== 'skipped'
}

/**
 * Whether a server needs the cleanup command run on it by hand, because
 * nothing else will clean it: a registered server that failed and was not
 * taken back, once the credentials are gone; a revert that removed the node
 * but could not clean the server; or a server a leader change left after its
 * install had started. A server that failed before it registered was cleaned
 * over SSH by the install itself. A registered server that was stopped stays
 * in the cluster until it is reverted, and needs the command once it was.
 * Never a server that had SFPanel before the install (sfpanel_before): the
 * command purges SFPanel and its data, which were the operator's.
 */
export function needsCleanup(record: RunNodeRecord | undefined, gone: boolean): boolean {
  if (!record || record.sfpanel_before) return false
  if (record.status !== 'failed' && record.status !== 'stopped') return false
  const d = record.reason_details ?? {}
  switch (d.reverted) {
    case 'cleanup_failed':
      return true
    case 'removed':
      return d.standalone !== '1'
    case 'removing':
    case 'purged':
      return false
  }
  if (record.status === 'stopped') return false
  if (record.reason === LEADER_CHANGED) return !record.node_id && installBegan(record)
  return Boolean(record.node_id) && gone
}

/** The command that starts a server's own panel again. */
export const PANEL_START_COMMAND = 'sudo systemctl start sfpanel'

export interface KeptPanelNote {
  text: string
  /** Whether to ask for its panel to be started with PANEL_START_COMMAND. */
  start: boolean
}

/**
 * What a server that had SFPanel before the install (sfpanel_before) is told
 * once its row failed or was stopped: nothing was removed from it, whether
 * its panel, stopped by install.sh, was started again (reason_details.panel),
 * and to start it with PANEL_START_COMMAND if it may be stopped: the start
 * failed, or a leader change cut the install off after it began on a server
 * that did not register. null for any other row, and for one whose revert
 * already said it was left as it is (standalone).
 */
export function keptPanelNote(record: RunNodeRecord | undefined, t: TFunction): KeptPanelNote | null {
  if (!record?.sfpanel_before || (record.status !== 'failed' && record.status !== 'stopped')) return null
  const d = record.reason_details ?? {}
  if (d.standalone === '1') return null
  const kept = t('cluster.tasks.install.run.kept')
  const text = d.panel === 'restarted' ? `${kept} ${t('cluster.tasks.install.run.panelRestarted')}` : kept
  const cut = record.reason === LEADER_CHANGED && !record.node_id && installBegan(record)
  return { text, start: d.panel === 'restart_failed' || cut }
}

export interface InstallRowActions {
  recheck: boolean
  revert: boolean
  openNode: boolean
}

/**
 * What can be done about a server: Recheck and Revert for one that
 * registered and failed, not yet taken back, while the leader that ran the
 * install still holds it (a leader change ends that; a stopped install
 * takes no recheck, a revert still removes the node); Revert alone for one
 * stopped after it registered (the stop ended the session's context and
 * dropped the credentials, so the revert removes the node); Open node for
 * one that became an agent, its group label missing or not. An install
 * never runs again.
 */
export function installRowActions(run: RunRecord, record: RunNodeRecord | undefined): InstallRowActions {
  const none = { recheck: false, revert: false, openNode: false }
  if (!record) return none
  if (record.status === 'succeeded') return { ...none, openNode: Boolean(record.node_id) }
  if ((record.status !== 'failed' && record.status !== 'stopped') || !record.node_id) return none
  if (record.reason === LEADER_CHANGED) return { ...none, openNode: true }
  if (run.reason === LEADER_CHANGED || record.reason_details?.reverted) return none
  if (record.status === 'stopped') return { ...none, revert: true }
  return { recheck: run.status !== 'stopped', revert: true, openNode: false }
}

export interface InstallReview {
  /** The servers waiting for the review, in run order. */
  waiting: string[]
  /** Whether [Continue install] is offered. */
  open: boolean
  /** How many servers passed their pre-flight check and wait, of how many. */
  passed: number
  total: number
}

/**
 * Where an install's review stands. [Continue install] is offered once every
 * server waits for the review or has ended, and one waits: the leader goes
 * on with the servers waiting when it is answered, so an answer given while
 * another server is still connecting, checking or asking would leave that
 * one to wait again. Until then, an install that asked for a review says how
 * many servers passed. null when there is nothing to say: an install that
 * asked for none, one past its review (every server went on or ended), or
 * one that ended.
 */
export function installReview(run: RunRecord, nodes: Record<string, RunNodeRecord>): InstallReview | null {
  const waiting = run.nodes.filter((key) => installBanner(nodes[key])?.kind === 'review')
  // Still on its way to the review: connecting, in its pre-flight check, or
  // asking about its host key, a check or its standalone panel.
  const before = run.nodes.filter((key) => {
    const record = nodes[key]
    if (!record) return true
    if (record.status !== 'pending' && record.status !== 'running') return false
    return !waiting.includes(key) && !installBegan(record)
  })
  const asked = run.inputs?.review === 'true' && UNDER_WAY.has(run.status)
  if (waiting.length === 0 && (!asked || before.length === 0)) return null
  return { waiting, open: waiting.length > 0 && before.length === 0, passed: waiting.length, total: run.nodes.length }
}

/**
 * A Recheck or Revert refused, in the operator's language. 404
 * AGENT_INSTALL_NOT_FOUND means the leader no longer holds the session that
 * ran the install (the leader changed, or another install started): nothing
 * here can undo it, so the sentence says what to do by hand. A server that
 * had SFPanel before is not to be cleaned.
 */
export function rowActionErrorText(err: unknown, record: RunNodeRecord | undefined, t: TFunction): string {
  if ((err as ApiError | undefined)?.code === NOT_FOUND) {
    return record?.sfpanel_before ? t('cluster.tasks.install.run.sessionGoneKept') : t('cluster.tasks.install.run.sessionGone')
  }
  return taskErrorMessage(err, t)
}

/**
 * The install's own reason, in the operator's language: a leader change
 * ends it with a code; anything else shows as sent.
 */
export function installRunReasonText(reason: string | undefined, t: TFunction): string | null {
  if (!reason) return null
  if (reason === LEADER_CHANGED) return t('cluster.tasks.install.run.leaderChanged')
  return reason
}

// ---- Why a row ended ----

type Details = Record<string, string>
type Words = (t: TFunction, params: Details) => string

// A closer sentence than the code's own where the details call for one:
// which of two things failed, or a cause with its own advice. null keeps the
// code's sentence (CODE_MESSAGES). Every key is written out whole, so
// locales.test checks it in both languages.
const VARIANTS: Record<string, (d: Details, record: RunNodeRecord) => Words | null> = {
  // A changed key names the key the server presents now and the approved
  // one; a server with no key of the approved type presented none. An answer
  // the leader refused carries host_key alone, in the code's own sentence.
  AGENT_INSTALL_HOST_KEY_CHANGED: (d) => {
    if (!d.approved) return null
    return d.host_key
      ? (t, p) => t('cluster.tasks.install.reason.hostKeyChanged', p)
      : (t, p) => t('cluster.tasks.install.reason.hostKeyChangedType', p)
  },
  AGENT_INSTALL_UNREACHABLE: (d) => (d.host ? (t, p) => t('cluster.tasks.install.reason.unreachableHost', p) : null),
  AGENT_INSTALL_AUTH_FAILED: (d) => (d.check === 'timeout' ? (t) => t('cluster.tasks.install.reason.authCheckTimeout') : null),
  AGENT_INSTALL_NAME_INVALID: (d) => ('hostname' in d || !d.name ? (t, p) => t('cluster.tasks.install.reason.nameInvalidHostname', p) : null),
  AGENT_INSTALL_IDENTITY_TAKEN: (d) => {
    if (!d.row) return null
    return d.ip ? (t, p) => t('cluster.tasks.install.reason.identityTakenIp', p) : (t, p) => t('cluster.tasks.install.reason.identityTakenName', p)
  },
  AGENT_INSTALL_FAILED: (d) => {
    if (d.no_answer) return (t, p) => t('cluster.tasks.install.reason.failedNoAnswer', p)
    if (d.step === 'labels') return (t) => t('cluster.tasks.install.reason.failedLabels')
    if (d.step === 'register') return (t, p) => t('cluster.tasks.install.reason.failedRegister', p)
    if (d.timeout_minutes) return (t, p) => t('cluster.tasks.install.reason.failedTimeout', p)
    if (d.step && d.exit) return (t, p) => t('cluster.tasks.install.reason.failedExit', p)
    if (d.step) return (t, p) => t('cluster.tasks.install.reason.failedStep', p)
    if (d.error) return (t, p) => t('cluster.tasks.install.reason.failedError', p)
    return null
  },
  AGENT_INSTALL_OFFLINE: (d) => {
    switch (d.cause) {
      case 'agent_to_managers': return (t, p) => t('cluster.tasks.install.reason.offlineAgentToManagers', p)
      case 'version': return (t, p) => t('cluster.tasks.install.reason.offlineVersion', p)
      case 'managers_to_agent': return (t, p) => t('cluster.tasks.install.reason.offlineManagersToAgent', p)
      default: return null
    }
  },
  // How far the server got decides what is left on it: registered, an agent
  // the node page shows; before its install phase, nothing; after, maybe an
  // agent that registered once the old leader was gone, at the row's address,
  // and SFPanel to clean off unless the server had it before.
  AGENT_INSTALL_LEADER_CHANGED: (_d, record) => {
    if (record.node_id) return (t) => t('cluster.tasks.install.reason.leaderChangedRegistered')
    if (!installBegan(record)) return (t) => t('cluster.tasks.install.reason.leaderChangedNotStarted')
    const address = record.ip || record.host
    return (t, p) => {
      const ip = address || t('cluster.tasks.install.detail.theServer')
      return record.sfpanel_before
        ? t('cluster.tasks.install.reason.leaderChangedKept', { ...p, ip })
        : t('cluster.tasks.install.reason.leaderChanged', { ...p, ip })
    }
  },
  AGENT_INSTALL_CREDENTIALS_GONE: (d) => (d.reverted ? (t) => t('cluster.tasks.install.reason.credentialsGoneReverted') : null),
}

// How far a revert went, said after the reason it kept.
function revertedText(d: Details, t: TFunction): string | null {
  switch (d.reverted) {
    case 'removing': return t('cluster.tasks.install.reverted.removing')
    case 'purged': return t('cluster.tasks.install.reverted.purged')
    case 'cleanup_failed': return t('cluster.tasks.install.reverted.cleanupFailed')
    case 'removed': return d.standalone === '1' ? t('cluster.tasks.install.reverted.removedStandalone') : t('cluster.tasks.install.reverted.removed')
    default: return null
  }
}

/**
 * Why an install row ended, in the operator's language: the reason looked up
 * as an error code is (CODE_MESSAGES), its reason_details filling the
 * placeholders — worded first where CODE_DETAIL_FORMATS says how, and
 * CODE_DEFAULTS standing in for one the server left out or sent empty — and
 * how far a revert went after it. A code this screen does not know shows as
 * sent; a row with no reason is null.
 */
export function installReasonText(record: RunNodeRecord | undefined, t: TFunction): string | null {
  const code = record?.reason
  if (!record || !code) return null
  const sent = record.reason_details ?? {}
  let text: string
  if (Object.hasOwn(CODE_MESSAGES, code)) {
    const params: Details = {}
    for (const [name, key] of Object.entries(CODE_DEFAULTS[code] ?? {})) params[name] = t(key)
    const formats = CODE_DETAIL_FORMATS[code] ?? {}
    for (const [name, value] of Object.entries(sent)) {
      if (value === '') continue
      const format = Object.hasOwn(formats, name) ? formats[name] : undefined
      const worded = format ? format(value, t) : value
      if (worded !== null) params[name] = worded
    }
    const variant = Object.hasOwn(VARIANTS, code) ? VARIANTS[code](sent, record) : null
    text = variant ? variant(t, params) : t(CODE_MESSAGES[code], params)
  } else {
    text = code
  }
  const reverted = code === 'AGENT_INSTALL_CREDENTIALS_GONE' ? null : revertedText(sent, t)
  return reverted ? `${text} ${reverted}` : text
}
