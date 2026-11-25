import type { RunningStack } from '@/lib/running'

export type Scope = 'node' | 'cluster'

/** Where a tile or a line leads, and whether that page belongs to one node. */
export interface Target {
  to: string
  /**
   * The page shows one node's own data, so in the datacenter scope the link
   * names the node in its address: opened here, in a new tab or from a
   * bookmark, it shows that node.
   */
  onNode: boolean
}

/**
 * A managed stack opens on the stack screen. A project the panel does not
 * manage has no stack screen: it opens the node's containers, found by the
 * project's name.
 */
export function stackTarget(scope: Scope, stack: RunningStack): Target {
  if (!stack.managed) return { to: `/docker/containers?search=${encodeURIComponent(stack.name)}`, onNode: scope === 'cluster' }
  return { to: `/docker/stacks/${encodeURIComponent(stack.name)}`, onNode: scope === 'cluster' }
}

/** A container's detail on its node's containers screen, on its logs if asked. */
export function containerTarget(scope: Scope, id: string, logs = false): Target {
  return { to: `/docker/containers?container=${encodeURIComponent(id)}${logs ? '&tab=logs' : ''}`, onNode: scope === 'cluster' }
}

/** The nodes that are not answering. The list's "offline" filter takes suspect ones too. */
export const DOWN_NODES: Target = { to: '/cluster/nodes?status=offline', onNode: false }

/** The element id of a node's section on the datacenter board, which a guest that is that node links to. */
export const nodeSectionId = (nodeId: string) => `running-node-${nodeId}`
