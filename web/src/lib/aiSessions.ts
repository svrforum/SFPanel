import type { AISession, AISessionState, AITool, AITools } from '@/types/api'

/** Brand glyphs, the same colours the packages page used for these CLIs. */
export const TOOL_META: Record<AITool, { label: string; initial: string; color: string }> = {
  claude: { label: 'Claude', initial: 'C', color: '#d97757' },
  codex: { label: 'Codex', initial: 'X', color: '#10a37f' },
  gemini: { label: 'Gemini', initial: 'G', color: '#4285f4' },
  shell: { label: 'Shell', initial: 'S', color: '#6b7280' },
}

/**
 * Status dot per state. The active tab never pulses: the operator is looking
 * at it, so "waiting" there is just "idle at the prompt".
 */
export function stateDotClass(state: AISessionState, active: boolean): string {
  switch (state) {
    case 'working':
      return 'bg-success'
    case 'waiting':
      return active ? 'bg-success' : 'bg-warning animate-pulse'
    case 'shell':
      return 'bg-muted-foreground/60'
    case 'ended':
      return 'bg-transparent ring-1 ring-muted-foreground/50'
  }
}

/** Sessions that need the operator and are not the one on screen. */
export function waitingCount(sessions: AISession[], activeId: string | null): number {
  return sessions.filter((s) => s.state === 'waiting' && s.id !== activeId).length
}

/**
 * The tools that can run under a profile: the two whose configuration
 * directory the project has verified an override for (CODEX_HOME,
 * CLAUDE_CONFIG_DIR). Gemini has no such override and a shell session runs
 * no tool, so the picker is not rendered for them — GET /ai/profiles answers
 * INVALID_TOOL rather than an empty list, and a picker that cannot work is
 * worse than none.
 */
export const PROFILE_TOOLS: AITool[] = ['claude', 'codex']

export function supportsProfiles(tool: AITool): boolean {
  return PROFILE_TOOLS.includes(tool)
}

/**
 * What the operator types to log a fresh profile in. The two CLIs disagree:
 * Codex is logged in from the shell, Claude from its own prompt. Empty for a
 * tool without profiles, whose hint is never rendered.
 */
export function loginCommandFor(tool: AITool): string {
  switch (tool) {
    case 'codex':
      return 'codex login'
    case 'claude':
      return '/login'
    default:
      return ''
  }
}

/**
 * Mirrors the server's default: "<Tool>(<profile>) · <basename of cwd>". The
 * profile belongs in the title because two tabs on the same tool and
 * directory are otherwise identical while running as different logins; the
 * default profile is the empty string and adds nothing.
 */
export function defaultTitle(tool: AITool, cwd: string, profile?: string): string {
  const base = cwd.replace(/\/+$/, '').split('/').pop() || '/'
  const name = profile ? `${TOOL_META[tool].label}(${profile})` : TOOL_META[tool].label
  return `${name} · ${base}`
}

/**
 * Coarse-to-fine units for relativeSince. The last entry ends the walk, so
 * its divisor is 0.
 */
const RELATIVE_UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['second', 60], ['minute', 60], ['hour', 24], ['day', 30], ['month', 12], ['year', 0],
]

/**
 * "30분 전" / "30 minutes ago" for a profile's last use, in the operator's
 * own language. A value that will not parse — including the '' the server
 * sends for a profile no session has ever run on — produces nothing, because
 * a zero date would otherwise read as "56 years ago".
 */
export function relativeSince(value: string, lang: string, now = Date.now()): string {
  const ms = new Date(value).getTime()
  if (Number.isNaN(ms)) return ''
  let n = Math.round((ms - now) / 1000)
  for (const [unit, per] of RELATIVE_UNITS) {
    if (per === 0 || Math.abs(n) < per) {
      return new Intl.RelativeTimeFormat(lang, { numeric: 'always' }).format(n, unit)
    }
    n = Math.round(n / per)
  }
  return ''
}

/**
 * The bundle that answers for `account`, or null when none of the ones at hand
 * does. A `/ai/tools` response is resolved *as one account*, so the page's
 * bundle says nothing about the account the dialog switched to — a tool root
 * has and alice lacks is the whole reason the selector exists.
 */
export function toolsFor(account: string, ...bundles: (AITools | null | undefined)[]): AITools | null {
  return bundles.find((b) => b?.account === account) ?? null
}

