import type { TFunction } from 'i18next'
import type { TreeSection } from '@/lib/clusterNodes'

/** A section's heading, shared by the sidebar tree and the phone's node picker. A group is its own name. */
export function sectionLabel(s: TreeSection, t: TFunction): string {
  return { managers: t('cluster.tree.managers'), ungrouped: t('cluster.tree.ungrouped'), group: s.group ?? '' }[s.kind]
}
