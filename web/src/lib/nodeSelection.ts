import { hrefWithNode } from '@/lib/serverScope'

/**
 * Broadcast whenever the API client is scoped to another node. Layout remounts
 * the page on it, and the shell's server scope takes the stored choice.
 */
export const NODE_CHANGED = 'sfpanel:node-changed'

export interface NodeChangedDetail {
  /** The node now shown; the local node's own id when it is the local one. */
  nodeId: string | null
}

/**
 * Opens a node's dashboard, or another of its pages, at an address that names
 * the node. The shell selects the node from the address, the way it does for
 * a bookmark or a new tab; nothing here selects it.
 */
export function openNode(nodeId: string, navigate: (to: string) => void, to?: string): void {
  navigate(hrefWithNode(to ?? '/dashboard', nodeId))
}
