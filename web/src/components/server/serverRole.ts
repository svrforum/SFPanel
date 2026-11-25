import type { TFunction } from 'i18next'
import { isAgent, nodeGroup } from '@/lib/clusterNodes'
import type { ClusterNode } from '@/types/api'

/** A server's role in the words the menu's head and the line above the page share: an agent with its group; '' before the node list has it. */
export function serverRole(node: ClusterNode | undefined, t: TFunction): string {
  if (!node) return ''
  if (!isAgent(node)) return t('cluster.kind.manager')
  const group = nodeGroup(node)
  return group ? t('layout.server.agentGroup', { group }) : t('cluster.kind.agent')
}
