import { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '@/lib/api'
import type { ClusterStatus, ClusterNode } from '@/types/api'
import TreePanel, { type TreeSelection } from './TreePanel'
import ContextMenu from './ContextMenu'

const TREE_COLLAPSE_KEY = 'sfpanel-cluster-tree-collapsed'
const MENU_COLLAPSE_KEY = 'sfpanel-cluster-menu-collapsed'
const SELECTION_KEY = 'sfpanel-cluster-selection'

interface ClusterSidebarProps {
  panelVersion: string
  onLogout: () => void
  onNodeChanged: () => void
}

export default function ClusterSidebar({ panelVersion, onLogout, onNodeChanged }: ClusterSidebarProps) {
  const navigate = useNavigate()
  const [clusterStatus, setClusterStatus] = useState<ClusterStatus | null>(null)
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [localId, setLocalId] = useState('')
  const [treeCollapsed, setTreeCollapsed] = useState(() => localStorage.getItem(TREE_COLLAPSE_KEY) === 'true')
  const [menuCollapsed, setMenuCollapsed] = useState(() => localStorage.getItem(MENU_COLLAPSE_KEY) === 'true')

  const [selection, setSelection] = useState<TreeSelection>(() => {
    try {
      const saved = localStorage.getItem(SELECTION_KEY)
      if (saved) return JSON.parse(saved)
    } catch {}
    return { type: 'datacenter' }
  })

  const initialLoad = useRef(true)

  const loadClusterData = useCallback(() => {
    Promise.all([
      api.getClusterStatus(true),
      api.getClusterNodes(true).catch(() => ({ nodes: [], local_id: '', is_leader: false })),
    ]).then(([status, nodesData]) => {
      setClusterStatus(status)
      setNodes(nodesData.nodes)
      setLocalId(nodesData.local_id)
    }).catch(() => {})
  }, [])

  useEffect(() => {
    loadClusterData()
    const interval = setInterval(loadClusterData, 15000)
    return () => clearInterval(interval)
  }, [loadClusterData])

  useEffect(() => {
    localStorage.setItem(TREE_COLLAPSE_KEY, String(treeCollapsed))
  }, [treeCollapsed])

  useEffect(() => {
    localStorage.setItem(MENU_COLLAPSE_KEY, String(menuCollapsed))
  }, [menuCollapsed])

  // Handle selection changes
  useEffect(() => {
    localStorage.setItem(SELECTION_KEY, JSON.stringify(selection))

    if (initialLoad.current) {
      initialLoad.current = false
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

  if (!clusterStatus?.enabled) return null

  const selectedNodeName = selection.type === 'node'
    ? nodes.find(n => n.id === selection.nodeId)?.name || 'Unknown'
    : ''

  return (
    <div className="flex h-full">
      <TreePanel
        clusterStatus={clusterStatus}
        nodes={nodes}
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
