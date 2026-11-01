import type { TFunction } from 'i18next'
import type { TaskInput, TaskModel, TaskPackagesStep, TaskStep, TaskStepKind, TaskTargets } from '@/types/api'
import type { PickedTargets } from './TaskTargetPicker'

// The task editor's pure parts. The web has no YAML parser: the cards edit
// the model validate returns, and the server writes it back as YAML
// (POST /cluster/tasks/format). Format leaves a default out and reads a zero
// duration or batch as unset, but a boolean cannot be unset: one that
// defaults to true has to be carried as true, or it is saved as false.

/** Go's DefaultCommandTimeout and DefaultRebootWait: 10m, in nanoseconds. */
export const DEFAULT_WAIT = 600_000_000_000
/** A task's document, at most (tasks.MaxDocumentBytes). */
export const MAX_DOCUMENT_BYTES = 64 * 1024
/** Steps in a task, at most. */
export const MAX_STEPS = 50

/** A new step of a kind, with the settings it starts from. */
export function blankStep(kind: TaskStepKind): TaskStep {
  switch (kind) {
    case 'packages':
      return { kind, packages: { update: true } }
    case 'service':
      return { kind, service: { name: '', action: 'restart' } }
    case 'command':
      return { kind, command: { run: '', timeout: DEFAULT_WAIT } }
    case 'file':
      return { kind, file: { path: '', content: '' } }
    case 'stack':
      return { kind, stack: { name: '', compose: '', up: true } }
    case 'reboot':
      return { kind, reboot: { only_if_required: true, wait: DEFAULT_WAIT } }
  }
}

/** A new task: no steps yet, stopping on failure as Parse's default does. */
export function blankTask(): TaskModel {
  return { name: '', targets: {}, batch: 1, stop_on_failure: true, steps: [] }
}

// ------------------------------------------------------------- durations --

const UNIT_NS: Record<string, number> = {
  ns: 1,
  us: 1e3,
  'µs': 1e3, // U+00B5 micro sign
  'μs': 1e3, // U+03BC Greek mu
  ms: 1e6,
  s: 1e9,
  m: 60e9,
  h: 3600e9,
}

/** A fraction of a unit as Go writes it: 1.5, 250, no trailing zeros. */
function fraction(whole: number, rest: number, digits: number): string {
  if (rest === 0) return String(whole)
  return `${whole}.${String(rest).padStart(digits, '0').replace(/0+$/, '')}`
}

/**
 * A duration in nanoseconds as the server writes it: Go's Duration.String
 * with whole trailing units dropped (10m, 1h30m, 2h, 1m30s, 1.5s, 500ms).
 */
export function formatDuration(ns: number): string {
  if (ns === 0) return '0s'
  if (ns < 1e3) return `${ns}ns`
  if (ns < 1e6) return `${fraction(Math.floor(ns / 1e3), ns % 1e3, 3)}µs`
  if (ns < 1e9) return `${fraction(Math.floor(ns / 1e6), ns % 1e6, 6)}ms`
  const h = Math.floor(ns / 3600e9)
  const m = Math.floor((ns % 3600e9) / 60e9)
  const sNs = ns % 60e9
  const s = fraction(Math.floor(sNs / 1e9), sNs % 1e9, 9)
  let text = h > 0 ? `${h}h${m}m${s}s` : m > 0 ? `${m}m${s}s` : `${s}s`
  if (text.endsWith('m0s')) text = text.slice(0, -2)
  if (text.endsWith('h0m')) text = text.slice(0, -2)
  return text
}

/**
 * A duration typed in Go's syntax (90s, 10m, 1h30m, 1.5h), in nanoseconds;
 * null when Go would refuse it. "0" is zero. A sign is refused: no step
 * takes a negative wait.
 */
export function parseDuration(text: string): number | null {
  const s = text.trim()
  if (s === '0') return 0
  if (s === '') return null
  const segment = /(\d*)(?:\.(\d*))?(ns|us|µs|μs|ms|s|m|h)/y
  let at = 0
  let total = 0
  while (at < s.length) {
    segment.lastIndex = at
    const m = segment.exec(s)
    if (!m) return null
    const [, whole, frac = ''] = m
    if (whole === '' && frac === '') return null
    const unit = UNIT_NS[m[3]]
    total += Number(whole || '0') * unit
    if (frac !== '') total += Math.round(Number(`0.${frac}`) * unit)
    at = segment.lastIndex
  }
  return Math.round(total)
}

