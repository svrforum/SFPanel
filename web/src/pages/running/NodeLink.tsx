import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { hrefWithNode } from '@/lib/serverScope'
import type { Target } from './targets'

/**
 * What leads to a page of one node: a link whose address names the node when
 * the page shows one node's data, so it opens on that node here, in a new tab
 * or from a bookmark alike.
 */
export function NodeLink({ target, nodeId, className, title, children }: {
  target: Target
  nodeId: string
  className?: string
  /** The whole name, where the text shown may be cut. */
  title?: string
  children: ReactNode
}) {
  return (
    <Link to={target.onNode ? hrefWithNode(target.to, nodeId) : target.to} className={className} title={title}>
      {children}
    </Link>
  )
}
