import type { TFunction } from 'i18next'
import type { AgentInstallLogin, AgentInstallLoginBody } from '@/types/api'
import { taskErrorMessage } from '@/lib/taskErrors'
import { formAccount, methodText, type InstallAccountFields } from './installForm'

// Saved logins (/cluster/agent-installs/logins), apart from the install pane
// and the dialog that show them. The cluster keeps them and answers whether
// each secret is stored, never the secret. This browser keeps the ID of the
// login the last install started with, and nothing else of them.

/** localStorage: the ID of the saved login the last install started with. Never a secret. */
export const LAST_LOGIN_KEY = 'sfpanel_install_login'

/** The login select's value for an account typed in the form. */
export const TYPED = ''

/** The most characters a saved login's name has (the leader's maxLoginName). */
export const MAX_LOGIN_NAME = 64

export function readLastLogin(): string | null {
  try {
    return localStorage.getItem(LAST_LOGIN_KEY)
  } catch {
    return null
  }
}

export function rememberLogin(id: string): void {
  try {
    localStorage.setItem(LAST_LOGIN_KEY, id)
  } catch {
    // private mode: the next pane starts on the first login listed
  }
}

/**
 * The login the pane starts on: the one the last install started with, while
 * the list still has it; else the first listed; else typing.
 */
export function defaultLogin(logins: AgentInstallLogin[], last: string | null): string {
  if (last && logins.some((l) => l.id === last)) return last
  return logins[0]?.id ?? TYPED
}

/** The list as the leader orders it: by name without regard to case, then by ID. */
export function sortLogins(logins: AgentInstallLogin[]): AgentInstallLogin[] {
  const order = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0)
  return [...logins].sort((a, b) => order(a.name.toLowerCase(), b.name.toLowerCase()) || order(a.id, b.id))
}

/** The list with login in it, in place of the one with its ID, in the leader's order. */
export function withLogin(logins: AgentInstallLogin[], login: AgentInstallLogin): AgentInstallLogin[] {
  return sortLogins([...logins.filter((l) => l.id !== login.id), login])
}

// ---- The name ----

// What Go's unicode.IsSpace takes, which the leader's strings.TrimSpace trims
// by. Not String.prototype.trim: that trims U+FEFF and keeps U+0085.
const SPACE = '[\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]+'
const EDGES = new RegExp(`^${SPACE}|${SPACE}$`, 'g')

/** A name as the leader stores it: without the white space at its ends. */
export function trimName(name: string): string {
  return name.replace(EDGES, '')
}

// Go's unicode.IsControl: the C0 and C1 controls, and nothing past Latin-1.
const isControl = (ch: string) => {
  const c = ch.codePointAt(0) ?? 0
  return c <= 0x1f || (c >= 0x7f && c <= 0x9f)
}

export type LoginNameProblem = 'empty' | 'long' | 'control' | 'taken'

/**
 * What the leader would refuse in a saved login's name: 1 to 64 characters
 * once trimmed, counted as characters, none of them a control character, and
 * no name another login has, without regard to case. except is the ID of the
 * login being changed, whose own name is no other's.
 */
export function loginNameProblem(name: string, logins: AgentInstallLogin[], except?: string): LoginNameProblem | null {
  const trimmed = trimName(name)
  const chars = [...trimmed]
  if (chars.length === 0) return 'empty'
  if (chars.length > MAX_LOGIN_NAME) return 'long'
  if (chars.some(isControl)) return 'control'
  const folded = trimmed.toLowerCase()
  if (logins.some((l) => l.id !== except && l.name.toLowerCase() === folded)) return 'taken'
  return null
}

export function loginNameText(p: LoginNameProblem, t: TFunction): string {
  switch (p) {
    case 'empty': return t('cluster.tasks.install.logins.nameEmpty')
    case 'long': return t('cluster.tasks.install.logins.nameLong')
    case 'control': return t('cluster.tasks.install.logins.nameControl')
    case 'taken': return t('cluster.tasks.install.errors.loginExists')
  }
}

// ---- How a login reads ----

