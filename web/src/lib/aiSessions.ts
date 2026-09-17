import type { AILaunchOptions, AISession, AISessionState, AITool, AITools } from '@/types/api'

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
 * The launch options each CLI offers, per tool. These are the installed
 * CLIs' own lists, read from their `--help` (Claude Code 2.1.271, Codex
 * 0.154.0) and mirrored from the server's claudePermissionModes /
 * codexApprovalPolicies / codexSandboxModes: the dialog must never offer a
 * mode the CLI does not know, because the server refuses it *after* the
 * operator chose it.
 *
 * It is a static table rather than a field of GET /ai/tools on purpose — the
 * catalogue does not depend on the host, and a round trip in front of the
 * dialog would be one the operator waits on.
 *
 * `permissions` is Claude's --permission-mode and Codex's
 * --ask-for-approval: they are not two spellings of one idea, which is why
 * switching the tool drops the choice instead of translating it.
 */
export const LAUNCH_CATALOGUE: Record<'claude' | 'codex', {
  permissions: string[]
  /** Codex only; Claude has no sandbox flag. */
  sandboxes?: string[]
  dangerousFlag: string
}> = {
  claude: {
    permissions: ['auto', 'manual', 'plan', 'acceptEdits', 'dontAsk', 'bypassPermissions'],
    dangerousFlag: '--dangerously-skip-permissions',
  },
  codex: {
    permissions: ['on-request', 'never'],
    sandboxes: ['read-only', 'workspace-write', 'danger-full-access'],
    dangerousFlag: '--dangerously-bypass-approvals-and-sandbox',
  },
}

/**
 * Whether the 실행 옵션 section is rendered at all. Kept separate from
 * supportsProfiles even though the two lists agree today: a profile needs a
 * verified config-directory variable, a launch option needs a verified flag,
 * and Gemini could gain one without the other. The server's
 * toolSupportsLaunch is the same split, and it refuses any option for the
 * other two rather than dropping it.
 */
export function supportsLaunch(tool: AITool): tool is 'claude' | 'codex' {
  return tool in LAUNCH_CATALOGUE
}

/**
 * Whether the tool's bypass flag is refused for this account. Claude's own
 * check is the reason: `--dangerously-skip-permissions cannot be used with
 * root/sudo privileges`. The dialog disables the checkbox and says so while
 * the account selector is still on screen, instead of letting the CLI die
 * with it after the session opens.
 *
 * Codex's bypass has no such rule of its own, so it stays available — the
 * asymmetry belongs to the two CLIs, not to the panel.
 *
 * The name, not the uid: the browser has no uid. The server checks uid 0, so
 * an account named otherwise that *is* root is refused there and
 * AI_LAUNCH_ROOT_DANGER carries the reason back.
 */
export function dangerousBlocked(tool: AITool, runAs: string): boolean {
  return tool === 'claude' && runAs === 'root'
}

/**
 * The bypass flag as the chosen CLI spells it. Two places put it in front of
 * the operator — the tab marker's tooltip and the launchSummary line that has
 * no translator — and both want the CLI's own word, because that is what an
 * operator checking what a tab is doing will find in `claude --help`.
 *
 * '' for the two tools that have no such flag. The marker never renders for
 * them (the server refuses every option for gemini and shell), but the lookup
 * stays total over AITool rather than asserting a narrower one.
 */
export function dangerousFlagFor(tool: AITool): string {
  return supportsLaunch(tool) ? LAUNCH_CATALOGUE[tool].dangerousFlag : ''
}

/** Between the summary's segments — the separator the session info line uses. */
const LAUNCH_SEP = ' · '

/**
 * The one line the collapsed 실행 옵션 section shows — "이어서 · acceptEdits"
 * — so a remembered option is never applied invisibly.
 *
 * Its emptiness is load-bearing in two places: the section renders no summary
 * when nothing is chosen, and the dialog sends no `launch` at all, which is
 * how a session created without touching the section stays byte-for-byte the
 * one an older panel would have created.
 *
 * The permission, sandbox and model values are shown as the CLI spells them:
 * an operator matching the panel against `claude --help` needs the CLI's own
 * word, and a translated "편집 허용" would not be findable there. The two
 * choices that are *not* CLI values — the continue choice and the bypass —
 * are translated, and the bypass per tool, because Claude skips permission
 * prompts while Codex drops approvals *and* the sandbox.
 *
 * Without `t` the line degrades to the CLI's own words rather than to raw
 * i18n keys, so a caller with no translator at hand still shows something
 * true.
 */
