import type { TFunction } from 'i18next'
import type { ApiError } from '@/lib/api'
import { agentLabel } from '@/lib/codeMessages'
import { taskErrorMessage } from '@/lib/taskErrors'
import { warningText } from '@/lib/warnings'
import type { CheckVerdict, PlanLeftReason, PlanNode, RunStepRecord, TaskPlan } from '@/types/api'

// Reading a preview (POST /cluster/task-runs/plan): what needs confirming,
// and whether it may start.

/** A Proxmox VE host refuses a reboot step; a run skips it there. */
export const PVE_REBOOT_CODE = 'TASK_REBOOT_PVE_HOST'

/** A node that leaves an agent alone refuses its step; a run skips it there. */
export const AGENT_SKIP_CODE = 'SERVER_AGENT_SKIPPED'

export { agentLabel }

/** The nodes that run, wave by wave, flattened. waves is null with none. */
export function planNodes(plan: TaskPlan): PlanNode[] {
  return (plan.waves ?? []).flat()
}

/** A verdict that says anything: {step} alone means the step is fine. */
export function verdictSays(v: CheckVerdict): boolean {
  return !!(v.refused || v.needs_confirm || v.code)
}

/**
 * The steps (0-based, in order, once each) some node needs confirmed. One
 * confirmation covers the step on every node, as the start's confirmed does.
 */
export function confirmSteps(plan: TaskPlan): number[] {
  const steps = new Set<number>()
  for (const n of planNodes(plan)) {
    for (const v of n.verdicts ?? []) if (v.needs_confirm) steps.add(v.step)
  }
  return [...steps].sort((a, b) => a - b)
}

/**
 * Whether the start would be refused whatever the operator ticks: a manager
 * not ready (409 TASK_MANAGERS_NOT_READY), or no node left to run on.
 */
export function planBlocked(plan: TaskPlan): boolean {
  return (plan.managers_not_ready?.length ?? 0) > 0 || planNodes(plan).length === 0
}

const LEFT_REASON: Record<PlanLeftReason, (t: TFunction) => string> = {
  unknown: (t) => t('cluster.tasks.plan.leftReason.unknown'),
  offline: (t) => t('cluster.tasks.plan.leftReason.offline'),
  outdated: (t) => t('cluster.tasks.plan.leftReason.outdated'),
  unreachable: (t) => t('cluster.tasks.plan.leftReason.unreachable'),
  busy: (t) => t('cluster.tasks.plan.leftReason.busy'),
  step_unsupported: (t) => t('cluster.tasks.plan.leftReason.stepUnsupported'),
}

/** Why a node is left out, in the screen's words; a reason this screen does not know, as sent. */
export function leftReasonText(reason: PlanLeftReason | undefined, t: TFunction): string {
  if (!reason) return ''
  return Object.hasOwn(LEFT_REASON, reason) ? LEFT_REASON[reason](t) : reason
}

// Verdict codes whose shared wording does not fit a preview: the Services
// page's sentences name the unit through {{unit}}, which a verdict carries
// no details for, and the confirmation one asks to type the name where the
// preview has a checkbox.
const VERDICT_TEXT: Record<string, (t: TFunction) => string> = {
  SERVICE_CONFIRM_REQUIRED: (t) => t('cluster.tasks.plan.verdict.serviceConfirm'),
  SERVICE_PROTECTED: (t) => t('cluster.tasks.plan.verdict.serviceProtected'),
}

/**
 * Why a node leaves an agent alone (its details' why), in the screen's words,
 * with the facts the node gives: the version installed and the one set, the
 * OS. A reason whose facts are missing is worded without them; null for a
 * reason this screen does not know.
 */
export function agentSkipWhy(details: Record<string, string> | undefined, t: TFunction): string | null {
  const { installed, version, os } = details ?? {}
  switch (details?.why) {
    case 'os': return os ? t('cluster.tasks.plan.agentSkipWhy.osNamed', { os }) : t('cluster.tasks.plan.agentSkipWhy.os')
    case 'legacy': return t('cluster.tasks.plan.agentSkipWhy.legacy')
    case 'unmanaged': return t('cluster.tasks.plan.agentSkipWhy.unmanaged')
    case 'no_package': return os ? t('cluster.tasks.plan.agentSkipWhy.noPackageFor', { os }) : t('cluster.tasks.plan.agentSkipWhy.no_package')
    case 'newer': return installed && version ? t('cluster.tasks.plan.agentSkipWhy.newerThan', { installed, version }) : t('cluster.tasks.plan.agentSkipWhy.newer')
    default: return null
  }
}

// A refusal a run skips past, in words: a Proxmox host's reboot, an agent the
// node leaves alone; null for any other code, and for a reason with no words.
function skipText(code: string, details: Record<string, string> | undefined, t: TFunction): string | null {
  if (code === PVE_REBOOT_CODE) return t('cluster.tasks.plan.skippedPve')
  if (code !== AGENT_SKIP_CODE) return null
  const why = agentSkipWhy(details, t)
  return why === null ? null : t('cluster.tasks.plan.agentSkipped', { agent: agentLabel(details?.agent ?? ''), why })
}

/**
 * A node's verdict on a step, in words: a Proxmox host's refused reboot is
 * a skip, and so is an agent the node leaves alone, with the reason its
 * details give (the node's own sentence for a reason this screen does not
 * know); any other code is worded through the same table as an error
 * (CODE_MESSAGES), and a code with no entry shows the node's reason.
 */
export function verdictText(v: CheckVerdict, t: TFunction): string {
  const code = v.code ?? ''
  if (code === PVE_REBOOT_CODE || code === AGENT_SKIP_CODE) return skipText(code, v.details, t) ?? (v.reason || code)
  if (Object.hasOwn(VERDICT_TEXT, code)) return VERDICT_TEXT[code](t)
  return warningText({ code, message: v.reason || code }, t)
}

/**
 * A step's summary as a run recorded it: a step the node's own rule skipped
 * says why in the screen's words, as the preview did, from the code and
 * details the record keeps. A run recorded before it kept them, a reason with
 * no words, and every other summary read as the node wrote them.
 */
export function stepSummaryText(r: Pick<RunStepRecord, 'status' | 'summary' | 'code' | 'details'>, t: TFunction): string {
  const skip = r.status === 'skipped' && r.code ? skipText(r.code, r.details, t) : null
  return skip ?? r.summary ?? ''
}

/** A failed preview or start in words, and the server's own sentence when it carries more. */
export interface RunErrorText {
  text: string
  server?: string
}

/**
 * What a refused preview or start reads as. TASK_INVALID keeps the server's
 * sentence beside the worded one: on a preview or a start that sentence is
 * where the document's line is. TASK_TARGET_BUSY names its node, which the
 * server gives by ID. Everything else goes through taskErrorMessage (503,
 * a code-less 413, and the codes CODE_MESSAGES words).
 */
export function runErrorText(err: unknown, t: TFunction, nodeName: (id: string) => string): RunErrorText {
  const e = (err ?? {}) as ApiError
  if (e.code === 'TASK_INVALID') {
    const server = e.serverMessage && e.serverMessage !== e.message ? e.serverMessage : undefined
    return { text: e.message, server }
  }
  if (e.code === 'TASK_TARGET_BUSY' && e.details?.node_id) {
    return { text: t('cluster.tasks.setup.targetBusy', { node: nodeName(e.details.node_id) }) }
  }
  return { text: taskErrorMessage(err, t) }
}
