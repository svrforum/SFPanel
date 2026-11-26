import { describe, expect, it } from 'vitest'
import type { TFunction } from 'i18next'
import type { PlanNode, TaskPlan } from '@/types/api'
import { agentLabel, agentSkipWhy, confirmSteps, leftReasonText, planBlocked, runErrorText, stepSummaryText, verdictText } from './taskPlan'

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

  // The node's own sentence goes along as {{serverMessage}}, for a message
  // that quotes it; this one does not, and reads by its key alone.
  it('words a code the error table knows through it', () => {
    expect(verdictText({ step: 0, refused: true, code: 'TASK_NODE_BUSY', reason: 'busy' }, t)).toBe('cluster.tasks.errors.nodeBusy {"serverMessage":"busy"}')
  })

  it('shows the node reason for a code the table does not know', () => {
    expect(verdictText({ step: 0, refused: true, code: 'COMPOSE_FORBIDDEN', reason: 'privileged: true is not allowed' }, t)).toBe('privileged: true is not allowed')
  })

  it('words a skipped agent with its reason', () =>
    expect(verdictText({ step: 0, refused: true, code: 'SERVER_AGENT_SKIPPED', reason: 'skipped', details: { agent: 'zabbix', why: 'legacy' } }, t))
      .toBe('cluster.tasks.plan.agentSkipped {"agent":"Zabbix","why":"cluster.tasks.plan.agentSkipWhy.legacy"}'))

  it('words every reason a node skips an agent for', () => {
    for (const why of ['os', 'legacy', 'unmanaged', 'no_package', 'newer']) {
      const text = verdictText({ step: 0, refused: true, code: 'SERVER_AGENT_SKIPPED', reason: 'skipped: …', details: { agent: 'wazuh', why } }, t)
      expect(text, why).toBe(`cluster.tasks.plan.agentSkipped {"agent":"Wazuh","why":"cluster.tasks.plan.agentSkipWhy.${why}"}`)
    }
  })

  // A newer node may skip for a reason this screen has no words for: its own
  // sentence says why.
  it('shows the node reason for a skip it has no words for', () => {
    const v = { step: 0, refused: true, code: 'SERVER_AGENT_SKIPPED', reason: 'skipped: the disk is full', details: { agent: 'zabbix', why: 'disk' } }
    expect(verdictText(v, t)).toBe('skipped: the disk is full')
    expect(verdictText({ ...v, details: undefined }, t)).toBe('skipped: the disk is full')
  })
})

describe('agentSkipWhy', () => {
  // 4.14.11 against 4.14.10 is the case the version rule is for: the operator
  // sees which two versions, not only that one is newer.
  it('names the version installed and the one set', () =>
    expect(agentSkipWhy({ agent: 'wazuh', why: 'newer', installed: '4.14.11-1', version: '4.14.10' }, t))
      .toBe('cluster.tasks.plan.agentSkipWhy.newerThan {"installed":"4.14.11-1","version":"4.14.10"}'))

  it('names the OS', () => {
    expect(agentSkipWhy({ agent: 'zabbix', why: 'os', os: 'Fedora Linux 41' }, t)).toBe('cluster.tasks.plan.agentSkipWhy.osNamed {"os":"Fedora Linux 41"}')
    expect(agentSkipWhy({ agent: 'zabbix', why: 'no_package', os: 'Ubuntu 26.10' }, t)).toBe('cluster.tasks.plan.agentSkipWhy.noPackageFor {"os":"Ubuntu 26.10"}')
  })

  // A value left out is not printed as its placeholder: the sentence without it.
  it('words a reason whose facts are missing without them', () => {
    expect(agentSkipWhy({ agent: 'wazuh', why: 'newer', installed: '4.14.11-1' }, t)).toBe('cluster.tasks.plan.agentSkipWhy.newer')
    expect(agentSkipWhy({ agent: 'zabbix', why: 'os' }, t)).toBe('cluster.tasks.plan.agentSkipWhy.os')
    expect(agentSkipWhy({ agent: 'zabbix', why: 'no_package', os: '' }, t)).toBe('cluster.tasks.plan.agentSkipWhy.no_package')
  })

  it('has no words for a reason it does not know, or none', () => {
    expect(agentSkipWhy({ agent: 'zabbix', why: 'disk' }, t)).toBeNull()
    expect(agentSkipWhy(undefined, t)).toBeNull()
  })
})

