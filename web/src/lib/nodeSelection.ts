import { api } from '@/lib/api'

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
 * Opens a node's dashboard, or another of its pages. The local node is the
 * unscoped client (null), and the event still names it.
 */
export function openNode(nodeId: string, localId: string, navigate: (to: string) => void, to = '/dashboard'): void {
  api.setCurrentNode(nodeId === localId ? null : nodeId)
  window.dispatchEvent(new CustomEvent<NodeChangedDetail>(NODE_CHANGED, { detail: { nodeId } }))
  navigate(to)
}