/**
 * Whether `tool` can be started as the account the bundle describes. Unknown
 * (no bundle yet, or a tool the response does not carry) stays enabled: the
 * server validates on submit, and greying a radio out on a guess is worse than
 * an inline error. A shell is always available.
 */
export function toolInstalledFor(bundle: AITools | null, tool: AITool): boolean {
  if (tool === 'shell') return true
  if (!bundle) return true
  return bundle.tools[tool]?.installed ?? true
}

/** What the new-session dialog remembers per node in localStorage. */
export interface AILastSession {
  tool?: AITool
  cwd?: string
  run_as?: string
}

/** The fields aiPrefill hands the dialog to open with. */
export interface AIPrefill {
  tool?: AITool
  cwd?: string
  runAs: string
}

/**
 * The dialog's opening values, or null while there is nothing to open with.
 *
 * `account` is '' until GET /ai/tools resolves, and the dialog can be opened
 * before that. Prefilling '' left the account select empty and short-circuited
 * the effect that loads directories and per-account tool status, so the dialog
 * stayed stuck; null tells the caller to leave its "already prefilled" flag
 * down and try again on the render that brings a real account.
 *
 * There is deliberately no title here. The prefill may run a second time, and
 * a name the operator typed while waiting for the account must survive it —
 * the closed-to-open reset is the only thing that clears the name. The other
 * two fields are protected by untouchedPrefill below rather than structurally,
 * because the remembered values are worth applying when nobody has.
 */
export function aiPrefill(
  last: AILastSession,
  accounts: string[] | undefined,
  account: string
): AIPrefill | null {
  const runAs = last.run_as && accounts?.includes(last.run_as) ? last.run_as : account
  if (!runAs) return null
  return {
    tool: last.tool && last.tool in TOOL_META ? last.tool : undefined,
    cwd: last.cwd || undefined,
    runAs,
  }
}

/** Which of the prefillable fields the operator has set by hand. */
export interface AITouched {
  tool: boolean
  cwd: boolean
}

/**
 * The prefill narrowed to the fields nobody has touched.
 *
 * aiPrefill defers while `account` is '', so it runs again on the render that
 * resolves the account — and by then the operator may already have picked a
 * tool or typed a directory. Re-applying the remembered ones took that back.
 * `runAs` is always applied: its absence is what deferred the prefill, and
 * until it arrives the account select has nothing else to offer.
 */
export function untouchedPrefill(pre: AIPrefill, touched: AITouched): AIPrefill {
  return {
    tool: touched.tool ? undefined : pre.tool,
    cwd: touched.cwd ? undefined : pre.cwd,
    runAs: pre.runAs,
  }
}

/**
 * A session timestamp in the operator's own locale, the way the audit table
 * renders one. A value that will not parse is shown as it came: a raw ISO
 * string is a poor label, but "Invalid Date" is a worse one.
 */
export function formatTimestamp(value: string): string {
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString()
}

/** document.title prefix while the page is open. */
export function titlePrefix(n: number): string {
  return n > 0 ? `(${n}) ` : ''
}

type Translate = (key: string, opts?: Record<string, unknown>) => string

/** Turns an api.request failure into the inline message the dialog shows. */
export function aiErrorMessage(err: unknown, t: Translate): string {
  if (!(err instanceof Error)) return t('ai.errors.generic')
  const code = (err as Error & { code?: string }).code
  switch (code) {
    case 'INVALID_PATH':
      return t('ai.errors.invalidPath', { reason: err.message.replace(/^cwd:\s*/, '') })
    case 'INVALID_ACCOUNT':
      return t('ai.errors.invalidAccount')
    case 'INVALID_TOOL':
      return t('ai.errors.invalidTool')
    case 'AI_SESSION_LIMIT':
      return t('ai.errors.limit')
    case 'TMUX_MISSING':
      return t('ai.errors.tmuxMissing')
    case 'AI_SESSION_STATE':
      return t('ai.errors.state')
    case 'AI_PROFILE_EXISTS':
      return t('ai.profiles.errors.exists')
    case 'AI_PROFILE_IN_USE':
      return t('ai.profiles.errors.inUse')
    default:
      return err.message || t('ai.errors.generic')
  }
}
