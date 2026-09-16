import { describe, expect, it } from 'vitest'
import type { AISession, AITools } from '@/types/api'
import { PROFILE_TOOLS, aiErrorMessage, aiPrefill, defaultTitle, formatTimestamp, loginCommandFor, relativeSince, stateDotClass, supportsProfiles, titlePrefix, toolInstalledFor, toolsFor, untouchedPrefill, waitingCount } from './aiSessions'

const bundle = (account: string, installed: Partial<Record<'claude' | 'codex' | 'gemini', boolean>>): AITools => ({
  tmux: { installed: true, version: '3.6', supported: true, min_version: '3.2' },
  systemd_run: true,
  accounts: ['root', 'alice'],
  panel_account: 'root',
  account,
  tools: Object.fromEntries((['claude', 'codex', 'gemini'] as const).map((t) => [
    t, { installed: installed[t] ?? false, version: '', path: '', latest: '', update_available: false, logged_in: false },
  ])) as AITools['tools'],
})

const s = (id: string, state: AISession['state']): AISession => ({
  id, tool: 'claude', title: id, run_as: 'root', cwd: '/', state, persistence: 'service', attached: false, created_at: '',
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

  // Mirrors the server's own defaultTitle: two tabs on the same tool and
  // directory are otherwise identical while running as different logins.
  it('names the profile in the title, and nothing for the default one', () => {
    expect(defaultTitle('codex', '/opt/stacks/myapp', 'work')).toBe('Codex(work) · myapp')
    expect(defaultTitle('claude', '/opt/stacks/myapp', 'client-a')).toBe('Claude(client-a) · myapp')
    // The default profile is the empty string and adds nothing — not '()'.
    expect(defaultTitle('codex', '/opt/stacks/myapp', '')).toBe('Codex · myapp')
    expect(defaultTitle('codex', '/opt/stacks/myapp')).toBe('Codex · myapp')
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
    // The profile codes carry a server message the operator cannot act on
    // ("a profile of that name already exists"), so they get their own keys.
    expect(aiErrorMessage(err('AI_PROFILE_EXISTS', 'a profile of that name already exists'), t)).toBe('ai.profiles.errors.exists')
    expect(aiErrorMessage(err('AI_PROFILE_IN_USE', '2 live session(s) still use this profile'), t)).toBe('ai.profiles.errors.inUse')
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

describe('aiPrefill', () => {
  // The reachable sequence: the operator presses + before GET /ai/tools has
  // answered, so `account` is still ''. Prefilling that emptied the account
  // select and short-circuited the dirs/tools effect on !runAs, and the
  // once-only flag meant the dialog never recovered.
  it('defers while the account is unresolved, then prefills when it arrives', () => {
    expect(aiPrefill({}, undefined, '')).toBeNull()
    expect(aiPrefill({}, ['root'], 'root')).toEqual({ tool: undefined, cwd: undefined, runAs: 'root' })
  })

  // Deferring means the prefill can run a second time, so it must not carry
  // anything the operator may have touched in between. The name is the one
  // such field, and it is cleared only by the closed -> open reset.
  it('never produces a title', () => {
    const pre = aiPrefill({ tool: 'codex', cwd: '/srv', run_as: 'alice' }, ['root', 'alice'], 'root')
    expect(pre).not.toBeNull()
    expect('title' in pre!).toBe(false)
    expect(Object.keys(pre!).sort()).toEqual(['cwd', 'runAs', 'tool'])
  })

  it('uses the remembered account only while it is still on the allowlist', () => {
    expect(aiPrefill({ run_as: 'alice' }, ['root', 'alice'], 'root')?.runAs).toBe('alice')
    expect(aiPrefill({ run_as: 'alice' }, ['root'], 'root')?.runAs).toBe('root')
    expect(aiPrefill({ run_as: 'alice' }, undefined, 'root')?.runAs).toBe('root')
  })

  it('drops a remembered tool that is not one of ours', () => {
    expect(aiPrefill({ tool: 'vim' as never }, ['root'], 'root')?.tool).toBeUndefined()
    expect(aiPrefill({ tool: 'gemini' }, ['root'], 'root')?.tool).toBe('gemini')
  })
})

describe('untouchedPrefill', () => {
  // The reachable sequence: the operator presses + before GET /ai/tools has
  // answered, picks Codex and types a directory while they wait, and the
  // prefill then runs a second time on the render that brings the account.
  // What it remembers from the last session must not take their choices back.
  it('applies only the fields the operator has not set', () => {
    const pre = aiPrefill({ tool: 'gemini', cwd: '/srv/old', run_as: 'alice' }, ['root', 'alice'], 'root')!
    expect(untouchedPrefill(pre, { tool: true, cwd: true })).toEqual({ tool: undefined, cwd: undefined, runAs: 'alice' })
    expect(untouchedPrefill(pre, { tool: true, cwd: false })).toEqual({ tool: undefined, cwd: '/srv/old', runAs: 'alice' })
    expect(untouchedPrefill(pre, { tool: false, cwd: true })).toEqual({ tool: 'gemini', cwd: undefined, runAs: 'alice' })
  })

  // Nothing touched is the first run, which is the whole point of remembering.
  it('applies everything on an untouched dialog', () => {
    const pre = aiPrefill({ tool: 'codex', cwd: '/srv', run_as: 'root' }, ['root'], 'root')!
    expect(untouchedPrefill(pre, { tool: false, cwd: false })).toEqual(pre)
  })

  // runAs is never withheld: its absence is what deferred the prefill, and
  // until it arrives the account select has nothing else to show.
  it('always applies the account', () => {
    const pre = aiPrefill({}, ['root'], 'root')!
    expect(untouchedPrefill(pre, { tool: true, cwd: true }).runAs).toBe('root')
  })
})

describe('formatTimestamp', () => {
  it('renders a stored timestamp in the local locale, not as raw ISO', () => {
    const got = formatTimestamp('2026-09-14T01:02:03Z')
    expect(got).toBe(new Date('2026-09-14T01:02:03Z').toLocaleString())
    expect(got).not.toContain('T')
  })

  // A value that will not parse is shown as it came: "Invalid Date" tells the
  // operator nothing, the raw string at least tells them what was stored.
  it('falls back to the raw value when it will not parse', () => {
    expect(formatTimestamp('not a date')).toBe('not a date')
    expect(formatTimestamp('')).toBe('')
  })
})

describe('profile support', () => {
  // Only the two tools with a verified config-directory variable
  // (CLAUDE_CONFIG_DIR, CODEX_HOME) can carry a profile, so the picker is
  // rendered for those two and for nothing else: Gemini has no override the
  // project has verified and a shell session runs no tool. A picker that
  // cannot work must never reach the dialog — the route answers
  // INVALID_TOOL for the rest.
  it('supports profiles for claude and codex only', () => {
    expect(PROFILE_TOOLS).toEqual(['claude', 'codex'])
    expect(supportsProfiles('claude')).toBe(true)
    expect(supportsProfiles('codex')).toBe(true)
    expect(supportsProfiles('gemini')).toBe(false)
    expect(supportsProfiles('shell')).toBe(false)
  })

  // The hint tells the operator what to type; the two CLIs do not agree on
  // it, and telling a Codex operator to run '/login' in their shell is worse
  // than saying nothing.
  it('names the login command each CLI actually uses', () => {
    expect(loginCommandFor('codex')).toBe('codex login')
    expect(loginCommandFor('claude')).toBe('/login')
    // No profile, so no hint is ever rendered for these.
    expect(loginCommandFor('gemini')).toBe('')
    expect(loginCommandFor('shell')).toBe('')
  })
})

describe('relativeSince', () => {
  const now = Date.parse('2026-09-17T12:00:00Z')

  it('says how long ago a profile was last used, in the operator\'s language', () => {
    expect(relativeSince('2026-09-17T11:59:10Z', 'en', now)).toBe('50 seconds ago')
    expect(relativeSince('2026-09-17T11:30:00Z', 'en', now)).toBe('30 minutes ago')
    expect(relativeSince('2026-09-16T12:00:00Z', 'en', now)).toBe('1 day ago')
    expect(relativeSince('2026-09-16T12:00:00Z', 'ko', now)).toBe('1일 전')
  })

  // A profile that has never been started carries last_used_at: '' (the
  // server omits it), and the picker must show no hint at all rather than
  // "56 years ago" from a zero date.
  it('says nothing for a value that is missing or will not parse', () => {
    expect(relativeSince('', 'en', now)).toBe('')
    expect(relativeSince('not a date', 'en', now)).toBe('')
  })
})
