import { describe, expect, it } from 'vitest'
import type { AISession, AITools } from '@/types/api'
import { aiErrorMessage, defaultTitle, stateDotClass, titlePrefix, toolInstalledFor, toolsFor, waitingCount } from './aiSessions'

const bundle = (account: string, installed: Partial<Record<'claude' | 'codex' | 'gemini', boolean>>): AITools => ({
  tmux: { installed: true, version: '3.6' },
  systemd_run: true,
  accounts: ['root', 'alice'],
  panel_account: 'root',
  account,
  tools: Object.fromEntries((['claude', 'codex', 'gemini'] as const).map((t) => [
    t, { installed: installed[t] ?? false, version: '', path: '', latest: '', update_available: false, logged_in: false },
  ])) as AITools['tools'],
})

const s = (id: string, state: AISession['state']): AISession => ({
  id, tool: 'claude', title: id, run_as: 'root', cwd: '/', state, persistence: 'scope', attached: false, created_at: '',
})

describe('aiSessions helpers', () => {
  it('never pulses the tab the operator is looking at', () => {
    expect(stateDotClass('waiting', false)).toContain('animate-pulse')
    expect(stateDotClass('waiting', true)).not.toContain('animate-pulse')
    expect(stateDotClass('working', false)).toBe('bg-success')
  })

  it('counts waiting sessions other than the active one', () => {
    const list = [s('a', 'waiting'), s('b', 'waiting'), s('c', 'working'), s('d', 'ended')]
    expect(waitingCount(list, 'a')).toBe(1)
    expect(waitingCount(list, null)).toBe(2)
  })

  it('titles a session after its tool and directory', () => {
    expect(defaultTitle('claude', '/opt/stacks/myapp')).toBe('Claude · myapp')
    expect(defaultTitle('shell', '/')).toBe('Shell · /')
    expect(defaultTitle('codex', '/home/alice/')).toBe('Codex · alice')
  })

  it('prefixes the document title only when something waits', () => {
    expect(titlePrefix(0)).toBe('')
    expect(titlePrefix(3)).toBe('(3) ')
  })

  // A /ai/tools response is resolved as one account. Reading root's bundle for
  // alice is how "picking a non-root account greys out the tools it lacks"
  // silently became "greys out nothing".
  it('only lets a bundle answer for the account it was resolved as', () => {
    const root = bundle('root', { claude: true, codex: true })
    const alice = bundle('alice', { claude: true })
    expect(toolsFor('root', root, alice)).toBe(root)
    expect(toolsFor('alice', root, alice)).toBe(alice)
    expect(toolsFor('dave', root, alice)).toBeNull()
    expect(toolsFor('alice', root, null)).toBeNull()
  })

  it('disables a tool the chosen account lacks, and nothing while the answer is missing', () => {
    const alice = bundle('alice', { claude: true })
    expect(toolInstalledFor(alice, 'claude')).toBe(true)
    expect(toolInstalledFor(alice, 'codex')).toBe(false)
    expect(toolInstalledFor(alice, 'shell')).toBe(true)
    // No bundle for that account yet: the server validates on submit, so the
    // radios stay enabled rather than greying out on a guess.
    expect(toolInstalledFor(null, 'codex')).toBe(true)
    expect(toolInstalledFor(null, 'shell')).toBe(true)
  })
})

describe('aiErrorMessage', () => {
  const t = (key: string, opts?: Record<string, unknown>) => (opts ? `${key}:${JSON.stringify(opts)}` : key)
  const err = (code: string | undefined, message: string) => Object.assign(new Error(message), { code })

  it('maps server codes to the ai.errors keys and passes the cwd reason through', () => {
    expect(aiErrorMessage(err('INVALID_PATH', 'cwd: not a directory'), t)).toBe('ai.errors.invalidPath:{"reason":"not a directory"}')
    expect(aiErrorMessage(err('INVALID_ACCOUNT', 'x'), t)).toBe('ai.errors.invalidAccount')
    expect(aiErrorMessage(err('AI_SESSION_LIMIT', 'x'), t)).toBe('ai.errors.limit')
    expect(aiErrorMessage(err('TMUX_MISSING', 'x'), t)).toBe('ai.errors.tmuxMissing')
    expect(aiErrorMessage(err('AI_SESSION_STATE', 'x'), t)).toBe('ai.errors.state')
  })

  it('falls back to the server message, then the generic key', () => {
    expect(aiErrorMessage(err(undefined, 'boom'), t)).toBe('boom')
    expect(aiErrorMessage('not an error', t)).toBe('ai.errors.generic')
    // A plain object with a .message is the case that proves the
    // `instanceof Error` guard: without it, that message would reach the
    // dialog as if the server had sent it.
    expect(aiErrorMessage({ message: 'not from the server' }, t)).toBe('ai.errors.generic')
  })
})
