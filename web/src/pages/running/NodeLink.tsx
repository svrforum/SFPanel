import type { ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { openNode } from '@/lib/nodeSelection'
import type { Target } from './targets'

/**
 * A link to a page of one node. When that page reads the selected node, the
 * node is selected on the way, the way the cluster tree selects it; any other
 * link is a plain one.
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
    <Link
      to={target.to}
      className={className}
      title={title}
      onClick={(e) => {
        e.preventDefault()
        openNode(nodeId, localId, navigate, target.to)
      }}
    >
      {children}
    </Link>
  )
}
