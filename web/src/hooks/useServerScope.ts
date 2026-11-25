import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { api } from '@/lib/api'
import { NODE_CHANGED, type NodeChangedDetail } from '@/lib/nodeSelection'
import { carriesNode, hrefWithNode, LOCAL_ID_KEY, nodeParam, scopeKind, searchWithNode, storeValue, urlValue } from '@/lib/serverScope'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import type { ClusterNode, ClusterStatus } from '@/types/api'

/** The server the shell's pages act on, and the cluster's list of servers. */
export interface ServerScope {
  /** The shell's belief about cluster mode; null before any answer. */
  clusterEnabled: boolean | null
  /** This page load's latest cluster status; null until one arrives. */
  status: ClusterStatus | null
  /** The last node list that came back: a failed ask keeps it. */
  nodes: ClusterNode[]
  /** The last ask for the node list failed. */
  nodesError: boolean
  /** The attached server's id: the status's, else the one a previous load kept, else ''. */
  localId: string
  /** The stored choice (api.currentNode) as state; null is the attached server. */
  selected: string | null
  /** The id of the server the pages act on; '' while the attached server's is unknown. */
  currentId: string
  current: ClusterNode | undefined
  /** Its name in the node list, else the hostname for the attached server, else the id. */
  currentName: string
  /** The server the address or the store named that the node list does not have; the attached one is shown instead. Kept until a pick or an address names another server. */
  missing: { id: string; name: string } | null
  /** Act on another server: the page stays and its address names the server, except on an all-servers page, which opens the server's dashboard. */
  switchTo: (id: string) => void
  /** Ask for the node list now. */
  reloadNodes: () => void
}

const noop = () => {}

export const ServerScopeContext = createContext<ServerScope>({
  clusterEnabled: null,
  status: null,
  nodes: [],
  nodesError: false,
  localId: '',
  selected: null,
  currentId: '',
  current: undefined,
  currentName: '',
  missing: null,
  switchTo: noop,
  reloadNodes: noop,
})

export function useServerScope(): ServerScope {
  return useContext(ServerScopeContext)
}

/**
 * The shell's server scope, owned by Layout and handed down through
 * ServerScopeContext. In a cluster the address names the server a page acts
 * on, the attached one by its own id, and the store (api.currentNode) keeps
 * null for the attached one. A new address is taken into the store before any
 * page asks for anything; the store is written back into an address that does
 * not name it, always by replace, so the two cannot chase each other.
 */
