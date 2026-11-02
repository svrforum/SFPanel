import type { AgentInstallMethod, AgentInstallTailnetPeer } from '@/types/api'
import { DEFAULT_SSH_PORT, blank, hostIdentity, validName, type InstallServerRow } from './installForm'

// Picking the install's servers from the leader's tailnet
// (GET /cluster/agent-installs/tailnet), apart from the dialog that shows it.

/** The dialog offers a search from this many devices on. */
export const SEARCH_FROM = 9

/**
 * Whether a device can be picked: it is online (the install connects to it
 * now) and no node of the cluster sits at its address already.
 */
export function pickable(peer: AgentInstallTailnetPeer): boolean {
  return peer.online && !peer.node
}

/** The row's name: the device's when the form takes it, else empty (the server's hostname). */
export function pickName(peer: AgentInstallTailnetPeer): string {
  return peer.name && validName(peer.name) ? peer.name : ''
}

/** The devices whose name or IP holds the search, without case, in the order given. */
export function filterPeers(peers: AgentInstallTailnetPeer[], search: string): AgentInstallTailnetPeer[] {
  const q = search.trim().toLowerCase()
  if (!q) return peers
  return peers.filter((p) => p.name.toLowerCase().includes(q) || p.ip.includes(q))
}

/**
 * The table with the picks added, a row each, host the device's IP. A device
 * whose IP the table holds already is skipped. Blank rows go, as a paste
 * takes them: a table holding only the blank row it starts with is replaced
 * by the picks rather than followed. added are the devices that got a row.
 */
export function addPicks(
  servers: InstallServerRow[],
  picks: AgentInstallTailnetPeer[],
): { servers: InstallServerRow[]; added: AgentInstallTailnetPeer[] } {
  const seen = new Set(servers.map((r) => r.host.trim()).filter(Boolean).map(hostIdentity))
  const added: AgentInstallTailnetPeer[] = []
  for (const p of picks) {
    const id = hostIdentity(p.ip)
    if (seen.has(id)) continue
    seen.add(id)
    added.push(p)
  }
  if (added.length === 0) return { servers, added }
  const rows = added.map((p): InstallServerRow => ({ host: p.ip, port: String(DEFAULT_SSH_PORT), name: pickName(p), group: '' }))
  return { servers: [...servers.filter((r) => !blank(r)), ...rows], added }
}

/**
 * The login after servers were added: Tailscale SSH when every one added
 * runs it (switched when it was another), else as it was. withoutSsh names
 * the added servers that do not run it, when some others do.
 */
export function methodAfterPicks(
  method: AgentInstallMethod,
  added: AgentInstallTailnetPeer[],
): { method: AgentInstallMethod; switched: boolean; withoutSsh: string[] } {
  const withSsh = added.filter((p) => p.ssh)
  if (added.length > 0 && withSsh.length === added.length) {
    return { method: 'tailscale', switched: method !== 'tailscale', withoutSsh: [] }
  }
  const withoutSsh = withSsh.length > 0 ? added.filter((p) => !p.ssh).map((p) => p.name || p.ip) : []
  return { method, switched: false, withoutSsh }
}
