import type { TFunction } from 'i18next'
import type { StatusPillTone } from '@/components/StatusPill'
import { taskRunHref } from '@/lib/taskPrefill'
import type { AgentConnectionState, AgentSettingsInput, AgentSettingsView, AgentStatus, WazuhAgentStatus, ZabbixAgentStatus } from '@/types/api'

export type AgentName = 'zabbix' | 'wazuh'

/** The built-in group task that deploys each agent. */
export const AGENT_TASK: Record<AgentName, string> = { zabbix: 'builtin/zabbix-agent', wazuh: 'builtin/wazuh-agent' }

/** The port a Zabbix active address without one is reached on. */
const ZABBIX_ACTIVE_PORT = 10051

export type Tone = Extract<StatusPillTone, 'success' | 'warning' | 'destructive' | 'muted'>

/**
 * One agent on one server, as the table shows it. An installed agent's
 * service is running (active), stopped (inactive or failed), starting,
 * reloading, not known (systemd gave no word), or another word, as sent.
 */
export interface AgentCell {
  install: 'none' | 'running' | 'stopped' | 'activating' | 'reloading' | 'serviceUnknown' | 'serviceOther' | 'legacy' | 'unmanaged'
  /** systemd's word for serviceOther. */
  service?: string
  version?: string
  source?: string
  held?: boolean
  match: 'same' | 'different' | 'noConfig' | 'unset'
  /** What the config file says, when it is not the central value. */
  fileValue?: string
  /** Why a config file that is there could not be read. */
  configError?: string
  /** Keys the config file sets on more than one line. */
  duplicates?: string[]
  connection?: { state: AgentConnectionState; tone: Tone; detail?: string; lastKeepalive?: string }
  /** When the answer shown was read, for one a later refresh could not replace (ms). */
  readAt?: number
}

/**
 * Only a connection is green. A service that is active and an agent that
 * waits on its server are not: an agent ran for months, active, against a
 * server that was gone.
 */
export function connectionTone(state: AgentConnectionState): Tone {
  switch (state) {
    case 'connected':
      return 'success'
    case 'pending':
    case 'never':
    case 'not_registered':
    case 'stale':
      return 'warning'
    case 'unreachable':
    case 'disconnected':
    case 'failing':
    case 'not_running':
      return 'destructive'
    default:
      return 'muted'
  }
}

/** A host as compared: spaces around it and letter case do not count. */
const hostKey = (s: string) => s.trim().toLowerCase()

export function sameHost(a: string, b: string): boolean {
  return hostKey(a) === hostKey(b)
}

const serverSet = (list: string) => new Set(list.split(',').map(hostKey).filter(Boolean))

/** Two Zabbix Server lists name the same servers, in any order. */
export function sameServers(a: string, b: string): boolean {
  const x = serverSet(a)
  const y = serverSet(b)
  return x.size === y.size && [...x].every((s) => y.has(s))
}

/** host[:port] as an agent reaches it; null for a list or a value that is not one address. */
function activeAddress(value: string): { host: string; port: number } | null {
  const v = value.trim()
  if (v === '' || v.includes(',')) return null
  const bracketed = /^\[([^\]]+)\](?::(\d+))?$/.exec(v)
  if (bracketed) return { host: bracketed[1], port: bracketed[2] ? Number(bracketed[2]) : ZABBIX_ACTIVE_PORT }
  const parts = v.split(':')
  // More than one colon is an IPv6 address without a port.
  if (parts.length !== 2) return { host: v, port: ZABBIX_ACTIVE_PORT }
  return /^\d+$/.test(parts[1]) ? { host: parts[0], port: Number(parts[1]) } : null
}

/** Two ServerActive values reach the same address; one without a port is on 10051. */
export function sameActive(a: string, b: string): boolean {
  const x = activeAddress(a)
  const y = activeAddress(b)
  if (!x || !y) return hostKey(a) === hostKey(b) && hostKey(a) !== ''
  return sameHost(x.host, y.host) && x.port === y.port
}

