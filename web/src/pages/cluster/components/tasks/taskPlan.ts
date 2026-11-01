import type { TFunction } from 'i18next'
import type { ApiError } from '@/lib/api'
import { waitingLine } from '@/lib/clusterRotation'
import { taskErrorMessage } from '@/lib/taskErrors'
import { warningText } from '@/lib/warnings'
import type { CheckVerdict, PlanLeftReason, PlanNode, TaskPlan } from '@/types/api'

// Reading a preview (POST /cluster/task-runs/plan): what needs confirming,
// whether the operator has to see it at all, and whether it may start.

/** A Proxmox VE host refuses a reboot step; a run skips it there. */
export const PVE_REBOOT_CODE = 'TASK_REBOOT_PVE_HOST'

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
 * Whether [Run] has to show the preview before it starts: a step to confirm
 * or refused (a Proxmox host's skipped reboot too), a node that could not be
 * checked or is left out, a manager not ready, or no node at all. A clean
 * plan starts straight away.
 */
export function planNeedsReview(plan: TaskPlan): boolean {
  if (planBlocked(plan) || (plan.left?.length ?? 0) > 0) return true
  return planNodes(plan).some((n) => !!n.check_error || (n.verdicts ?? []).some(verdictSays))
}

/**
 * Whether the start would be refused whatever the operator ticks: a manager
 * not ready (409 TASK_MANAGERS_NOT_READY), or no node left to run on.
 */
export function planBlocked(plan: TaskPlan): boolean {
  return (plan.managers_not_ready?.length ?? 0) > 0 || planNodes(plan).length === 0
}

/**
 * A manager a start would wait for, "<name> (<reason>)" as the leader writes
 * it (TasksReady), in the screen's words: offline and not heard from as the
 * rotation cards word them, a bare version as one that does not know group
 * tasks. A line this screen does not know, as sent.
 */
export function notReadyText(line: string, t: TFunction): string {
  return waitingLine(line, t, (name, version) => t('cluster.tasks.plan.notReadyVersion', { name, version }))
}

const LEFT_REASON: Record<PlanLeftReason, (t: TFunction) => string> = {
  unknown: (t) => t('cluster.tasks.plan.leftReason.unknown'),
  offline: (t) => t('cluster.tasks.plan.leftReason.offline'),
  outdated: (t) => t('cluster.tasks.plan.leftReason.outdated'),
  unreachable: (t) => t('cluster.tasks.plan.leftReason.unreachable'),
  busy: (t) => t('cluster.tasks.plan.leftReason.busy'),
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
 * A node's verdict on a step, in words: a Proxmox host's refused reboot is
 * a skip; any other code is worded through the same table as an error
 * (CODE_MESSAGES), and a code with no entry shows the node's reason.
 */
export function verdictText(v: CheckVerdict, t: TFunction): string {
  const code = v.code ?? ''
  if (code === PVE_REBOOT_CODE) return t('cluster.tasks.plan.skippedPve')
  if (Object.hasOwn(VERDICT_TEXT, code)) return VERDICT_TEXT[code](t)
  return warningText({ code, message: v.reason || code }, t)
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
