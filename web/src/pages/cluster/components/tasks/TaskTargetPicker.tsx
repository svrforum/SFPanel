import { useId, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Checkbox } from '@/components/ui/checkbox'
import { cn } from '@/lib/utils'
import { GROUP_LABEL, isAgent } from '@/lib/clusterNodes'
import type { ClusterNode } from '@/types/api'
import { countTargets } from './taskTargets'

/** A run's targets with every list present: what the picker edits and the request sends. */
export interface PickedTargets {
  groups: string[]
  kinds: ('manager' | 'agent')[]
  nodes: string[]
}

interface TaskTargetPickerProps {
  /** Every node of the cluster, by name; null while they load. */
  nodes: ClusterNode[] | null
  nodesError: string | null
  targets: PickedTargets
  onChange: (targets: PickedTargets) => void
  disabled?: boolean
}

const toggle = <T,>(list: T[], value: T): T[] => (list.includes(value) ? list.filter((x) => x !== value) : [...list, value])

function Chip({ pressed, onClick, disabled, children }: { pressed: boolean; onClick: () => void; disabled?: boolean; children: string }) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        'min-h-9 rounded-full border px-3 text-[12px] font-medium transition-colors break-all disabled:opacity-50',
        pressed ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-secondary/40 hover:bg-secondary',
      )}
    >
      {children}
    </button>
  )
}

// Which nodes a run takes, as the server reads it: the union of groups (the
// node label "group"), kinds and nodes picked one by one. The count beside it
// is the same union over the nodes this page knows; the preview is the
// judge (a node offline or busy is left out there).
export function TaskTargetPicker({ nodes, nodesError, targets, onChange, disabled }: TaskTargetPickerProps) {
  const { t } = useTranslation()
  const id = useId()

  // The groups nodes are in, plus any the task names that no node has (so it
  // can be taken off). The label is matched as the server matches it, untrimmed.
  const groups = useMemo(() => {
    const set = new Set<string>(targets.groups)
    for (const n of nodes ?? []) {
      const g = n.labels?.[GROUP_LABEL] ?? ''
      if (g !== '') set.add(g)
    }
    return [...set].sort((a, b) => a.localeCompare(b, undefined, { numeric: true }))
  }, [nodes, targets.groups])

  // An ID the task names that no node has stays listed, by its ID, until it is unticked.
  const known = new Set((nodes ?? []).map((n) => n.id))
  const unknownIds = targets.nodes.filter((n) => !known.has(n))
  const count = countTargets(nodes ?? [], targets)

  return (
    <section aria-labelledby={`${id}-targets`} className="space-y-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h3 id={`${id}-targets`} className="text-[14px] font-semibold">{t('cluster.tasks.setup.targets')}</h3>
        <span data-testid="task-target-count" className="text-[13px] font-medium text-primary">
          {t('cluster.tasks.setup.targetCount', { count })}
        </span>
      </div>

      <div className="space-y-1.5">
        <p id={`${id}-groups`} className="text-[12px] text-muted-foreground">{t('cluster.tasks.setup.groups')}</p>
        <div role="group" aria-labelledby={`${id}-groups`} className="flex flex-wrap gap-1.5">
          {groups.length === 0 && nodes !== null && (
            <span className="text-[12px] text-muted-foreground">{t('cluster.tasks.setup.noGroups')}</span>
          )}
          {groups.map((g) => (
            <Chip key={g} pressed={targets.groups.includes(g)} disabled={disabled} onClick={() => onChange({ ...targets, groups: toggle(targets.groups, g) })}>
              {g}
            </Chip>
          ))}
        </div>
      </div>

      <div className="space-y-1.5">
        <p id={`${id}-kinds`} className="text-[12px] text-muted-foreground">{t('cluster.tasks.setup.kinds')}</p>
        <div role="group" aria-labelledby={`${id}-kinds`} className="flex flex-wrap gap-1.5">
          <Chip pressed={targets.kinds.includes('manager')} disabled={disabled} onClick={() => onChange({ ...targets, kinds: toggle(targets.kinds, 'manager') })}>
            {t('cluster.tasks.setup.managers')}
          </Chip>
          <Chip pressed={targets.kinds.includes('agent')} disabled={disabled} onClick={() => onChange({ ...targets, kinds: toggle(targets.kinds, 'agent') })}>
            {t('cluster.tasks.setup.agents')}
          </Chip>
        </div>
      </div>

      <div className="space-y-1.5">
        <p id={`${id}-nodes`} className="text-[12px] text-muted-foreground">{t('cluster.tasks.setup.nodes')}</p>
        {nodesError && <p role="alert" className="text-[12px] text-destructive">{t('cluster.tasks.setup.nodesError')}: {nodesError}</p>}
        <ul aria-labelledby={`${id}-nodes`} className="max-h-56 overflow-y-auto rounded-xl border border-border divide-y divide-border">
          {(nodes ?? []).map((n) => {
            const box = `${id}-node-${n.id}`
            const group = n.labels?.[GROUP_LABEL] ?? ''
            return (
              <li key={n.id} className="flex items-center gap-2.5 px-3 py-2">
                <Checkbox
                  id={box}
                  checked={targets.nodes.includes(n.id)}
                  disabled={disabled}
                  onCheckedChange={() => onChange({ ...targets, nodes: toggle(targets.nodes, n.id) })}
                />
                <label htmlFor={box} className="min-w-0 flex-1 text-[13px] break-all cursor-pointer">{n.name}</label>
                <span className="shrink-0 text-[11px] text-muted-foreground">
                  {isAgent(n) ? t('cluster.kind.agent') : t('cluster.kind.manager')}
                  {group && ` · ${group}`}
                </span>
              </li>
            )
          })}
          {unknownIds.map((nodeId) => {
            const box = `${id}-node-${nodeId}`
            return (
              <li key={nodeId} className="flex items-center gap-2.5 px-3 py-2">
                <Checkbox id={box} checked disabled={disabled} onCheckedChange={() => onChange({ ...targets, nodes: toggle(targets.nodes, nodeId) })} />
                <label htmlFor={box} className="min-w-0 flex-1 text-[13px] font-mono break-all cursor-pointer">{nodeId}</label>
                <span className="shrink-0 text-[11px] text-muted-foreground">{t('cluster.tasks.plan.leftReason.unknown')}</span>
              </li>
            )
          })}
        </ul>
      </div>
    </section>
  )
}
