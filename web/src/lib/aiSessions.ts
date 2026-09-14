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

/** Mirrors the server's default: "<Tool> · <basename of cwd>". */
export function defaultTitle(tool: AITool, cwd: string): string {
  const base = cwd.replace(/\/+$/, '').split('/').pop() || '/'
  return `${TOOL_META[tool].label} · ${base}`
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
 * the closed-to-open reset is the only thing that clears the name.
 */
export function aiPrefill(
  last: AILastSession,
  accounts: string[] | undefined,
  account: string
): { tool?: AITool; cwd?: string; runAs: string } | null {
  const runAs = last.run_as && accounts?.includes(last.run_as) ? last.run_as : account
  if (!runAs) return null
  return {
    tool: last.tool && last.tool in TOOL_META ? last.tool : undefined,
    cwd: last.cwd || undefined,
    runAs,
  }
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
    default:
      return err.message || t('ai.errors.generic')
  }
}