// A step as a run recorded it: one a node's own rule skipped says why in the
// operator's language, from the code and details the run now keeps.
describe('stepSummaryText', () => {
  const summary = 'skipped: the old Zabbix agent (zabbix-agent) is here; only Zabbix Agent 2 is set up, and not beside it'

  it('words a skipped agent step with its reason', () =>
    expect(stepSummaryText({ status: 'skipped', summary, code: 'SERVER_AGENT_SKIPPED', details: { agent: 'zabbix', why: 'legacy' } }, t))
      .toBe('cluster.tasks.plan.agentSkipped {"agent":"Zabbix","why":"cluster.tasks.plan.agentSkipWhy.legacy"}'))

  it('words a skipped agent step with the versions it names', () =>
    expect(stepSummaryText({ status: 'skipped', summary: 'skipped: …', code: 'SERVER_AGENT_SKIPPED', details: { agent: 'wazuh', why: 'newer', installed: '4.14.11-1', version: '4.14.10' } }, t))
      .toBe(`cluster.tasks.plan.agentSkipped {"agent":"Wazuh","why":${JSON.stringify('cluster.tasks.plan.agentSkipWhy.newerThan {"installed":"4.14.11-1","version":"4.14.10"}')}}`))

  it("words a Proxmox host's skipped reboot", () =>
    expect(stepSummaryText({ status: 'skipped', summary: 'skipped: a Proxmox VE host is not rebooted by a task', code: 'TASK_REBOOT_PVE_HOST' }, t)).toBe('cluster.tasks.plan.skippedPve'))

  // A run recorded before the code was kept, and a reason a newer node gives.
  it("keeps the node's sentence when it has no code, or a reason it has no words for", () => {
    expect(stepSummaryText({ status: 'skipped', summary }, t)).toBe(summary)
    expect(stepSummaryText({ status: 'skipped', summary: 'skipped: the disk is full', code: 'SERVER_AGENT_SKIPPED', details: { agent: 'zabbix', why: 'disk' } }, t)).toBe('skipped: the disk is full')
  })

  it('keeps any other summary as the node wrote it, and has none to show for none', () => {
    expect(stepSummaryText({ status: 'failed', summary: 'exit 1' }, t)).toBe('exit 1')
    expect(stepSummaryText({ status: 'succeeded' }, t)).toBe('')
  })

  // The code is the node's refusal of a skipped step; on any other it is not a skip.
  it('reads the code only on a skipped step', () =>
    expect(stepSummaryText({ status: 'failed', summary: 'exit 1', code: 'TASK_REBOOT_PVE_HOST' }, t)).toBe('exit 1'))
})

describe('agentLabel', () => {
  it('names the two agents as their vendors do, and any other as sent', () => {
    expect(agentLabel('zabbix')).toBe('Zabbix')
    expect(agentLabel('wazuh')).toBe('Wazuh')
    expect(agentLabel('nagios')).toBe('nagios')
  })
})

describe('leftReasonText', () => {
  it('words every reason the server gives, and keeps one it does not know', () => {
    for (const r of ['unknown', 'offline', 'outdated', 'unreachable', 'busy'] as const) {
      expect(leftReasonText(r, t)).toBe(`cluster.tasks.plan.leftReason.${r}`)
    }
    expect(leftReasonText('later' as never, t)).toBe('later')
  })

  it('names a node left out for a step it does not run', () =>
    expect(leftReasonText('step_unsupported', t)).toBe('cluster.tasks.plan.leftReason.stepUnsupported'))
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
