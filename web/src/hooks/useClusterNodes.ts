import { useEffect, useRef, useState } from 'react'
import { api } from '@/lib/api'
import type { ClusterStatus, ClusterNode } from '@/types/api'

export interface ClusterNodesState {
  /** null until the status probe answers; disabled clusters stay null-ish. */
  clusterStatus: ClusterStatus | null
  nodes: ClusterNode[]
  localId: string
  /** The node id the API client is currently scoped to (null = local). */
  selectedNode: string | null
  /** The full node entry for the current scope (falls back to the local node). */
  currentNode: ClusterNode | undefined
  /** Scope the API client to a node and broadcast sfpanel:node-changed. */
  selectNode: (id: string | null) => void
}

/**
 * Shared cluster-node picker state: probes cluster status, loads the node
 * list, and owns the select-node → api.setCurrentNode → node-changed-event
 * sequence. NodeSelector (desktop sidebar) and MoreMenu (mobile drawer) used
 * to implement this identically in parallel — the event contract stays the
 * same (Layout remounts the Outlet on sfpanel:node-changed).
 */
export function useClusterNodes(): ClusterNodesState {
  const [clusterStatus, setClusterStatus] = useState<ClusterStatus | null>(null)
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [localId, setLocalId] = useState('')
  const [selectedNode, setSelectedNode] = useState<string | null>(api.currentNode)

  useEffect(() => {
    api
      .getClusterStatus(true)
      .then((status) => {
        setClusterStatus(status)
        if (status.enabled && status.local_id) {
          setLocalId(status.local_id)
          api
            .getClusterNodes(true)
            .then((data) => setNodes(data.nodes))
            .catch(() => {})
        }
      })
      .catch(() => {})
  }, [])

  // Skip the broadcast for the initial mount sync — only user-driven
  // selection changes should remount the page outlet.
  const initialRef = useRef(true)
  useEffect(() => {
    api.setCurrentNode(selectedNode)
    if (initialRef.current) {
      initialRef.current = false
      return
    }
    window.dispatchEvent(new Event('sfpanel:node-changed'))
  }, [selectedNode])

  const currentNode = selectedNode
    ? nodes.find((n) => n.id === selectedNode)
    : nodes.find((n) => n.id === localId)

  return {
    clusterStatus,
    nodes,
    localId,
    selectedNode,
    currentNode,
    selectNode: setSelectedNode,
  }
}
