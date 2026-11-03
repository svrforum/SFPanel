import type { TFunction } from 'i18next'
import type {
  AgentInstallAccount, AgentInstallMethod, AgentInstallMode, AgentInstallRequest, AgentInstallSavedAccount, AgentInstallServer,
} from '@/types/api'
import { NODE_NAME_PATTERN } from '../../../joinCommands'

// The agent install form, apart from its components. Its checks mirror the
// leader's (internal/feature/agentinstall normalise and accountProblem) so the
// form says a problem before anything is sent; the leader stays the judge.

export const MAX_SERVERS = 50
export const MAX_CONCURRENCY = 5
export const DEFAULT_CONCURRENCY = 3
export const DEFAULT_SSH_PORT = 22

// A server as the table holds it: what the operator typed. An empty port is
// 22, an empty name the server's hostname, an empty group none.
export interface InstallServerRow {
  host: string
  port: string
  name: string
  group: string
}

// The whole form. The four secrets live in component state only and leave the
// browser in a body alone: the start's (buildInstallRequest), or a saved
// login's when the operator saves them (installLogins.ts). sudoSame — the
// login password is sudo's too — is the default, and means something for a
// password login only.
export interface InstallForm {
  servers: InstallServerRow[]
  user: string
  method: AgentInstallMethod
  password: string
  key: string
  passphrase: string
  sudoSame: boolean
  sudoPassword: string
  mode: AgentInstallMode
  concurrency: number
}

// The account's fields, which a saved login's change has too.
export type InstallAccountFields = Pick<InstallForm, 'user' | 'method' | 'password' | 'key' | 'passphrase' | 'sudoSame' | 'sudoPassword'>

export function emptyServerRow(): InstallServerRow {
  return { host: '', port: String(DEFAULT_SSH_PORT), name: '', group: '' }
}

export function emptyInstallForm(): InstallForm {
  return {
    servers: [emptyServerRow()],
    user: '',
    method: 'password',
    password: '',
    key: '',
    passphrase: '',
    sudoSame: true,
    sudoPassword: '',
    mode: 'auto',
    concurrency: DEFAULT_CONCURRENCY,
  }
}

// ---- Fields ----

