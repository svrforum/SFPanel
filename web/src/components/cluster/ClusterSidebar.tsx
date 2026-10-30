import { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { api } from '@/lib/api'
import { NODE_CHANGED, type NodeChangedDetail } from '@/lib/nodeSelection'
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
  /** The node now shown, or null for the datacenter. */
  onNodeChanged: (nodeId: string | null) => void
}

export default function ClusterSidebar({ status, panelVersion, onLogout, onNodeChanged }: ClusterSidebarProps) {
  const navigate = useNavigate()
  const { t } = useTranslation()
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
  // Set when the selection is being synced FROM a node switch made elsewhere
  // (the phone's drawer, a node list's "Open node", a node that left), so the
  // selection effect updates the highlight only and does NOT re-run its
  // navigate / setCurrentNode side effects.
  const syncing = useRef(false)

  const loadNodes = useCallback(() => {
    api.getClusterNodes()
      .then((data) => {
        setNodes(data.nodes)
        setNodesError(false)
        // The node being shown left the cluster (removed, or converted and not
        // back yet), whether picked now or remembered from an earlier visit:
        // every page would ask the manager for it and be told "Node not found".
        // Show this node instead. An empty list says nothing about who is in it.
        const current = api.currentNode
        if (current && data.nodes.length > 0 && !data.nodes.some((n) => n.id === current)) {
          api.setCurrentNode(null)
          window.dispatchEvent(new CustomEvent<NodeChangedDetail>(NODE_CHANGED, { detail: { nodeId: data.local_id } }))
          toast.warning(t('cluster.tree.nodeGone', { id: current }))
        }
      })
      .catch(() => {
        // Keep the last good list and flag the failure. Substituting an empty
        // array (the old behaviour) rendered "leader unreachable" as "this
        // cluster has no nodes", with nothing on screen to say otherwise.
        setNodesError(true)
      })
  }, [t])

  // Load on mount + poll every 15s while visible (paused when the tab is hidden).
  // Status itself comes from Layout — this only refreshes the node list.
  useVisibleInterval(loadNodes, 15000)
  // A page that changed the cluster (a node removed, labels edited) says so;
  // the tree shows it now rather than at the next poll.
  useEffect(() => {
    window.addEventListener('sfpanel:cluster-changed', loadNodes)
    return () => window.removeEventListener('sfpanel:cluster-changed', loadNodes)
  }, [loadNodes])

  useEffect(() => {
    localStorage.setItem(TREE_COLLAPSE_KEY, String(treeCollapsed))
  }, [treeCollapsed])

  useEffect(() => {
    localStorage.setItem(MENU_COLLAPSE_KEY, String(menuCollapsed))
  }, [menuCollapsed])

  // Re-highlight the tree when another surface switches node (the phone's
  // drawer, a node list's "Open node", the fallback above). An event naming
  // its node is followed, the local one included. A bare event (or one naming
  // the datacenter) only says currentNode, and null there is ambiguous (local
  // node vs datacenter), so the tree's own selection is left alone rather
  // than guessed.
  useEffect(() => {
    const handler = (e: Event) => {
      const nid = (e as CustomEvent<NodeChangedDetail | undefined>).detail?.nodeId ?? api.currentNode
      if (!nid) return
      setSelection((prev) => {
        if (prev.type === 'node' && prev.nodeId === nid) return prev
        syncing.current = true
        return { type: 'node', nodeId: nid }
      })
    }
    window.addEventListener(NODE_CHANGED, handler)
    return () => window.removeEventListener(NODE_CHANGED, handler)
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
      onNodeChanged(null)
      navigate('/cluster/overview')
    } else {
      const targetNodeId = selection.nodeId
      const isLocal = targetNodeId === localId
      api.setCurrentNode(isLocal ? null : targetNodeId)
      onNodeChanged(targetNodeId)
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
    ? nodes.find(n => n.id === selection.nodeId)?.name || t('layout.cluster.unknownNode')
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
