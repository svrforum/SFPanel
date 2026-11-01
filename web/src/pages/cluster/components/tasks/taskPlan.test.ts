import { describe, expect, it } from 'vitest'
import type { TFunction } from 'i18next'
import type { PlanNode, TaskPlan } from '@/types/api'
import { confirmSteps, leftReasonText, planBlocked, planNeedsReview, runErrorText, verdictText } from './taskPlan'

// A translator that names the key it was asked for, and its values.
const t = ((key: string, opts?: Record<string, unknown>) => {
  const { defaultValue, ...values } = opts ?? {}
  if (key.startsWith('cluster.tasks.') || key.startsWith('services.')) return Object.keys(values).length ? `${key} ${JSON.stringify(values)}` : key
  return (defaultValue as string) ?? key
}) as unknown as TFunction

const pn = (id: string, over: Partial<PlanNode> = {}): PlanNode => ({ id, name: id, kind: 'agent', verdicts: [{ step: 0 }, { step: 1 }], ...over })
const plan = (over: Partial<TaskPlan> = {}): TaskPlan => ({ waves: [[pn('a-1'), pn('a-2')], [pn('m-1', { kind: 'manager', leader: true })]], plan_id: 'p', ...over })

describe('confirmSteps', () => {
  it('lists each step some node needs confirmed, once, in order', () => {
    const p = plan({
      waves: [
        [pn('a-1', { verdicts: [{ step: 0 }, { step: 1, needs_confirm: true, code: 'SERVICE_CONFIRM_REQUIRED' }] })],
        [pn('m-2', { verdicts: [{ step: 0, needs_confirm: true, code: 'COMPOSE_RISKY' }, { step: 1, needs_confirm: true }] })],
      ],
    })
    expect(confirmSteps(p)).toEqual([0, 1])
  })

  it('lists nothing for a clean plan or one with no node', () => {
    expect(confirmSteps(plan())).toEqual([])
    expect(confirmSteps(plan({ waves: null }))).toEqual([])
  })
})

describe('planNeedsReview', () => {
  it('starts a clean plan straight away', () => {
    expect(planNeedsReview(plan())).toBe(false)
  })

  it.each<[string, Partial<TaskPlan>]>([
    ['a step to confirm', { waves: [[pn('a-1', { verdicts: [{ step: 0, needs_confirm: true }] })]] }],
    ['a refused step', { waves: [[pn('a-1', { verdicts: [{ step: 0, refused: true, code: 'INVALID_PATH' }] })]] }],
    ['a Proxmox host skipping its reboot', { waves: [[pn('m-1', { verdicts: [{ step: 0, refused: true, code: 'TASK_REBOOT_PVE_HOST' }] })]] }],
    ['a node that could not be checked', { waves: [[pn('a-1', { verdicts: undefined, check_error: 'timeout' })]] }],
    ['a node left out', { left: [pn('a-3', { reason: 'offline', verdicts: undefined })] }],
    ['a manager not ready', { managers_not_ready: ['manager-3 (offline)'] }],
    ['no node at all', { waves: null }],
  ])('shows the preview for %s', (_what, over) => {
    expect(planNeedsReview(plan(over))).toBe(true)
  })
})

describe('planBlocked', () => {
  it('lets a plan with nodes and ready managers start', () => {
    expect(planBlocked(plan())).toBe(false)
    expect(planBlocked(plan({ managers_not_ready: [] }))).toBe(false)
  })

  it('blocks a start while a manager is not ready, or with no node', () => {
    expect(planBlocked(plan({ managers_not_ready: ['manager-3 (0.79.0)'] }))).toBe(true)
    expect(planBlocked(plan({ waves: null }))).toBe(true)
    expect(planBlocked(plan({ waves: [] }))).toBe(true)
  })
})

describe('verdictText', () => {
  it('reads a Proxmox host refusing its reboot as a skip', () => {
    expect(verdictText({ step: 0, refused: true, code: 'TASK_REBOOT_PVE_HOST', reason: 'This is a Proxmox VE host' }, t)).toBe('cluster.tasks.plan.skippedPve')
  })

  it('words the service codes without the unit the verdict does not carry', () => {
    expect(verdictText({ step: 1, needs_confirm: true, code: 'SERVICE_CONFIRM_REQUIRED', reason: 'Type x to confirm' }, t)).toBe('cluster.tasks.plan.verdict.serviceConfirm')
    expect(verdictText({ step: 1, refused: true, code: 'SERVICE_PROTECTED', reason: 'Refusing' }, t)).toBe('cluster.tasks.plan.verdict.serviceProtected')
  })

  it('words a code the error table knows through it', () => {
    expect(verdictText({ step: 0, refused: true, code: 'TASK_NODE_BUSY', reason: 'busy' }, t)).toBe('cluster.tasks.errors.nodeBusy')
  })

  it('shows the node reason for a code the table does not know', () => {
    expect(verdictText({ step: 0, refused: true, code: 'COMPOSE_FORBIDDEN', reason: 'privileged: true is not allowed' }, t)).toBe('privileged: true is not allowed')
  })
})

describe('leftReasonText', () => {
  it('words every reason the server gives, and keeps one it does not know', () => {
    for (const r of ['unknown', 'offline', 'outdated', 'unreachable', 'busy'] as const) {
      expect(leftReasonText(r, t)).toBe(`cluster.tasks.plan.leftReason.${r}`)
    }
    expect(leftReasonText('later' as never, t)).toBe('later')
  })
})

describe('runErrorText', () => {
  const err = (status: number, code: string | undefined, message: string, extra: Record<string, unknown> = {}) =>
    Object.assign(new Error(message), { status, code, ...extra })
  const names = (id: string) => ({ 'a-2': 'agent-02' })[id] ?? id

  it('keeps the line a TASK_INVALID names beside its worded text', () => {
    const e = err(400, 'TASK_INVALID', 'The task document has problems.', { serverMessage: 'line 7: unknown step kind' })
    expect(runErrorText(e, t, names)).toEqual({ text: 'The task document has problems.', server: 'line 7: unknown step kind' })
  })

  it('names the node a TASK_TARGET_BUSY gives by ID', () => {
    const e = err(409, 'TASK_TARGET_BUSY', 'held', { details: { node_id: 'a-2', run_id: 'r-7' } })
    expect(runErrorText(e, t, names)).toEqual({ text: 'cluster.tasks.setup.targetBusy {"node":"agent-02"}' })
  })

  it('reads a 503 and a code-less 413 as the task screen words them', () => {
    expect(runErrorText(err(503, 'INTERNAL_ERROR', 'No cluster leader available'), t, names)).toEqual({ text: 'cluster.tasks.errors.leaderBusy' })
    expect(runErrorText(err(413, undefined, 'HTTP 413'), t, names)).toEqual({ text: 'cluster.tasks.errors.tooLarge' })
  })
})