export function launchSummary(tool: AITool, o?: AILaunchOptions, t?: Translate): string {
  if (!o) return ''
  const parts: string[] = []
  if (o.continue === 'last') parts.push(t ? t('ai.launch.summaryLast') : 'last')
  else if (o.continue === 'pick') parts.push(t ? t('ai.launch.summaryPick') : 'pick')
  if (o.permission) parts.push(o.permission)
  if (o.sandbox) parts.push(o.sandbox)
  if (o.dangerous) {
    // supportsLaunch, not just `t`: ai.launch.dangerous.* exists for the two
    // tools that have a bypass flag, so translating for gemini or shell would
    // put the raw key `ai.launch.dangerous.shell` on screen. The server
    // refuses every option for those two, but a row from an edited database
    // reaches this line, and a raw key is the one output worse than the flag.
    parts.push(t && supportsLaunch(tool) ? t(`ai.launch.dangerous.${tool}`) : dangerousFlagFor(tool) || 'dangerous')
  }
  if (o.model) parts.push(o.model)
  if (o.extra?.length) parts.push(o.extra.join(' '))
  return parts.join(LAUNCH_SEP)
}

/** Why a 추가 인자 line was refused; each value is one i18n key and one rule. */
export type LaunchExtraError = 'space' | 'char' | 'count' | 'length'

/** The server's own limits on Extra, so a line the dialog accepts is accepted there too. */
const MAX_EXTRA = 8
const MAX_EXTRA_LEN = 64

/**
 * The 모델 field, checked before anything is posted. A twin of the server's
 * launchModelRe — letters, digits, dot, dash, underscore and colon, 1 to 64
 * characters, starting alphanumeric — for the same reason validateExtra
 * exists: without it the only refusal is the server's, and that one arrives
 * in English beside a Korean field.
 *
 * The two shapes this actually catches are the ones an operator pastes:
 * `openai/gpt-5` and `model@2025-09`. Neither `/` nor `@` is in the set, and
 * neither CLI takes a model name with one.
 *
 * Empty is valid — it means the tool's own default, which is what the field's
 * placeholder says.
 *
 * This is the rule on a finished name. What the *field* holds is not one yet,
 * so the dialog calls launchModel below rather than this directly.
 */
export function validateModel(model: string): boolean {
  return model === '' || /^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$/.test(model)
}

/**
 * The 모델 field as typed, turned into the name that gets posted — or null,
 * meaning the field is showing something that is not a model name.
 *
 * The field keeps what the operator typed, and the check runs on that. It
 * used to be trimmed on every keystroke instead, which turned a typed
 * `gpt 5` into `gpt5`: a name this rule and the server's both accept, so the
 * operator watched a character vanish under the cursor and got a model they
 * had not asked for, where the same input used to draw a refusal. Whitespace
 * *inside* a name is therefore one of the unusable characters
 * ai.launch.modelError names.
 *
 * The edges are the exception, and the only one: they are what a name pasted
 * out of a document carries, ` gpt-5 ` and `gpt-5` are the same model, and
 * "쓸 수 없는 문자" would be a false thing to say about that paste. So they
 * are dropped once, here, where the request body is built — not under the
 * cursor.
 *
 * `''` (the whole field, or all whitespace) is not an error: it means the
 * tool's own default. A boolean-or-value rather than validateExtra's tagged
 * union because there is one rule here, not four, so there is no *which* to
 * carry back.
 */
export function launchModel(raw: string): string | null {
  const model = raw.trim()
  return validateModel(model) ? model : null
}

/**
 * The remembered option set for one folder, from whatever the browser has
 * stored under its key.
 *
 * Anything that is not a JSON object is "nothing chosen". `null` is the case
 * that matters: `JSON.parse('null')` is a value, not a parse failure, so a
 * stored literal "null" — an older panel, another tab, a hand-edited entry —
 * came back as null and the caller's very next line read `.dangerous` off it,
 * which throws inside the effect and leaves the dialog blank instead of
 * simply forgetting a preference. An array is rejected for the same reason:
 * spreading one produces index keys, not options.
 */
export function parseLaunch(raw: string | null): AILaunchOptions {
  if (!raw) return {}
  try {
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return {}
    return parsed as AILaunchOptions
  } catch {
    return {}
  }
}

/**
 * The advanced 추가 인자 field, split and checked before anything is posted.
 * Mirrors the server's validateLaunch — 8 tokens, 64 characters each, the
 * same character set — so the dialog refuses inline what the server would
 * have refused after the operator pressed 만들기.
 *
 * Each token becomes one argv element in `bash -lic '… "$@"' <tool> <argv…>`
 * and no shell re-parses it, so this pattern is belt to that brace rather
 * than the only defence. What it buys is a *named* reason next to the field.
 *
 * The quote check comes first and deliberately answers 'space' rather than
 * 'char': a single or double quote is the only way an operator can write a
 * token containing a space, and the honest refusal is "a value with a space cannot be
 * expressed" — not "an argument may not contain a double quote", which would
 * send them looking for the wrong mistake. Splitting on whitespace is what
 * makes that ordering necessary: after the split no token can hold a space,
 * so without this check the quoted form would only ever fail as a character.
 * A backtick is not in that class — it is a shell metacharacter, not a way
 * to write a space — so it stays with the character rule.
 */
