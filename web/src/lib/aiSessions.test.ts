import { describe, expect, it } from 'vitest'
import type { AISession, AITools } from '@/types/api'
import { LAUNCH_CATALOGUE, PROFILE_TOOLS, aiErrorMessage, aiPrefill, dangerousBlocked, dangerousFlagFor, defaultTitle, formatTimestamp, launchKey, launchModel, launchSummary, loginCommandFor, parseLaunch, profileErrorMessage, relativeSince, sessionInfoLine, stateDotClass, supportsLaunch, supportsProfiles, titlePrefix, toolInstalledFor, toolsFor, untouchedPrefill, validateExtra, validateModel, waitingCount } from './aiSessions'

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

  // Tool and directory only. Mirrors the server's own defaultTitle, which
  // takes no profile either, so the profile has nowhere to enter the
  // generated title; the message spells out why it stays out.
  it('titles a session after its tool and directory, never its profile', () => {
    const why = "the tab's pill is the profile indicator, and a second copy in the title costs width in a strip that truncates around 18 characters and goes stale on a rename the pill survives"
    expect(defaultTitle('claude', '/opt/stacks/myapp'), why).toBe('Claude · myapp')
    expect(defaultTitle('shell', '/'), why).toBe('Shell · /')
    expect(defaultTitle('codex', '/home/alice/'), why).toBe('Codex · alice')
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
    // The server's own sentence for this one is English and names the flag;
    // the key says the same thing in the operator's language, beside the
    // account selector that fixes it.
    expect(aiErrorMessage(err('AI_LAUNCH_ROOT_DANGER', 'launch: claude refuses --dangerously-skip-permissions when it runs as root, and root is root; pick a non-root account'), t)).toBe('ai.errors.launchRoot')
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

  // The name-format refusal is INVALID_BODY carrying the server's English
  // sentence. aiErrorMessage has no case for it on purpose — a body the panel
  // built wrongly is better described by the server — so the create surfaces
  // take profileErrorMessage, which is the only thing that turns it into the
  // rule spelled out in the operator's language.
  it('maps the name-format refusal on the create surfaces only', () => {
    const nameErr = err('INVALID_BODY', 'name must be 1-32 characters of letters, digits, dot, dash or underscore, starting with a letter or digit')
    expect(profileErrorMessage(nameErr, t, 'create')).toBe('ai.profiles.errors.name')
    expect(aiErrorMessage(nameErr, t)).toBe(nameErr.message)
  })

  // DELETE answers INVALID_BODY too, for the default profile, and the name
  // rule would describe that refusal falsely — the name it was given is fine,
  // the profile is simply not the panel's to remove. The surface decides which
  // of the two INVALID_BODY means, because the code alone cannot.
  it('does not read the delete surface INVALID_BODY as the name rule', () => {
    const defaultErr = err('INVALID_BODY', "the default profile is the tool's own directory and is not the panel's to delete")
    expect(profileErrorMessage(defaultErr, t, 'delete')).toBe(defaultErr.message)
    expect(profileErrorMessage(defaultErr, t, 'create')).toBe('ai.profiles.errors.name')
  })

  // Everything else still goes through aiErrorMessage: a create can also come
  // back AI_PROFILE_EXISTS, INVALID_ACCOUNT or INVALID_TOOL, and those keys
  // must not be swallowed by the name message. A delete's own refusals —
  // AI_PROFILE_IN_USE, INVALID_PATH — reach it the same way.
  it('hands every other code to aiErrorMessage', () => {
    expect(profileErrorMessage(err('AI_PROFILE_EXISTS', 'x'), t, 'create')).toBe('ai.profiles.errors.exists')
    expect(profileErrorMessage(err('INVALID_TOOL', 'x'), t, 'create')).toBe('ai.errors.invalidTool')
    expect(profileErrorMessage(err(undefined, 'boom'), t, 'create')).toBe('boom')
    expect(profileErrorMessage('not an error', t, 'create')).toBe('ai.errors.generic')
    expect(profileErrorMessage(err('AI_PROFILE_IN_USE', '2 live session(s) still use this profile'), t, 'delete')).toBe('ai.profiles.errors.inUse')
    expect(profileErrorMessage(err('INVALID_PATH', 'no profile directory of that name'), t, 'delete'))
      .toBe('ai.errors.invalidPath:{"reason":"no profile directory of that name"}')
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

  // "Last used" is a row the host wrote, so a value ahead of the browser's
  // clock is the two disagreeing, not a future use. Unclamped, a profile the
  // operator had just started read "0초 후", and a browser a few minutes
  // behind its host read "in 3 minutes".
  it('never reads as the future when the browser clock is behind the host', () => {
    expect(relativeSince('2026-09-17T12:00:01Z', 'en', now)).toBe('0 seconds ago')
    // Exactly now is the same case: Intl reads the sign, so +0 would still
    // have formatted as "in 0 seconds".
    expect(relativeSince('2026-09-17T12:00:00Z', 'en', now)).toBe('0 seconds ago')
    expect(relativeSince('2026-09-17T12:00:00Z', 'ko', now)).toBe('0초 전')
    expect(relativeSince('2026-09-17T12:03:00Z', 'en', now)).toBe('0 seconds ago')
    expect(relativeSince('2026-09-17T12:03:00Z', 'ko', now)).toBe('0초 전')
    // The past still reads as the past, at the same granularity as before.
    expect(relativeSince('2026-09-17T11:59:59Z', 'en', now)).toBe('1 second ago')
  })
})

describe('sessionInfoLine', () => {
  // The keys pass through untouched so the assertion reads as the layout the
  // operator sees, without pulling i18next into a unit test.
  const tr = (key: string) => key
  const created = '2026-09-14T01:02:03Z'
  const sess = (profile?: string): AISession => ({
    ...s('x', 'working'), run_as: 'alice', cwd: '/opt/stacks/myapp', created_at: created, profile,
  })

  // A tab's 정보 action is the only place that names the profile in full:
  // the pill on the tab truncates, and two sessions on the same tool and
  // directory are otherwise indistinguishable.
  it('names the profile between the directory and the creation time', () => {
    expect(sessionInfoLine(sess('work'), tr)).toBe(
      `ai.tabs.infoAccount: alice · ai.tabs.infoDir: /opt/stacks/myapp · ai.tabs.infoProfile: work · ai.tabs.infoCreated: ${formatTimestamp(created)}`
    )
  })

  // The default profile is the empty string — the tool's own configuration
  // directory. A '프로파일:' label with nothing after it reads as a value the
  // panel failed to load, so the segment is absent instead.
  it('omits the profile for a session on the tool\'s own directory', () => {
    const expected = `ai.tabs.infoAccount: alice · ai.tabs.infoDir: /opt/stacks/myapp · ai.tabs.infoCreated: ${formatTimestamp(created)}`
    expect(sessionInfoLine(sess(undefined), tr)).toBe(expected)
    expect(sessionInfoLine(sess(''), tr)).toBe(expected)
  })

  // The 정보 action is the only place the options a session was started with
  // are spelled out: the tab carries a marker for the bypass flag and for
  // nothing else, and the dialog that chose them is long gone. It shows them
  // in the same words the collapsed section showed, which is why it goes
  // through launchSummary rather than spelling the values out a second time.
  it('lists the launch options between the profile and the creation time', () => {
    const started: AISession = {
      ...sess('work'),
      launch: { continue: 'last', permission: 'acceptEdits', dangerous: true },
    }
    expect(sessionInfoLine(started, tr)).toBe(
      `ai.tabs.infoAccount: alice · ai.tabs.infoDir: /opt/stacks/myapp · ai.tabs.infoProfile: work`
      + ` · ai.tabs.infoLaunch: ai.launch.summaryLast · acceptEdits · ai.launch.dangerous.claude`
      + ` · ai.tabs.infoCreated: ${formatTimestamp(created)}`
    )
  })

  // A session started bare shows nothing new — every row written before the
  // feature is one of those, and a '실행 옵션:' label with nothing after it
  // reads as a value the panel failed to load. The empty object is the same
  // case: the server sends no `launch` for it, but a stored `{}` must not
  // grow a label either.
  it('says nothing about the launch when the tool was started bare', () => {
    const expected = `ai.tabs.infoAccount: alice · ai.tabs.infoDir: /opt/stacks/myapp · ai.tabs.infoCreated: ${formatTimestamp(created)}`
    expect(sessionInfoLine(sess(undefined), tr)).toBe(expected)
    expect(sessionInfoLine({ ...sess(undefined), launch: {} }, tr)).toBe(expected)
  })
})

describe('launch catalogue', () => {
  // The option values are the installed CLIs' own lists, read from their
  // --help (Claude Code 2.1.271, Codex 0.154.0) and mirrored from the
  // server's claudePermissionModes / codexApprovalPolicies /
  // codexSandboxModes. A select offering a mode the CLI does not know would
  // be refused by the server after the operator had already chosen it.
  it('offers each tool the modes its own CLI documents', () => {
    expect(LAUNCH_CATALOGUE.claude.permissions).toEqual(
      ['auto', 'manual', 'plan', 'acceptEdits', 'dontAsk', 'bypassPermissions']
    )
    expect(LAUNCH_CATALOGUE.claude.sandboxes).toBeUndefined()
    expect(LAUNCH_CATALOGUE.claude.dangerousFlag).toBe('--dangerously-skip-permissions')
    expect(LAUNCH_CATALOGUE.codex.permissions).toEqual(['on-request', 'never'])
    expect(LAUNCH_CATALOGUE.codex.sandboxes).toEqual(['read-only', 'workspace-write', 'danger-full-access'])
    expect(LAUNCH_CATALOGUE.codex.dangerousFlag).toBe('--dangerously-bypass-approvals-and-sandbox')
  })

  // Gemini has no launch preference this project has verified and a shell
  // session runs no tool, so the section is not rendered for them — the
  // server refuses any option for those two rather than dropping it.
  it('has a section for claude and codex only', () => {
    expect(supportsLaunch('claude')).toBe(true)
    expect(supportsLaunch('codex')).toBe(true)
    expect(supportsLaunch('gemini')).toBe(false)
    expect(supportsLaunch('shell')).toBe(false)
  })

  // Claude refuses --dangerously-skip-permissions as root ("cannot be used
  // with root/sudo privileges"), so the checkbox is disabled with that
  // reason while the account selector is still on screen. Codex's bypass has
  // no such rule of its own and stays available — the asymmetry is the two
  // CLIs', not the panel's.
  it('blocks the bypass only where the CLI itself refuses it', () => {
    expect(dangerousBlocked('claude', 'root')).toBe(true)
    expect(dangerousBlocked('claude', 'alice')).toBe(false)
    expect(dangerousBlocked('codex', 'root')).toBe(false)
  })

  // The tab marker's tooltip names the flag the session is running under, and
  // it has to be the CLI's own spelling: an operator who wants to know what
  // that tab is doing looks the word up in `claude --help`. '' for the two
  // tools that have no such flag — the server refuses the option for them, so
  // the marker never renders there, but the table stays total over AITool.
  it('names the bypass flag the chosen CLI documents', () => {
    expect(dangerousFlagFor('claude')).toBe('--dangerously-skip-permissions')
    expect(dangerousFlagFor('codex')).toBe('--dangerously-bypass-approvals-and-sandbox')
    expect(dangerousFlagFor('gemini')).toBe('')
    expect(dangerousFlagFor('shell')).toBe('')
  })
})

describe('launchSummary', () => {
  const tr = (key: string) => key

  // The summary is what the collapsed section shows, so nothing is ever
  // applied invisibly. It is also how the dialog knows there is nothing to
  // send: an empty line means an empty option set, which the server stores
  // as '' exactly like every session created before the feature.
  it('says nothing when nothing was chosen', () => {
    expect(launchSummary('claude', undefined, tr)).toBe('')
    expect(launchSummary('claude', {}, tr)).toBe('')
    expect(launchSummary('claude', { dangerous: false, extra: [] }, tr)).toBe('')
  })

  it('names the continue choice before the permission mode', () => {
    expect(launchSummary('claude', { continue: 'last', permission: 'acceptEdits' }, tr))
      .toBe('ai.launch.summaryLast · acceptEdits')
    expect(launchSummary('claude', { continue: 'pick' }, tr)).toBe('ai.launch.summaryPick')
  })

  // One fixed order — continue, permission, sandbox, bypass, model, extra —
  // so two sessions with the same options read identically.
  it('lists every chosen option in one fixed order', () => {
    expect(launchSummary('codex', {
      continue: 'last', permission: 'never', sandbox: 'workspace-write',
      dangerous: true, model: 'gpt-5', extra: ['--profile', 'work'],
    }, tr)).toBe('ai.launch.summaryLast · never · workspace-write · ai.launch.dangerous.codex · gpt-5 · --profile work')
  })

  // The bypass is named per tool because the two CLIs bypass different
  // things: Claude skips permission prompts, Codex drops approvals *and* the
  // sandbox. One shared word would misdescribe one of them.
  it('names the bypass the way the chosen tool bypasses', () => {
    expect(launchSummary('claude', { dangerous: true }, tr)).toBe('ai.launch.dangerous.claude')
    expect(launchSummary('codex', { dangerous: true }, tr)).toBe('ai.launch.dangerous.codex')
  })

  // Without a translator the line degrades to the CLI's own words rather
  // than to raw i18n keys, so a caller that has no `t` at hand still shows
  // something true.
  it('falls back to the CLI\'s own words when there is no translator', () => {
    expect(launchSummary('claude', { continue: 'last', permission: 'plan', dangerous: true }))
      .toBe('last · plan · --dangerously-skip-permissions')
  })

  // ai.launch.dangerous.* exists for the two tools that have a bypass flag,
  // so a translator must not be handed `ai.launch.dangerous.shell` — the tab
  // would show the key itself. The server refuses every option for these two
  // tools, but a row from an edited database reaches the summary.
  it('never renders a raw i18n key for a tool with no bypass flag', () => {
    expect(launchSummary('shell', { dangerous: true }, tr)).toBe('dangerous')
    expect(launchSummary('gemini', { dangerous: true }, tr)).toBe('dangerous')
  })
})

describe('validateModel', () => {
  // The field is one CLI token. The pattern is the server's launchModelRe, so
  // a name the dialog accepts is never refused after 만들기 — and the ones it
  // refuses are refused in the operator's own language beside the field.
  it('accepts an empty field and the shapes both CLIs name their models', () => {
    expect(validateModel('')).toBe(true)
    expect(validateModel('opus-5')).toBe(true)
    expect(validateModel('claude-opus-4.5')).toBe(true)
    expect(validateModel('gpt-5.1-codex_max')).toBe(true)
    expect(validateModel('o'.repeat(64))).toBe(true)
  })

  // The two shapes an operator actually pastes, plus the space that reaches
  // this rule now that the field no longer closes it up (see launchModel).
  it('refuses a vendor prefix, a dated tag, a space and 65 characters', () => {
    expect(validateModel('openai/gpt-5')).toBe(false)
    expect(validateModel('model@2025-09')).toBe(false)
    expect(validateModel('opus 5')).toBe(false)
    expect(validateModel('-opus')).toBe(false)
    expect(validateModel('o'.repeat(65))).toBe(false)
  })
})

describe('launchModel', () => {
  // The defect this replaces: the field trimmed on every keystroke, so a
  // typed `gpt 5` became `gpt5` — a name this rule and the server's both
  // accept. The operator watched a character vanish under the cursor and the
  // session got a model they never asked for, where the same input used to
  // draw a refusal. The check runs on the string the field is holding, so an
  // inner space is one of the unusable characters modelError names.
  it('refuses a space inside the name instead of closing it up', () => {
    expect(launchModel('gpt 5')).toBeNull()
    expect(launchModel('gpt 5')).not.toBe('gpt5')
    expect(launchModel('openai/gpt-5')).toBeNull()
  })

  // The edges are the exception and the only one: they are what a pasted
  // name carries, ` gpt-5 ` names the same model as `gpt-5`, and "쓸 수 없는
  // 문자" would be a false thing to say about that paste. Dropped once, where
  // the body is built.
  it('drops the whitespace a pasted name carries and posts the name', () => {
    expect(launchModel(' gpt-5 ')).toBe('gpt-5')
    expect(launchModel('\tclaude-opus-4.5\n')).toBe('claude-opus-4.5')
  })

  // An empty field is the tool's own default, which is what the placeholder
  // says — not a refusal. Whitespace alone is the same thing typed.
  it('reads an empty or blank field as the tool default', () => {
    expect(launchModel('')).toBe('')
    expect(launchModel('   ')).toBe('')
  })
})

describe('parseLaunch', () => {
  it('reads back a stored option set', () => {
    expect(parseLaunch('{"continue":"last","permission":"plan"}')).toEqual({ continue: 'last', permission: 'plan' })
    expect(parseLaunch(null)).toEqual({})
    expect(parseLaunch('')).toEqual({})
  })

  // JSON.parse('null') is a *value*, not a failure, so a stored literal
  // "null" used to come back as null — and the dialog reads `.dangerous` off
  // this the very next line, which throws inside an effect and leaves the
  // dialog blank. Forgetting a remembered preference is the only acceptable
  // cost of a value someone else wrote.
  it('answers an empty option set for anything that is not one', () => {
    expect(parseLaunch('null')).toEqual({})
    expect(parseLaunch('[1,2]')).toEqual({})
    expect(parseLaunch('"last"')).toEqual({})
    expect(parseLaunch('7')).toEqual({})
    expect(parseLaunch('{oops')).toEqual({})
  })
})

describe('validateExtra', () => {
  // The field is one line of space-separated tokens, each of which becomes
  // its own argv element. The limits mirror the server's (8 tokens, 64
  // characters, the same character set), so a value the dialog accepts is
  // never refused after the operator pressed 만들기.
  it('accepts a blank field and up to eight tokens', () => {
    expect(validateExtra('')).toEqual({ tokens: [] })
    expect(validateExtra('   ')).toEqual({ tokens: [] })
    expect(validateExtra('--verbose  --model=x')).toEqual({ tokens: ['--verbose', '--model=x'] })
    const eight = ['a1', 'b2', 'c3', 'd4', 'e5', 'f6', 'g7', 'h8']
    expect(validateExtra(eight.join(' '))).toEqual({ tokens: eight })
  })

  it('refuses a ninth token, naming the count rule', () => {
    expect(validateExtra('a1 b2 c3 d4 e5 f6 g7 h8 i9')).toEqual({ error: 'count' })
  })

  // Quotes are the only way an operator can write a token containing a
  // space, and the field cannot express one: each token is one argv element
  // and no shell re-parses it. So the refusal says *that* rather than
  // blaming the quote character — the quote fails the character rule too,
  // and "an argument may not contain \" " would send the operator looking
  // for the wrong mistake.
  it('refuses a value that needs a space, naming the space rule and not the character one', () => {
    expect(validateExtra('--append "hello world"')).toEqual({ error: 'space' })
    expect(validateExtra("--append 'hello world'")).toEqual({ error: 'space' })
  })

  it('refuses a character an argument may not carry', () => {
    expect(validateExtra('--x;y')).toEqual({ error: 'char' })
    expect(validateExtra('--ok --and=$(id)')).toEqual({ error: 'char' })
    expect(validateExtra('---three-dashes')).toEqual({ error: 'char' })
    // A backtick is a shell metacharacter rather than a way of writing a
    // space, so it is refused as a character and not as the quoting rule.
    expect(validateExtra('--and=`id`')).toEqual({ error: 'char' })
  })

  it('refuses a token past 64 characters, naming the length rule', () => {
    expect(validateExtra('a'.repeat(64))).toEqual({ tokens: ['a'.repeat(64)] })
    expect(validateExtra('a'.repeat(65))).toEqual({ error: 'length' })
  })
})

describe('launchKey', () => {
  // Per node, tool and directory: the same folder opened with the same tool
  // starts the way it did last time, and opening it as another tool — or the
  // same tool on another node — does not inherit those options.
  it('is scoped to the node, the tool and the directory', () => {
    expect(launchKey('local', 'claude', '/opt/stacks/myapp')).toBe('sfpanel_ai_launch:local:claude:/opt/stacks/myapp')
    expect(launchKey('node-b', 'claude', '/opt/stacks/myapp')).not.toBe(launchKey('local', 'claude', '/opt/stacks/myapp'))
    expect(launchKey('local', 'codex', '/opt/stacks/myapp')).not.toBe(launchKey('local', 'claude', '/opt/stacks/myapp'))
    expect(launchKey('local', 'claude', '/srv')).not.toBe(launchKey('local', 'claude', '/opt/stacks/myapp'))
  })
})
