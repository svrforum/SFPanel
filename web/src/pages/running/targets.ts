import type { RunningStack } from '@/lib/running'

export type Scope = 'node' | 'cluster'

/** Where a tile or a line leads, and whether that page belongs to one node. */
export interface Target {
  to: string
  /**
   * The page shows the selected node's own data (the containers screen), so in
   * the datacenter scope the node is selected first, as the cluster tree does.
   */
  onNode: boolean
}

/**
 * A managed stack opens on the stack screen, which in the datacenter scope
 * carries its node in the address. A project the panel does not manage has no
 * stack screen: it opens the node's containers, found by the project's name.
 */
export function stackTarget(scope: Scope, nodeId: string, stack: RunningStack): Target {
  if (!stack.managed) return { to: `/docker/containers?search=${encodeURIComponent(stack.name)}`, onNode: scope === 'cluster' }
  return scope === 'cluster'
    ? { to: `/cluster/stacks/${encodeURIComponent(nodeId)}/${encodeURIComponent(stack.name)}`, onNode: false }
    : { to: `/docker/stacks/${encodeURIComponent(stack.name)}`, onNode: false }
}

/** A container's detail on its node's containers screen, on its logs if asked. */
export function containerTarget(scope: Scope, id: string, logs = false): Target {
  return { to: `/docker/containers?container=${encodeURIComponent(id)}${logs ? '&tab=logs' : ''}`, onNode: scope === 'cluster' }
}

/** The nodes that are not answering. The list's "offline" filter takes suspect ones too. */
export const DOWN_NODES: Target = { to: '/cluster/nodes?status=offline', onNode: false }
