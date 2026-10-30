import { api } from '@/lib/api'

/**
 * Broadcast whenever the API client is scoped to another node. Layout remounts
 * the page on it, and the cluster tree moves its highlight to detail.nodeId.
 */
export const NODE_CHANGED = 'sfpanel:node-changed'

export interface NodeChangedDetail {
  /** The node to highlight; the local node's own id when it is the local one. */
  nodeId: string
}

/**
 * Opens a node's dashboard from anywhere outside the tree. The local node is
 * the unscoped client (null), and the event still names it, so the tree can
 * highlight it — a bare null cannot tell the local node from the datacenter.
 */
export function openNode(nodeId: string, localId: string, navigate: (to: string) => void): void {
  api.setCurrentNode(nodeId === localId ? null : nodeId)
  window.dispatchEvent(new CustomEvent<NodeChangedDetail>(NODE_CHANGED, { detail: { nodeId } }))
  navigate('/dashboard')
}