// ------------------------------------------------------------- comments --

/**
 * Whether a YAML document has a comment the form view would lose: a # at the
 * start of a line or after a space, outside a quoted scalar and outside a
 * block scalar (a script's own # lines are content, and survive). A # inside
 * a plain value with no space before it (echo "#", http://x/#top) is not one.
 * Written for the documents this editor sees; it does not parse YAML.
 */
export function hasYamlComments(yaml: string): boolean {
  let quote: '"' | "'" | null = null
  // A block scalar's parent column: its lines are those indented further.
  let block: number | null = null

  for (const line of yaml.split(/\r?\n/)) {
    if (block !== null) {
      const indent = line.length - line.trimStart().length
      if (line.trim() === '' || indent > block) continue
      block = null
    }
    let start = true // where a scalar may start: a quote here opens one
    let flow = 0
    let keyCol = -1
    for (let i = 0; i < line.length; i++) {
      const c = line[i]
      if (quote !== null) {
        if (quote === "'" && c === "'") {
          if (line[i + 1] === "'") i++
          else quote = null
        } else if (quote === '"' && c === '\\') i++
        else if (quote === '"' && c === '"') quote = null
        continue
      }
      if (c === ' ' || c === '\t') continue
      if (c === '#' && (i === 0 || line[i - 1] === ' ' || line[i - 1] === '\t')) return true
      const next = line[i + 1]
      const spaceAfter = next === undefined || next === ' ' || next === '\t'
      if (start && (c === '"' || c === "'")) {
        quote = c
        start = false
        continue
      }
      if (start && c === '-' && spaceAfter) continue
      if (keyCol < 0) keyCol = i
      if (c === ':' && (spaceAfter || flow > 0)) {
        start = true
        continue
      }
      if ((c === '[' || c === '{') && (start || flow > 0)) {
        flow++
        start = true
        continue
      }
      if ((c === ']' || c === '}') && flow > 0) {
        flow--
        continue
      }
      if (c === ',' && flow > 0) {
        start = true
        continue
      }
      if (start && flow === 0 && (c === '|' || c === '>')) {
        const indicator = /^[|>][0-9+-]*[ \t]*(#.*)?$/.exec(line.slice(i))
        if (indicator) {
          if (indicator[1]) return true
          block = Math.max(keyCol, 0)
          break
        }
      }
      start = false
    }
  }
  return false
}

// ---------------------------------------------------------------- names --

const INPUT_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/

/** An input name Parse takes: an identifier, and not node (node.name is the node's). */
export function inputNameValid(name: string): boolean {
  return INPUT_NAME.test(name) && name !== 'node'
}

/** The name a clone starts with: "<name> copy", "<name> 복사본". */
export function copyName(name: string, t: TFunction): string {
  return t('cluster.tasks.editor.copyName', { name })
}

/**
 * A model taken from a task, to be saved as a new one: named as a copy and,
 * for a built-in, in the screen's words (text gives a field's, as
 * builtinText does, or the fallback).
 */
export function cloneModel(model: TaskModel, copy: (name: string) => string, text: (field: string, fallback: string) => string): TaskModel {
  const description = text('description', model.description ?? '')
  const inputs = model.inputs?.map((input): TaskInput => {
    const label = text(`inputs.${input.name}`, input.label ?? '')
    return label ? { ...input, label } : input
  })
  const steps = model.steps.map((step, i): TaskStep => {
    const name = text(`steps.${i}`, step.name ?? '')
    return name ? { ...step, name } : step
  })
  return {
    ...model,
    name: copy(text('name', model.name)),
    ...(description ? { description } : {}),
    ...(inputs ? { inputs } : {}),
    steps,
  }
}

// ------------------------------------------------------------- packages --

// A reference stays one item: {{ packages }} expands to several on the node.
// A run of references and other characters is one name (nginx-{{ suffix }}),
// judged on the node once expanded.
const NAME_ITEM = /(?:\{\{.*?\}\}|[^\s,])+/g

/** Package names typed in one field: spaces, commas or lines between them. */
export function splitNames(text: string): string[] {
  return text.match(NAME_ITEM) ?? []
}

export function joinNames(list: string[] | undefined): string {
  return (list ?? []).join(' ')
}

/** What a packages step does besides update: at most one of these (Parse). */
export type PackagesAction = 'none' | 'upgradeAll' | 'upgrade' | 'install' | 'remove'

export function packagesAction(p: TaskPackagesStep): PackagesAction {
  if (p.upgrade_all) return 'upgradeAll'
  if (p.upgrade) return 'upgrade'
  if (p.install) return 'install'
  if (p.remove) return 'remove'
  return 'none'
}

/** The step doing another action; the names typed move with it. */
export function withPackagesAction(p: TaskPackagesStep, action: PackagesAction): TaskPackagesStep {
  const names = p.upgrade ?? p.install ?? p.remove ?? []
  const next: TaskPackagesStep = p.update ? { update: true } : {}
  if (action === 'upgradeAll') next.upgrade_all = true
  else if (action === 'upgrade') next.upgrade = names
  else if (action === 'install') next.install = names
  else if (action === 'remove') next.remove = names
  return next
}

/** The names of a packages step's action, under its own key. */
export function withPackageNames(p: TaskPackagesStep, names: string[]): TaskPackagesStep {
  const action = packagesAction(p)
  if (action === 'upgrade') return { ...p, upgrade: names }
  if (action === 'install') return { ...p, install: names }
  if (action === 'remove') return { ...p, remove: names }
  return p
}

// --------------------------------------------------------- the rest of it --

/** The picker's lists, from a task's targets. */
export function pickedTargets(targets: TaskTargets): PickedTargets {
  return { groups: targets.groups ?? [], kinds: targets.kinds ?? [], nodes: targets.nodes ?? [] }
}

/** A task's targets from the picker: an empty list is left out, not written as []. */
export function modelTargets(picked: PickedTargets): TaskTargets {
  const out: TaskTargets = {}
  if (picked.groups.length > 0) out.groups = picked.groups
  if (picked.kinds.length > 0) out.kinds = picked.kinds
  if (picked.nodes.length > 0) out.nodes = picked.nodes
  return out
}

/** A copy of list with item i moved by one place (-1 up, 1 down); unchanged at either end. */
export function moveItem<T>(list: T[], i: number, by: -1 | 1): T[] {
  const j = i + by
  if (i < 0 || i >= list.length || j < 0 || j >= list.length) return [...list]
  const next = [...list]
  ;[next[i], next[j]] = [next[j], next[i]]
  return next
}

/** Over the server's 64 KiB, counted in UTF-8 bytes as it counts them. */
export function documentTooLarge(yaml: string): boolean {
  return new TextEncoder().encode(yaml).length > MAX_DOCUMENT_BYTES
}

/** The kinds in the order the editor offers them. */
export const STEP_KINDS: TaskStepKind[] = ['command', 'packages', 'service', 'file', 'stack', 'reboot']

/** A step kind's name on screen; every key written out for locales.test. */
export function stepKindLabel(kind: TaskStepKind, t: TFunction): string {
  switch (kind) {
    case 'packages':
      return t('cluster.tasks.editor.kind.packages')
    case 'service':
      return t('cluster.tasks.editor.kind.service')
    case 'command':
      return t('cluster.tasks.editor.kind.command')
    case 'file':
      return t('cluster.tasks.editor.kind.file')
    case 'stack':
      return t('cluster.tasks.editor.kind.stack')
    case 'reboot':
      return t('cluster.tasks.editor.kind.reboot')
  }
}

/** obj with an optional text set: empty leaves the key out, as the server writes it. */
export function withOptional<T extends object, K extends keyof T>(obj: T, key: K, value: string): T {
  const next = { ...obj }
  if (value === '') delete next[key]
  else next[key] = value as T[K]
  return next
}

/** obj with an optional flag set: false leaves the key out. */
export function withFlag<T extends object, K extends keyof T>(obj: T, key: K, on: boolean): T {
  const next = { ...obj }
  if (on) next[key] = true as T[K]
  else delete next[key]
  return next
}

// A task's ID travels in a path escaped once (builtin/command → builtin%2Fcommand).
export const editPath = (id: string) => `/cluster/tasks/${encodeURIComponent(id)}/edit`
export const clonePath = (id: string) => `/cluster/tasks/new?from=${encodeURIComponent(id)}`
