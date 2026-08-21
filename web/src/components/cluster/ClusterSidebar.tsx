import { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { api } from '@/lib/api'
import type { ClusterStatus, ClusterNode } from '@/types/api'
import SidebarSkeleton from '@/components/SidebarSkeleton'
import TreePanel, { type TreeSelection } from './TreePanel'
import ContextMenu from './ContextMenu'

const TREE_COLLAPSE_KEY = 'sfpanel-cluster-tree-collapsed'
const MENU_COLLAPSE_KEY = 'sfpanel-cluster-menu-collapsed'
const SELECTION_KEY = 'sfpanel-cluster-selection'

interface ClusterSidebarProps {
  /** Owned and polled by Layout; null until the first probe answers. */
  status: ClusterStatus | null
  panelVersion: string
  onLogout: () => void
  onNodeChanged: () => void
}

export default function ClusterSidebar({ status, panelVersion, onLogout, onNodeChanged }: ClusterSidebarProps) {
  const navigate = useNavigate()
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [nodesError, setNodesError] = useState(false)
  // Collapsed by default (a 2-3 node tree is mostly empty otherwise); a user who
  // explicitly expands it ('false') keeps it expanded.
  const [treeCollapsed, setTreeCollapsed] = useState(() => localStorage.getItem(TREE_COLLAPSE_KEY) !== 'false')
  const [menuCollapsed, setMenuCollapsed] = useState(() => localStorage.getItem(MENU_COLLAPSE_KEY) === 'true')

  const [selection, setSelection] = useState<TreeSelection>(() => {
    try {
      const saved = localStorage.getItem(SELECTION_KEY)
      if (saved) return JSON.parse(saved)
    } catch {
      // ignore parse errors and fall through to the default selection
    }
    return { type: 'datacenter' }
  })

  // The node list can fail independently (it 503s when the leader is
  // unreachable) while status still answers, so take the local id from status
  // — it was previously lost whenever the nodes call failed.
  const localId = status?.local_id ?? ''

  const initialLoad = useRef(true)
  // Set when the selection is being synced FROM an external node switch (the
  // cluster stacks page), so the selection effect updates the highlight only and
  // does NOT re-run its navigate / setCurrentNode side effects.
  const syncing = useRef(false)

  const loadNodes = useCallback(() => {
    api.getClusterNodes(true)
      .then((data) => {
        setNodes(data.nodes)
        setNodesError(false)
      })
      .catch(() => {
        // Keep the last good list and flag the failure. Substituting an empty
        // array (the old behaviour) rendered "leader unreachable" as "this
        // cluster has no nodes", with nothing on screen to say otherwise.
        setNodesError(true)
      })
  }, [])

  // Load on mount + poll every 15s while visible (paused when the tab is hidden).
  // Status itself comes from Layout — this only refreshes the node list.
  useVisibleInterval(loadNodes, 15000)

  useEffect(() => {
    localStorage.setItem(TREE_COLLAPSE_KEY, String(treeCollapsed))
  }, [treeCollapsed])

  useEffect(() => {
    localStorage.setItem(MENU_COLLAPSE_KEY, String(menuCollapsed))
  }, [menuCollapsed])

  // Re-highlight the tree when another surface switches to a remote node (e.g.
  // the cluster stacks page drilling into a peer's stack). Only remote ids are
  // synced: currentNode === null is ambiguous (local node vs datacenter), so we
  // leave the tree's own selection alone there rather than guess.
  useEffect(() => {
    const handler = () => {
      const nid = api.currentNode
      if (!nid) return
      setSelection((prev) => {
        if (prev.type === 'node' && prev.nodeId === nid) return prev
        syncing.current = true
        return { type: 'node', nodeId: nid }
      })
    }
    window.addEventListener('sfpanel:node-changed', handler)
    return () => window.removeEventListener('sfpanel:node-changed', handler)
  }, [])

  // Handle selection changes
  useEffect(() => {
    localStorage.setItem(SELECTION_KEY, JSON.stringify(selection))

    if (initialLoad.current) {
      initialLoad.current = false
      return
    }

    // Synced from an external switch: update the highlight only, don't navigate
    // away (the external switcher already navigated) or re-set the node.
    if (syncing.current) {
      syncing.current = false
      return
    }

    if (selection.type === 'datacenter') {
      api.setCurrentNode(null)
      onNodeChanged()
      navigate('/cluster/overview')
    } else {
      const targetNodeId = selection.nodeId
      const isLocal = targetNodeId === localId
      api.setCurrentNode(isLocal ? null : targetNodeId)
      onNodeChanged()
      navigate('/dashboard')
    }
  }, [selection]) // eslint-disable-line react-hooks/exhaustive-deps

  // Loading, not "no cluster": Layout only mounts us once it believes the
  // cluster is enabled. Returning null here used to remove the sidebar
  // entirely while the status call was in flight.
  if (!status) {
    return <SidebarSkeleton widthPx={(treeCollapsed ? 52 : 180) + (menuCollapsed ? 42 : 180)} />
  }

  const selectedNodeName = selection.type === 'node'
    ? nodes.find(n => n.id === selection.nodeId)?.name || 'Unknown'
    : ''

  return (
    <div className="flex h-full">
      <TreePanel
        clusterStatus={status}
        nodes={nodes}
        nodesError={nodesError}
        localId={localId}
        selection={selection}
        onSelect={setSelection}
        collapsed={treeCollapsed}
        onToggleCollapse={() => setTreeCollapsed(!treeCollapsed)}
        panelVersion={panelVersion}
        onLogout={onLogout}
      />
      <ContextMenu
        selection={selection}
        nodeName={selectedNodeName}
        collapsed={menuCollapsed}
        onToggleCollapse={() => setMenuCollapsed(!menuCollapsed)}
      />
    </div>
  )
}