/** The agent has the values a deployment needs: both Zabbix addresses, or the Wazuh manager. */
export function configured(s: AgentSettingsView | null, agent: AgentName): boolean {
  if (!s) return false
  if (agent === 'zabbix') return s.zabbix.server.trim() !== '' && s.zabbix.server_active.trim() !== ''
  return s.wazuh.manager.trim() !== ''
}

// Stopped is a claim: only inactive and failed make it. A service starting
// or reloading is on its way to one or the other.
function installOf(state: ZabbixAgentStatus['state'] | WazuhAgentStatus['state'], service: string | undefined): Pick<AgentCell, 'install' | 'service'> {
  if (state !== 'installed') return { install: state }
  switch (service) {
    case 'active': return { install: 'running' }
    case 'inactive':
    case 'failed': return { install: 'stopped' }
    case 'activating': return { install: 'activating' }
    case 'reloading': return { install: 'reloading' }
    case undefined:
    case '':
    case 'unknown': return { install: 'serviceUnknown' }
    default: return { install: 'serviceOther', service }
  }
}

function connectionOf(c: ZabbixAgentStatus['connection']): AgentCell['connection'] {
  if (!c) return undefined
  return {
    state: c.state,
    tone: connectionTone(c.state),
    ...(c.detail ? { detail: c.detail } : {}),
    ...(c.last_keepalive ? { lastKeepalive: c.last_keepalive } : {}),
  }
}

export function zabbixCell(z: ZabbixAgentStatus, s: AgentSettingsView | null): AgentCell {
  const cell: AgentCell = { ...installOf(z.state, z.service), match: 'unset', connection: connectionOf(z.connection) }
  // A version is a package's: an agent without one (not a package, or the old
  // agent's config file alone) comes from no repository, whatever "other" says.
  if (z.version) {
    cell.version = z.version
    cell.source = z.source
  }
  if (z.config?.duplicates?.length) cell.duplicates = z.config.duplicates
  if (!configured(s, 'zabbix')) return cell
  if (!z.config) return { ...cell, match: 'noConfig' }
  if (sameServers(z.config.server, s!.zabbix.server) && sameActive(z.config.server_active, s!.zabbix.server_active)) return { ...cell, match: 'same' }
  return { ...cell, match: 'different', fileValue: `Server=${z.config.server} · ServerActive=${z.config.server_active}` }
}

export function wazuhCell(w: WazuhAgentStatus, s: AgentSettingsView | null): AgentCell {
  const cell: AgentCell = { ...installOf(w.state, w.service), match: 'unset', held: !!w.held, connection: connectionOf(w.connection) }
  if (w.version) cell.version = w.version
  if (w.config_error) cell.configError = w.config_error
  if (!configured(s, 'wazuh')) return cell
  if (!w.config) return { ...cell, match: 'noConfig' }
  const servers = w.config.servers ?? []
  if (servers.length === 1 && sameHost(servers[0], s!.wazuh.manager)) return { ...cell, match: 'same' }
  return { ...cell, match: 'different', fileValue: servers.join(', ') }
}

/**
 * A cell from an answer a later refresh could not replace: the agent may have
 * gone since, so no connection keeps its colour — "connected" least of all —
 * and the cell says when it was read.
 */
export function staleCell(cell: AgentCell, readAt: number): AgentCell {
  return { ...cell, ...(cell.connection ? { connection: { ...cell.connection, tone: 'muted' } } : {}), readAt }
}

/** The values typed in the card differ from the saved ones, which are what a run uses. */
export function settingsDirty(draft: AgentSettingsInput | null, saved: AgentSettingsView | null): boolean {
  if (!draft) return false
  if (!saved) return true
  return (['server', 'server_active', 'host_metadata'] as const).some((k) => draft.zabbix[k] !== saved.zabbix[k])
    || (['manager', 'groups', 'version'] as const).some((k) => draft.wazuh[k] !== saved.wazuh[k])
}