export function useServerScopeState({ clusterEnabled, status, hostname }: {
  clusterEnabled: boolean | null
  status: ClusterStatus | null
  hostname: string
}): ServerScope {
  const location = useLocation()
  const navigate = useNavigate()
  const [cachedLocalId] = useState(() => localStorage.getItem(LOCAL_ID_KEY) ?? '')
  const localId = status?.local_id || cachedLocalId
  const [selected, setSelected] = useState<string | null>(() => api.currentNode)
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [nodesError, setNodesError] = useState(false)
  const [missing, setMissing] = useState<ServerScope['missing']>(null)
  // The list before this one: a server that just left is named by the name
  // it had there, not by its id.
  const lastNodes = useRef<ClusterNode[]>([])

  // Every change of server goes through here. The event remounts the page
  // (Layout) and tells the other surfaces which server is shown.
  const select = useCallback((target: string | null) => {
    api.setCurrentNode(target)
    setSelected(target)
    window.dispatchEvent(new CustomEvent<NodeChangedDetail>(NODE_CHANGED, { detail: { nodeId: target ?? localId } }))
  }, [localId])

  const listed = clusterEnabled === true
  const loadNodes = useCallback(() => {
    if (!listed) return
    api.getClusterNodes()
      .then((data) => {
        setNodes(data.nodes)
        setNodesError(false)
        const before = lastNodes.current
        lastNodes.current = data.nodes
        // The server shown is not in the cluster (removed, or a stale
        // address): every page would be told "Node not found". Show the
        // attached one instead. An empty list says nothing about who is in it.
        const current = api.currentNode
        if (current && data.nodes.length > 0 && !data.nodes.some((n) => n.id === current)) {
          setMissing({ id: current, name: before.find((n) => n.id === current)?.name || current })
          select(null)
        }
      })
      // Keep the last good list: a leader that cannot be reached is not a
      // cluster without servers, and nothing is judged missing from a list
      // that did not come.
      .catch(() => setNodesError(true))
  }, [listed, select])

  useVisibleInterval(loadNodes, 15000)
  // A page that changed the cluster says so; the list follows now rather than
  // at the next poll.
  useEffect(() => {
    window.addEventListener('sfpanel:cluster-changed', loadNodes)
    return () => window.removeEventListener('sfpanel:cluster-changed', loadNodes)
  }, [loadNodes])
  // The interval asks once on mount; a cluster first known after that asks now.
  const listedBefore = useRef(listed)
  useEffect(() => {
    if (listed && !listedBefore.current) loadNodes()
    listedBefore.current = listed
  }, [listed, loadNodes])

  // A switch made elsewhere (a node list's "Open node").
  useEffect(() => {
    const follow = () => setSelected(api.currentNode)
    window.addEventListener(NODE_CHANGED, follow)
    return () => window.removeEventListener(NODE_CHANGED, follow)
  }, [])

  // The address the shell itself wrote last, until the router shows it. It is
  // never read back as a new address: the router shows a location in a
  // transition, so the store it was written from can move on in between (a
  // node list that drops the server answers first), and reading it back
  // would undo that move.
  const written = useRef<string | null>(null)
  const shown = useRef(location)

  // Address → store. Only a new address, or the attached server's id becoming
  // known, is followed: a server picked while the address still names the
  // last one is not undone. A new address is a new location object, not a new
  // location.key: every entry the router did not make itself (the first one,
  // one another script pushed) has the key "default", so Back from one to
  // another would go unseen. A layout effect, so the page's own effects in the
  // same commit already ask the server the address names. A single server
  // takes no ?node= from the address. The state set here mirrors the address,
  // which lives outside React.
  useLayoutEffect(() => {
    if (shown.current !== location) {
      shown.current = location
      const own = written.current === location.pathname + location.search
      written.current = null
      if (own) return
    }
    if (clusterEnabled === false) return
    const id = nodeParam(location.search)
    if (id === null) return
    const target = storeValue(id, localId || null)
    if (target === api.currentNode) return
    // A server the cluster lacked was the last address's; this one names another.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setMissing(null)
    select(target)
  }, [location, localId]) // eslint-disable-line react-hooks/exhaustive-deps

  // Store → address: a server page whose address does not name the stored
  // server (an old bookmark, a menu link, a redirect) gets it back by replace.
  // Nothing else writes the address.
  useEffect(() => {
    if (clusterEnabled !== true || !localId) return
    if (!carriesNode(location.pathname, location.search)) return
    const want = urlValue(api.currentNode, localId)
    if (nodeParam(location.search) === want) return
    const search = searchWithNode(location.search, want)
    written.current = location.pathname + search
    navigate({ pathname: location.pathname, search, hash: location.hash }, { replace: true, state: location.state })
  }, [location, clusterEnabled, localId, selected, navigate])

  const switchTo = useCallback((id: string) => {
    setMissing(null)
    const target = storeValue(id, localId || null)
    const changed = target !== api.currentNode
    // Picked on a page about all servers: open that server's dashboard, as a
    // move the operator meant (a new history entry).
    if (scopeKind(location.pathname, location.search) === 'fleet') {
      if (changed) select(target)
      const to = hrefWithNode('/dashboard', id)
      written.current = to
      navigate(to)
      return
    }
    if (changed) select(target)
    if (carriesNode(location.pathname, location.search)) {
      const search = searchWithNode(location.search, id)
      written.current = location.pathname + search
      navigate({ pathname: location.pathname, search, hash: location.hash }, { replace: true, state: location.state })
    }
  }, [localId, location, navigate, select])

  const currentId = selected ?? localId
  const current = nodes.find((n) => n.id === currentId)
  const currentName = current?.name || (selected === null ? hostname : selected) || ''

  return useMemo<ServerScope>(() => ({
    clusterEnabled,
    status,
    nodes,
    nodesError,
    localId,
    selected,
    currentId,
    current,
    currentName,
    missing,
    switchTo,
    reloadNodes: loadNodes,
  }), [clusterEnabled, status, nodes, nodesError, localId, selected, currentId, current, currentName, missing, switchTo, loadNodes])
}
