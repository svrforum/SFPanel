import type { ClusterUpdateEvent } from '@/types/api'

export interface UpdateRunState {
  overall?: 'started' | 'complete' | 'error'; target?: string; total: number; done: number; failed: number
  wave?: number; waves?: number; waveNodes: string[]; stopReason?: string; leaderRestarting: boolean
  nodes: { id: string; name: string; step: string; message: string }[]   // in the order first seen
}

// A node counts as done only once it was asked to update and came back. The
// leader also lists nodes it skipped (offline) or did not need to touch
// ("Already on …", step online without updating first); those are shown, not
// counted — total_nodes leaves them out too. An older leader reports a node
// done as "complete".
const DONE_STEPS = new Set(['online', 'complete'])

/** Folds the cluster-update stream into what the progress card draws. */
export function reduceUpdateEvents(events: ClusterUpdateEvent[]): UpdateRunState {
  const s: UpdateRunState = { total: 0, done: 0, failed: 0, waveNodes: [], leaderRestarting: false, nodes: [] }
  const byId = new Map<string, UpdateRunState['nodes'][number]>()
  const asked = new Set<string>()
  let reported: { updated?: number; failed?: number } | undefined

  for (const e of events) {
    if (e.overall) {
      s.overall = e.overall
      if (e.overall === 'started') {
        s.total = e.total_nodes ?? 0
        s.target = e.target
        if (typeof e.waves === 'number') s.waves = e.waves
      } else {
        // The run's own verdict outranks a count read off the steps.
        reported = { updated: e.updated, failed: e.failed }
        if (e.overall === 'error') s.stopReason = e.message
        if (e.leader_restarting) s.leaderRestarting = true
      }
      continue
    }
    if (typeof e.wave === 'number') {
      s.wave = e.wave
      if (typeof e.waves === 'number') s.waves = e.waves
      s.waveNodes = e.nodes ?? []
      continue
    }
    if (!e.node_id) continue
    const step = e.step ?? ''
    if (step === 'updating') asked.add(e.node_id)
    const entry = { id: e.node_id, name: e.node_name || e.node_id, step, message: e.message ?? '' }
    const seen = byId.get(e.node_id)
    if (seen) Object.assign(seen, entry)
    else {
      byId.set(e.node_id, entry)
      s.nodes.push(entry)
    }
  }

  s.done = s.nodes.filter((n) => asked.has(n.id) && DONE_STEPS.has(n.step)).length
  s.failed = s.nodes.filter((n) => n.step === 'error').length
  if (typeof reported?.updated === 'number') s.done = reported.updated
  if (typeof reported?.failed === 'number') s.failed = reported.failed
  return s
}
