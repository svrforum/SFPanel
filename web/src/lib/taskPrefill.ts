import type { TaskModel } from '@/types/api'

/**
 * What a group task's run setup is opened with from another screen: the
 * servers to run on, and values for the task's inputs, by input name.
 */
export interface RunPrefill {
  nodes: string[]
  inputs: Record<string, string>
}

export const NO_PREFILL: RunPrefill = { nodes: [], inputs: {} }

const INPUT_PREFIX = 'input.'

/**
 * Reads a run setup's address: ?nodes=<id,id> (trimmed, empty and repeated
 * IDs dropped, in order) and input.<name>=<value> (the first value of a
 * name). Any other key is not the run setup's.
 */
export function parseRunPrefill(search: string): RunPrefill {
  const params = new URLSearchParams(search)
  const nodes: string[] = []
  for (const id of (params.get('nodes') ?? '').split(',')) {
    const trimmed = id.trim()
    if (trimmed !== '' && !nodes.includes(trimmed)) nodes.push(trimmed)
  }
  const inputs: Record<string, string> = {}
  for (const [key, value] of params) {
    if (!key.startsWith(INPUT_PREFIX)) continue
    const name = key.slice(INPUT_PREFIX.length)
    if (name !== '' && !Object.hasOwn(inputs, name)) inputs[name] = value
  }
  return { nodes, inputs }
}

/**
 * The address of a task's run setup, filled with servers and values: a
 * built-in's ID (builtin/command) travels escaped once, and an address with
 * nothing to fill has no query.
 */
export function taskRunHref(taskId: string, prefill: { nodes?: string[]; inputs?: Record<string, string> } = {}): string {
  const params = new URLSearchParams()
  if (prefill.nodes && prefill.nodes.length > 0) params.set('nodes', prefill.nodes.join(','))
  for (const [name, value] of Object.entries(prefill.inputs ?? {})) params.set(`${INPUT_PREFIX}${name}`, value)
  const query = params.toString()
  return `/cluster/tasks/${encodeURIComponent(taskId)}${query ? `?${query}` : ''}`
}

/** A query part's key as URLSearchParams reads it; a malformed escape is left as written. */
function keyOf(part: string): string {
  const raw = part.split('=', 1)[0].replace(/\+/g, ' ')
  try {
    return decodeURIComponent(raw)
  } catch {
    return raw
  }
}

/**
 * The query without its input.<name> values, every other part kept as it was
 * written (?nodes=a-1,m-2 stays unescaped); '' when nothing is left.
 */
export function withoutInputs(search: string): string {
  const kept = search.replace(/^\?/, '').split('&').filter((part) => part !== '' && !keyOf(part).startsWith(INPUT_PREFIX))
  return kept.length > 0 ? `?${kept.join('&')}` : ''
}

/** The values the task asks for, out of those an address gave: any other name is dropped. */
export function prefilledValues(model: TaskModel, inputs: Record<string, string>): Record<string, string> {
  const values: Record<string, string> = {}
  for (const input of model.inputs ?? []) {
    if (Object.hasOwn(inputs, input.name)) values[input.name] = inputs[input.name]
  }
  return values
}
