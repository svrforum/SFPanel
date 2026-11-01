import type { ClusterNode, TaskModel, TaskTargets } from '@/types/api'
import { GROUP_LABEL, isAgent } from '@/lib/clusterNodes'

// What a run will take and what it may carry, judged in the browser before
// the server is asked: the count beside the target picker and the input
// limits. The server stays the judge (the preview's waves and left are the
// truth); these only spare a round trip.

/**
 * Whether a run's targets take n: its group label, its kind or its ID, as
 * the server's targeted (plan.go) reads them. The label is matched exactly,
 * untrimmed, as the server does; a node with no kind is a manager.
 */
export function targetedBy(n: ClusterNode, targets: TaskTargets): boolean {
  const group = n.labels?.[GROUP_LABEL] ?? ''
  if (group !== '' && (targets.groups ?? []).includes(group)) return true
  const kind = isAgent(n) ? 'agent' : 'manager'
  return (targets.kinds ?? []).includes(kind) || (targets.nodes ?? []).includes(n.id)
}

/** How many of nodes the targets take: their union, each node once. */
export function countTargets(nodes: ClusterNode[], targets: TaskTargets): number {
  return nodes.filter((n) => targetedBy(n, targets)).length
}

// The server's caps on a run's values (handler.go maxInputBytes,
// maxInputsBytes), in UTF-8 bytes: each value, and all of them together.
export const MAX_INPUT_BYTES = 16 << 10
export const MAX_INPUTS_BYTES = 64 << 10

export type InputProblemReason = 'required' | 'tooLarge' | 'totalTooLarge'

/** A value the start would be refused for; name is '*' for the total. */
export interface InputProblem {
  name: string
  reason: InputProblemReason
}

const encoder = new TextEncoder()
const bytes = (s: string) => encoder.encode(s).length

/**
 * The values a run would be refused for: a required input with no value and
 * no default (an empty value takes the default, render.go), a value over 16
 * KiB, and the values over 64 KiB together. Only the task's own inputs count:
 * the server drops any other key before it measures.
 */
export function checkInputs(task: TaskModel, values: Record<string, string>): InputProblem[] {
  const problems: InputProblem[] = []
  let total = 0
  for (const input of task.inputs ?? []) {
    const v = values[input.name] ?? ''
    const size = bytes(v)
    total += size
    if (v === '' && !input.default && input.required) problems.push({ name: input.name, reason: 'required' })
    else if (size > MAX_INPUT_BYTES) problems.push({ name: input.name, reason: 'tooLarge' })
  }
  if (total > MAX_INPUTS_BYTES) problems.push({ name: '*', reason: 'totalTooLarge' })
  return problems
}

/**
 * The inputs a preview and its start send: the task's own, those given a
 * value, as typed. An empty value is left out, which the server reads the
 * same way (the default applies), and the start must send exactly what the
 * preview did or its plan_id no longer matches.
 */
export function runInputs(task: TaskModel, values: Record<string, string>): Record<string, string> | undefined {
  const out: Record<string, string> = {}
  for (const input of task.inputs ?? []) {
    const v = values[input.name] ?? ''
    if (v !== '') out[input.name] = v
  }
  return Object.keys(out).length > 0 ? out : undefined
}