/** Why a deploy or a re-apply cannot open now: values typed and not saved, no saved values for the agent, no server. */
export type ActionHold = 'unsaved' | 'values' | 'servers'

/**
 * Whether an agent's deploy or re-apply can open, and every reason it cannot.
 * A run takes the saved values, so the card's unsaved ones hold it. Values not
 * read yet hold it with no reason: nothing is known to be missing.
 */
export function actionState(agent: AgentName, { settings, dirty, servers }: { settings: AgentSettingsView | null; dirty: boolean; servers: number }): { enabled: boolean; holds: ActionHold[] } {
  const holds: ActionHold[] = []
  if (dirty) holds.push('unsaved')
  if (settings && !configured(settings, agent)) holds.push('values')
  if (servers === 0) holds.push('servers')
  return { enabled: holds.length === 0 && configured(settings, agent), holds }
}

/** The ids of the lines that say why a control is off, for its aria-describedby; undefined for none. */
export function holdsDescribedBy(holds: ActionHold[], lineId: (hold: ActionHold) => string | undefined): string | undefined {
  return holds.map(lineId).filter(Boolean).join(' ') || undefined
}

/** The servers whose agent is its package, in row order: a re-apply sets their values again. */
export function reapplyNodes(rows: { id: string; status?: AgentStatus }[], agent: AgentName): string[] {
  return rows.filter((r) => r.status?.[agent].state === 'installed').map((r) => r.id)
}

/** The agent's group task, opened with the servers picked. */
export function deployHref(agent: AgentName, nodes: string[]): string {
  return taskRunHref(AGENT_TASK[agent], { nodes })
}

// Each key written out, for the check that every key the code asks for exists.

export function installText(cell: Pick<AgentCell, 'install' | 'service'>, t: TFunction): string {
  switch (cell.install) {
    case 'none': return t('agents.install.none')
    case 'running': return t('agents.install.running')
    case 'stopped': return t('agents.install.stopped')
    case 'activating': return t('agents.install.activating')
    case 'reloading': return t('agents.install.reloading')
    case 'serviceUnknown': return t('agents.install.serviceUnknown')
    case 'serviceOther': return t('agents.install.serviceOther', { state: cell.service ?? '' })
    case 'legacy': return t('agents.install.legacy')
    case 'unmanaged': return t('agents.install.unmanaged')
  }
}

/** A source this page does not know, as sent. */
export function sourceText(source: string, t: TFunction): string {
  switch (source) {
    case 'vendor': return t('agents.source.vendor')
    case 'distro': return t('agents.source.distro')
    case 'other': return t('agents.source.other')
    default: return source
  }
}

export function matchText(cell: AgentCell, t: TFunction): string {
  switch (cell.match) {
    case 'same': return t('agents.match.same')
    case 'different': return t('agents.match.different', { value: cell.fileValue ?? '' })
    case 'noConfig': return t('agents.match.noConfig')
    case 'unset': return t('agents.match.unset')
  }
}

/** A state a newer node sends that this page does not know, as sent. */
export function connectionText(state: AgentConnectionState, t: TFunction): string {
  switch (state) {
    case 'connected': return t('agents.connection.connected')
    case 'pending': return t('agents.connection.pending')
    case 'disconnected': return t('agents.connection.disconnected')
    case 'never': return t('agents.connection.never')
    case 'stale': return t('agents.connection.stale')
    case 'unreachable': return t('agents.connection.unreachable')
    case 'not_registered': return t('agents.connection.not_registered')
    case 'failing': return t('agents.connection.failing')
    case 'not_running': return t('agents.connection.not_running')
    case 'no_server': return t('agents.connection.no_server')
    case 'unknown': return t('agents.connection.unknown')
    default: return state
  }
}