/** A login as the select lists it: name (user · method). */
export function loginLabel(l: AgentInstallLogin, t: TFunction): string {
  return t('cluster.tasks.install.logins.option', { name: l.name, user: l.user, method: methodText(l.method, t) })
}

/**
 * The account a chosen login stands for, in one line: the user, the method,
 * and the sudo password — the login's own, a saved one, none, or none needed
 * for root.
 */
export function loginSummary(l: AgentInstallLogin, t: TFunction): string {
  let sudo: string
  if (l.user.trim() === 'root') sudo = t('cluster.tasks.install.logins.sudoRoot')
  else if (l.method === 'password' && l.sudo_same) sudo = t('cluster.tasks.install.logins.sudoSame')
  else if (l.has_sudo_password) sudo = t('cluster.tasks.install.logins.sudoSaved')
  else sudo = t('cluster.tasks.install.logins.sudoNone')
  return t('cluster.tasks.install.logins.summary', { user: l.user, method: methodText(l.method, t), sudo })
}

// ---- Saving and changing ----

/**
 * A saved login's body, for its creation or a change: the name, trimmed, and
 * the account as the start would send it (formAccount). A secret left empty
 * is not sent: a change keeps the stored one where the method uses it. A key
 * of blanks is no key.
 */
export function loginBody(name: string, fields: InstallAccountFields): AgentInstallLoginBody {
  const a = formAccount(fields)
  const body: AgentInstallLoginBody = { name: trimName(name), user: a.user, method: a.method }
  if (a.password) body.password = a.password
  if (a.key?.trim()) body.key = a.key
  if (a.passphrase) body.passphrase = a.passphrase
  if (a.sudo_same) body.sudo_same = true
  if (a.sudo_password) body.sudo_password = a.sudo_password
  return body
}

/** A saved login's change as the dialog holds it: its name and its account, the secrets empty until typed. */
export interface LoginChange extends InstallAccountFields {
  name: string
}

/** A change's starting point: the login as listed, no secret in it. */
export function changeFromLogin(l: AgentInstallLogin): LoginChange {
  return { name: l.name, user: l.user, method: l.method, password: '', key: '', passphrase: '', sudoSame: l.sudo_same, sudoPassword: '' }
}

/**
 * What the leader would refuse in a change of login: its name, then the
 * account's fields (the request's names) once a secret left empty is taken
 * from the stored login, as the leader takes it where the method uses it.
 */
export function loginChangeProblems(
  change: LoginChange,
  login: AgentInstallLogin,
  logins: AgentInstallLogin[],
): { name: LoginNameProblem | null; fields: string[] } {
  const fields: string[] = []
  if (!change.user.trim()) fields.push('user')
  if (change.method === 'password' && change.password === '' && !login.has_password) fields.push('password')
  if (change.method === 'key' && !change.key.trim() && !login.has_key) fields.push('key')
  return { name: loginNameProblem(change.name, logins, login.id), fields }
}

// ---- Refusals ----

/**
 * A saved login's write refused, in the operator's language. Its body is
 * capped at 64 KiB, not the 1 MiB of other task requests, and a 413 carries
 * no code to say so.
 */
export function loginErrorText(err: unknown, t: TFunction): string {
  const { status, code } = (err ?? {}) as { status?: number; code?: string }
  if (status === 413 && !code) return t('cluster.tasks.install.errors.loginTooLarge')
  return taskErrorMessage(err, t)
}

/**
 * The leader's refusal of a saved login's name, to show on the name: another
 * login has it, or it is not 1 to 64 characters without a control character.
 * null for any other refusal.
 */
export function loginNameRefusal(err: unknown, t: TFunction): string | null {
  const { code, details } = (err ?? {}) as { code?: string; details?: Record<string, string> }
  if (code === 'AGENT_INSTALL_LOGIN_EXISTS') return t('cluster.tasks.install.errors.loginExists')
  if (code === 'AGENT_INSTALL_INVALID' && details?.field === 'name') return t('cluster.tasks.install.logins.nameInvalid')
  return null
}
