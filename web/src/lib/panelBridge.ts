import { isAgent } from '@/lib/clusterNodes'
import { navFor, type NavGroup } from '@/lib/navigation'
import type { ServerScope } from '@/hooks/useServerScope'

/**
 * What the page tells the Android app it runs in, on window.__sfpanel: the
 * server the pages act on and the menu, so the app's title and feature sheet
 * follow the panel instead of a list of their own. The app reads it when the
 * address or the title changes and checks v, so the shape only grows.
 */

/** Fired on window when the server the pages act on changes; the detail is the new server. */
export const SERVER_EVENT = 'sfpanel:server'

export interface BridgeServer {
  name: string
  role: 'manager' | 'agent' | null
  /** The server this panel runs on, not one reached through it. */
  attached: boolean
}

export interface BridgeLink {
  /** In the interface's language. */
  label: string
  /** A path on this origin: starts with one slash. */
  path: string
}

export interface BridgeItem extends BridgeLink {
  tabs: BridgeLink[]
}

export interface BridgeGroup {
  group: NavGroup
  /** The group's heading; empty when it has none. */
  title: string
  items: BridgeItem[]
}

export interface PanelBridge {
  v: 1
  /** Null on a single server, and in a cluster until the node list names the server. */
  server: BridgeServer | null
  nav: BridgeGroup[]
}

declare global {
  interface Window {
    __sfpanel?: PanelBridge
  }
}

/** The server to name: in a cluster, the one the pages act on once the node list has it; no one otherwise. */
export function bridgeServer(scope: Pick<ServerScope, 'clusterEnabled' | 'current' | 'selected'>): BridgeServer | null {
  if (scope.clusterEnabled !== true || !scope.current) return null
  return {
    name: scope.current.name,
    role: isAgent(scope.current) ? 'agent' : 'manager',
    attached: scope.selected === null,
  }
}

const GROUP_ORDER: NavGroup[] = ['server', 'fleet', 'tail']

/**
 * The menu for the panel's mode, in sidebar order with every tab. In a
 * cluster the server group is headed by the server's name and the fleet group
 * by its own; a single server's menu has no headings.
 */
export function buildBridge(cluster: boolean, server: BridgeServer | null, t: (key: string) => string): PanelBridge {
  const entries = navFor(cluster)
  const title = (group: NavGroup): string => {
    if (!cluster) return ''
    if (group === 'server') return server?.name ?? ''
    if (group === 'fleet') return t('layout.groups.fleet')
    return ''
  }
  return {
    v: 1,
    server,
    nav: GROUP_ORDER.map((group) => ({
      group,
      title: title(group),
      items: entries.filter((e) => e.group === group).map((e) => ({
        label: t(e.labelKey),
        path: e.to,
        tabs: (e.tabs ?? []).map((tab) => ({ label: t(tab.labelKey), path: tab.to })),
      })),
    })),
  }
}

/**
 * Put the bridge on the target, and announce the server when it is not the
 * one already published: the first publish announces, a relabelled menu or a
 * reread node list with the same server does not.
 */
export function publishBridge(target: { __sfpanel?: PanelBridge; dispatchEvent(e: Event): boolean }, next: PanelBridge): void {
  const before = target.__sfpanel
  target.__sfpanel = next
  if (before && JSON.stringify(before.server) === JSON.stringify(next.server)) return
  target.dispatchEvent(new CustomEvent(SERVER_EVENT, { detail: next.server }))
}