const IPV4 = /^(0|[1-9]\d{0,2})(\.(0|[1-9]\d{0,2})){3}$/
const LABEL = /^[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?$/

/**
 * An address or host name the leader can use: an IPv4 address as Go's
 * net.ParseIP takes it (no leading zeros), an IPv6 address without brackets
 * or a zone, or a DNS name (Tailscale's MagicDNS names included), which the
 * leader resolves. Ports, users and schemes are not part of it.
 */
export function validHost(host: string): boolean {
  if (host === '' || host.length > 253) return false
  if (host.includes(':')) return ipv6(host) !== null
  if (/^[\d.]+$/.test(host)) return IPV4.test(host) && host.split('.').every((part) => Number(part) <= 255)
  const labels = (host.endsWith('.') ? host.slice(0, -1) : host).split('.')
  return labels.every((label) => LABEL.test(label))
}

// The canonical form of an IPv6 address (2001:DB8:0::1 → 2001:db8::1), or
// null for anything that is not one. The URL parser is the browser's own
// IPv6 reader: it refuses zones and stray characters.
function ipv6(host: string): string | null {
  if (!/^[0-9A-Fa-f:.]+$/.test(host)) return null
  try {
    return new URL(`http://[${host}]/`).hostname.slice(1, -1)
  } catch {
    return null
  }
}

/** An SSH port, 1 to 65535, written as digits alone. */
export function validPort(port: string): boolean {
  return /^\d{1,5}$/.test(port) && Number(port) >= 1 && Number(port) <= 65535
}

/** A group label value as the cluster's label check takes it; empty is no group. */
export function validGroup(group: string): boolean {
  return /^[a-zA-Z0-9._-]{0,63}$/.test(group)
}

/** A node name as the leader takes it; empty is the server's hostname. */
export function validName(name: string): boolean {
  return name === '' || NODE_NAME_PATTERN.test(name)
}

// What makes two hosts one, as the leader judges it (hostIdentity): an
// address in its canonical form, a name without case or its trailing dot.
export function hostIdentity(host: string): string {
  const v6 = host.includes(':') ? ipv6(host) : null
  return v6 ?? host.toLowerCase().replace(/\.$/, '')
}

// ---- Pasting servers ----

export type LineField = 'host' | 'port' | 'name' | 'group' | 'extra'

// A pasted line that was left out, by its line number from 1, counting
// comments and blank lines as the text area does. sameLine is the line the
// same host came on first.
export interface LineProblem {
  line: number
  field: LineField
  sameLine?: number
}

/**
 * Reads pasted servers, one a line: host[:port] [name] [group], separated by
 * spaces. An IPv6 address takes its port in brackets ([2001:db8::1]:2222), or
 * none (2001:db8::1). # starts a comment; blank lines are skipped. A line with
 * a problem is left out of servers and named in problems, as is a host that
 * came on an earlier line.
 */
export function parseServerLines(text: string): { servers: InstallServerRow[]; problems: LineProblem[] } {
  const servers: InstallServerRow[] = []
  const problems: LineProblem[] = []
  const seen = new Map<string, number>()
  text.split(/\r?\n/).forEach((raw, i) => {
    const line = i + 1
    const words = raw.replace(/#.*$/, '').trim().split(/\s+/).filter(Boolean)
    if (words.length === 0) return
    if (words.length > 3) {
      problems.push({ line, field: 'extra' })
      return
    }
    const [address, name = '', group = ''] = words
    const split = splitHostPort(address)
    if (!split || !validHost(split.host)) {
      problems.push({ line, field: 'host' })
      return
    }
    if (!validPort(split.port)) {
      problems.push({ line, field: 'port' })
      return
    }
    if (!validName(name)) {
      problems.push({ line, field: 'name' })
      return
    }
    if (!validGroup(group)) {
      problems.push({ line, field: 'group' })
      return
    }
    const id = hostIdentity(split.host)
    const first = seen.get(id)
    if (first !== undefined) {
      problems.push({ line, field: 'host', sameLine: first })
      return
    }
    seen.set(id, line)
    servers.push({ host: split.host, port: split.port, name, group })
  })
  return { servers, problems }
}

// host[:port], with brackets around an IPv6 address that has a port. A bare
// address with more than one colon is IPv6 with no port. null when the
// brackets do not close.
function splitHostPort(word: string): { host: string; port: string } | null {
  const port = String(DEFAULT_SSH_PORT)
  if (word.startsWith('[')) {
    const m = /^\[([^\]]*)\](?::(.*))?$/.exec(word)
    if (!m || !m[1].includes(':')) return null
    return { host: m[1], port: m[2] ?? port }
  }
  const colons = word.split(':').length - 1
  if (colons === 1) {
    const at = word.indexOf(':')
    return { host: word.slice(0, at), port: word.slice(at + 1) }
  }
  return { host: word, port }
}

export function lineProblemText(p: LineProblem, t: TFunction): string {
  const problem = p.sameLine !== undefined
    ? t('cluster.tasks.install.line.hostTwice', { other: p.sameLine })
    : p.field === 'extra'
      ? t('cluster.tasks.install.problem.extra')
      : fieldProblemText(p.field, t)
  return t('cluster.tasks.install.line.problem', { line: p.line, problem })
}

// ---- The form's problems ----

// A problem with the form, named as the leader names it (details.field of
// AGENT_INSTALL_INVALID). index is the table row of a server's problem, and
// sameAs the row its host came on first; max the most servers one install
// takes.
export interface FormProblem {
  field: string
  index?: number
  sameAs?: number
  max?: number
}

export const blank = (r: InstallServerRow) => !r.host.trim() && !r.name.trim() && !r.group.trim()

// The table rows sent, in order: a row left blank is not. The leader's row
// s<n> is the n-th of these.
function sentRows(form: InstallForm): number[] {
  return form.servers.flatMap((r, i) => (blank(r) ? [] : [i]))
}

/**
 * Everything the leader would refuse in the form, in its order: the server
 * list, then each server, then the account, mode and concurrency. A secret is
 * looked at only for being there. What only the leader can know — whether a
 * name resolves, a login works — waits for the install. With savedLogin the
 * account is a saved login's, which the leader checked when it was saved:
 * the account's fields are not looked at.
 */
export function formProblems(form: InstallForm, savedLogin = false): FormProblem[] {
  const rows = sentRows(form)
  if (rows.length === 0) return [{ field: 'servers' }]
  if (rows.length > MAX_SERVERS) return [{ field: 'servers', max: MAX_SERVERS }]
  const problems: FormProblem[] = []
  const seen = new Map<string, number>()
  for (const index of rows) {
    const r = form.servers[index]
    const host = r.host.trim()
    const port = r.port.trim()
    if (!validHost(host)) {
      problems.push({ field: 'host', index })
      continue
    }
    const first = seen.get(hostIdentity(host))
    if (first !== undefined) {
      problems.push({ field: 'host', index, sameAs: first })
      continue
    }
    seen.set(hostIdentity(host), index)
    if (port !== '' && !validPort(port)) problems.push({ field: 'port', index })
    else if (!validName(r.name.trim())) problems.push({ field: 'name', index })
    else if (!validGroup(r.group.trim())) problems.push({ field: 'group', index })
  }
  if (!savedLogin) {
    if (!form.user.trim()) problems.push({ field: 'user' })
    if (form.method === 'password' && form.password === '') problems.push({ field: 'password' })
    if (form.method === 'key' && !form.key.trim()) problems.push({ field: 'key' })
  }
  if (!['auto', 'online', 'offline'].includes(form.mode)) problems.push({ field: 'mode' })
  if (!Number.isInteger(form.concurrency) || form.concurrency < 1 || form.concurrency > MAX_CONCURRENCY) {
    problems.push({ field: 'concurrency' })
  }
  return problems
}

/**
 * The form problem a 400 AGENT_INSTALL_INVALID names, its row (s1 …) put back
 * on the table row it was sent from; null for any other error.
 */
export function problemFromError(err: unknown, form: InstallForm): FormProblem | null {
  const { code, details } = (err ?? {}) as { code?: string; details?: Record<string, string> }
  if (code !== 'AGENT_INSTALL_INVALID' || !details?.field) return null
  const rows = sentRows(form)
  const tableRow = (key?: string) => {
    const m = key ? /^s(\d+)$/.exec(key) : null
    return m ? rows[Number(m[1]) - 1] : undefined
  }
  const p: FormProblem = { field: details.field }
  const index = tableRow(details.row)
  const sameAs = tableRow(details.same_as)
  if (index !== undefined) p.index = index
  if (sameAs !== undefined) p.sameAs = sameAs
  if (details.max && /^\d+$/.test(details.max)) p.max = Number(details.max)
  return p
}

export function formProblemText(p: FormProblem, t: TFunction): string {
  if (p.field === 'servers') {
    return p.max !== undefined ? t('cluster.tasks.install.problem.serversMax', { max: p.max }) : t('cluster.tasks.install.problem.servers')
  }
  if (p.field === 'host' && p.sameAs !== undefined) return t('cluster.tasks.install.problem.hostTwice', { other: p.sameAs + 1 })
  return fieldProblemText(p.field, t)
}

/** A login method's name; one this screen does not know, as sent. */
export function methodText(method: string, t: TFunction): string {
  switch (method) {
    case 'password': return t('cluster.tasks.install.setup.methodPassword')
    case 'key': return t('cluster.tasks.install.setup.methodKey')
    case 'tailscale': return t('cluster.tasks.install.setup.methodTailscale')
    default: return method
  }
}

function fieldProblemText(field: string, t: TFunction): string {
  switch (field) {
    case 'host': return t('cluster.tasks.install.problem.host')
    case 'port': return t('cluster.tasks.install.problem.port')
    case 'name': return t('cluster.tasks.install.problem.name')
    case 'group': return t('cluster.tasks.install.problem.group')
    case 'user': return t('cluster.tasks.install.problem.user')
    case 'method': return t('cluster.tasks.install.problem.method')
    case 'password': return t('cluster.tasks.install.problem.password')
    case 'key': return t('cluster.tasks.install.problem.key')
    case 'passphrase': return t('cluster.tasks.install.problem.passphrase')
    case 'sudo_same': return t('cluster.tasks.install.problem.sudoSame')
    case 'sudo_password': return t('cluster.tasks.install.problem.sudoPassword')
    case 'mode': return t('cluster.tasks.install.problem.mode')
    case 'concurrency': return t('cluster.tasks.install.problem.concurrency')
    default: return t('cluster.tasks.install.problem.other')
  }
}

// ---- The request ----

/**
 * The start's body. Each server is sent with its port (22 when left empty),
 * and a name and group only when given. login, a saved login's ID, is the
 * whole account: it goes alone, whatever the account's fields hold. Without
 * it, the account is the one typed (formAccount).
 */
export function buildInstallRequest(form: InstallForm, review: boolean, login?: string): AgentInstallRequest {
  const servers = sentRows(form).map((i): AgentInstallServer => {
    const r = form.servers[i]
    const s: AgentInstallServer = { host: r.host.trim(), port: Number(r.port.trim() || DEFAULT_SSH_PORT) }
    if (r.name.trim()) s.name = r.name.trim()
    if (r.group.trim()) s.group = r.group.trim()
    return s
  })
  const account: AgentInstallAccount | AgentInstallSavedAccount = login ? { login } : formAccount(form)
  return { servers, account, review, mode: form.mode, concurrency: form.concurrency }
}

/**
 * The account as typed. It carries the chosen method's secrets and no
 * other's, whatever the hidden fields still hold: sudo_same only for a
 * password login and never with sudo_password, and no sudo secret at all for
 * root, who needs none. Secrets go as typed, untrimmed.
 */
export function formAccount(form: InstallAccountFields): AgentInstallAccount {
  const user = form.user.trim()
  const account: AgentInstallAccount = { user, method: form.method }
  if (form.method === 'password') account.password = form.password
  if (form.method === 'key') {
    account.key = form.key
    if (form.passphrase) account.passphrase = form.passphrase
  }
  if (user !== 'root') {
    if (form.method === 'password' && form.sudoSame) account.sudo_same = true
    else if (form.sudoPassword) account.sudo_password = form.sudoPassword
  }
  return account
}
