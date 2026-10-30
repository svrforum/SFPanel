import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '@/lib/api'
import { NODE_CHANGED, type NodeChangedDetail } from '@/lib/nodeSelection'
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
  /** Ask for the status and the node list again. */
  reload: () => void
}

/**
 * The cluster-node picker state of the phone's More drawer: probes cluster
 * status, loads the node list, and owns the select-node →
 * api.setCurrentNode → node-changed-event sequence (Layout remounts the
 * Outlet on sfpanel:node-changed).
 */
export function useClusterNodes(): ClusterNodesState {
  const [clusterStatus, setClusterStatus] = useState<ClusterStatus | null>(null)
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [localId, setLocalId] = useState('')
  const [selectedNode, setSelectedNode] = useState<string | null>(api.currentNode)

  // A follower answers 503 while it cannot reach the leader, so one ask per
  // page load could leave the picker empty until a reload, long after the
  // cluster had recovered. The drawer calls this again each time it opens.
  const reload = useCallback(() => {
    api
      .getClusterStatus()
      .then((status) => {
        setClusterStatus(status)
        if (status.enabled && status.local_id) {
          setLocalId(status.local_id)
          api
            .getClusterNodes()
            .then((data) => setNodes(data.nodes))
            .catch(() => {})
        }
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  // Skip the broadcast for the initial mount sync — only user-driven
  // selection changes should remount the page outlet.
  const initialRef = useRef(true)
  useEffect(() => {
    api.setCurrentNode(selectedNode)
    if (initialRef.current) {
      initialRef.current = false
      return
    }
    // Name the local node too, so the tree can highlight it: null alone does
    // not tell it from the datacenter. localId stays out of the deps — it
    // loading must not rebroadcast and remount the page.
    window.dispatchEvent(
      new CustomEvent<NodeChangedDetail>(NODE_CHANGED, { detail: { nodeId: selectedNode ?? localId } }),
    )
  }, [selectedNode]) // eslint-disable-line react-hooks/exhaustive-deps

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
    reload,
  }
}