export function validateExtra(raw: string): { tokens: string[] } | { error: LaunchExtraError } {
  const tokens = raw.trim().split(/\s+/).filter((tok) => tok !== '')
  if (tokens.length === 0) return { tokens: [] }
  if (/["']/.test(raw)) return { error: 'space' }
  if (tokens.length > MAX_EXTRA) return { error: 'count' }
  for (const tok of tokens) {
    if (tok.length > MAX_EXTRA_LEN) return { error: 'length' }
    if (!/^-{0,2}[A-Za-z0-9][A-Za-z0-9=._,:/@-]*$/.test(tok)) return { error: 'char' }
  }
  return { tokens }
}

/**
 * Where the dialog remembers the options a folder was last run with, so
 * reopening it with the same tool starts it the same way (spec §6). Per node
 * as well as per tool and directory: /opt/stacks/myapp on another node is
 * another machine's folder, and inheriting its options would start a session
 * nobody asked for.
 */
export function launchKey(node: string, tool: AITool, cwd: string): string {
  return `sfpanel_ai_launch:${node}:${tool}:${cwd}`
}

/**
 * Mirrors the server's default: "<Tool> · <basename of cwd>". The profile is
 * deliberately absent — the tab renders it as its own pill, so a second copy
 * in the title costs width in a strip that truncates around 18 characters and
 * goes stale on a rename the pill survives.
 */
export function defaultTitle(tool: AITool, cwd: string): string {
  const base = cwd.replace(/\/+$/, '').split('/').pop() || '/'
  return `${TOOL_META[tool].label} · ${base}`
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
 *
 * Clamped to the past. "Last used" is a row the host wrote, so a value ahead
 * of Date.now() means the two clocks disagree, not that a profile will be
 * used later: a profile used a moment ago read "0초 후", and a browser a few
 * minutes behind its host read "in 3 minutes". -0 rather than 0, and >= not
 * >, because Intl reads the *sign* of the value: +0 formats as "in 0
 * seconds", which is the same defect for a profile used this very second.
 */
export function relativeSince(value: string, lang: string, now = Date.now()): string {
  const ms = new Date(value).getTime()
  if (Number.isNaN(ms)) return ''
  const delta = Math.round((ms - now) / 1000)
  let n = delta >= 0 ? -0 : delta
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

/**
 * The one line a tab's 정보 action toasts. The profile is named only when the
 * session runs on one: the default profile is the empty string — the tool's
 * own configuration directory — and a label with nothing after it reads as a
 * value the panel failed to load. It sits next to the directory because the
 * two together are what distinguishes otherwise identical tabs.
 *
 * The launch options follow it, in the same words the dialog's collapsed
 * summary used — this is the only place they can be read back, since the tab
 * marks the bypass flag and nothing else. An option set that is absent or
 * empty adds no segment at all, so a session started bare (which is every row
 * written before the feature) reads exactly as it did.
 */
export function sessionInfoLine(s: AISession, t: Translate): string {
  const parts = [
    `${t('ai.tabs.infoAccount')}: ${s.run_as}`,
    `${t('ai.tabs.infoDir')}: ${s.cwd}`,
  ]
  if (s.profile) parts.push(`${t('ai.tabs.infoProfile')}: ${s.profile}`)
  const launch = launchSummary(s.tool, s.launch, t)
  if (launch) parts.push(`${t('ai.tabs.infoLaunch')}: ${launch}`)
  parts.push(`${t('ai.tabs.infoCreated')}: ${formatTimestamp(s.created_at)}`)
  return parts.join(' · ')
}

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
    // The server's sentence is English and names the flag; the key says the
    // same thing in the operator's language, next to the account selector
    // that is the fix. Reachable even though the dialog disables the
    // checkbox for root: the panel compares the account *name*, the server
    // its uid, so an account named otherwise that is uid 0 lands here.
    case 'AI_LAUNCH_ROOT_DANGER':
      return t('ai.errors.launchRoot')
    case 'AI_PROFILE_EXISTS':
      return t('ai.profiles.errors.exists')
    case 'AI_PROFILE_IN_USE':
      return t('ai.profiles.errors.inUse')
    default:
      return err.message || t('ai.errors.generic')
  }
}

/** Which profile route answered, because the two disagree about INVALID_BODY. */
export type ProfileSurface = 'create' | 'delete'

/**
 * aiErrorMessage for the profile surfaces.
 *
 * `create` refuses a name it will not turn into a directory with INVALID_BODY
 * and an English sentence naming the allowed shape; passed through, that
 * sentence is the one message a Korean operator cannot read, and it is also
 * the only refusal here they can act on — so it gets the key that spells the
 * rule out in their language.
 *
 * `delete` answers the same code for the default profile ("the default profile
 * is the tool's own directory and is not the panel's to delete"), which the
 * name rule would describe falsely — so the surface decides, not the code.
 *
 * Everywhere outside these two routes INVALID_BODY means a body the panel
 * itself built wrongly, where the server's own message is the more useful of
 * the two; those callers take aiErrorMessage directly.
 */
export function profileErrorMessage(err: unknown, t: Translate, surface: ProfileSurface): string {
  if (surface === 'create' && err instanceof Error && (err as Error & { code?: string }).code === 'INVALID_BODY') {
    return t('ai.profiles.errors.name')
  }
  return aiErrorMessage(err, t)
}
