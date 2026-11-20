import type { ReactNode } from 'react'

export type HeadingLevel = 1 | 2 | 3 | 4

/**
 * A heading at the level its place on the page calls for: the board is a page
 * of its own at /running and a tab under the cluster page's heading at
 * /cluster/running, and its headings stay in order in both.
 */
export function Heading({ level, id, className, children }: { level: HeadingLevel; id?: string; className?: string; children: ReactNode }) {
  const Tag = `h${level}` as const
  return <Tag id={id} className={className}>{children}</Tag>
}
