import type { AISession, AISessionState, AITool } from '@/types/api'

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
