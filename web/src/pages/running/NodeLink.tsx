import type { ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { openNode } from '@/lib/nodeSelection'
import type { Target } from './targets'

/**
 * What leads to a page of one node. A page whose address says which node (a
 * stack of the cluster) is a plain link. One that shows the selected node
 * (the containers screen, the app store) has an address that does not say
 * which: from another node's tile it is a button that selects the node on
 * the way, the way the cluster tree does. As a link it could be opened in a
 * new tab, where it showed whichever node was selected last — another
 * server's containers, or its app store to install into.
 */
export function NodeLink({ target, nodeId, localId, className, title, children }: {
  target: Target
  nodeId: string
  localId: string
  className?: string
  /** The whole name, where the text shown may be cut. */
  title?: string
  children: ReactNode
}) {
  const navigate = useNavigate()
  if (!target.onNode) return <Link to={target.to} className={className} title={title}>{children}</Link>
  return (
    <button
      type="button"
      className={`cursor-pointer text-left ${className ?? ''}`}
      title={title}
      onClick={() => openNode(nodeId, localId, navigate, target.to)}
    >
      {children}
    </button>
  )
}
