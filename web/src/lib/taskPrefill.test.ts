import { describe, expect, it } from 'vitest'
import type { TaskModel } from '@/types/api'
import { NO_PREFILL, parseRunPrefill, prefilledValues, taskRunHref } from './taskPrefill'

describe('parseRunPrefill', () => {
  it('reads servers and values from the address', () =>
    expect(parseRunPrefill('?nodes=a-1,a-3&input.command=uptime')).toEqual({ nodes: ['a-1', 'a-3'], inputs: { command: 'uptime' } }))

  it('drops empty and repeated servers, keeps their order', () =>
    expect(parseRunPrefill('?nodes=,a-2,, a-1 ,a-2').nodes).toEqual(['a-2', 'a-1']))

  it('takes nothing that is not a server list or a named input', () =>
    expect(parseRunPrefill('?node=x&input.=y&inputs=z&nodesx=1')).toEqual(NO_PREFILL))

  it('keeps the first of two values for one input', () =>
    expect(parseRunPrefill('?input.command=uptime&input.command=reboot').inputs).toEqual({ command: 'uptime' }))

  it('round-trips any value through the address', () => {
    const inputs = { command: 'df -h /srv && echo "100% done" # c\nnext', service: 'getty@tty1.service', packages: 'nginx, curl' }
    const url = new URL(taskRunHref('builtin/command', { nodes: ['a-1', 'm-2'], inputs }), 'http://x')
    expect(url.pathname).toBe('/cluster/tasks/builtin%2Fcommand')
    expect(parseRunPrefill(url.search)).toEqual({ nodes: ['a-1', 'm-2'], inputs })
  })
})

describe('taskRunHref', () => {
  it('has no query when there is nothing to fill', () => {
    expect(taskRunHref('builtin/reboot')).toBe('/cluster/tasks/builtin%2Freboot')
    expect(taskRunHref('builtin/reboot', { nodes: [], inputs: {} })).toBe('/cluster/tasks/builtin%2Freboot')
  })
})

describe('prefilledValues', () => {
  it("fills only the task's own inputs", () =>
    expect(prefilledValues({ inputs: [{ name: 'command' }] } as unknown as TaskModel, { command: 'uptime', nope: '1' })).toEqual({ command: 'uptime' }))

  it('fills nothing for a task that asks for nothing', () =>
    expect(prefilledValues({} as unknown as TaskModel, { command: 'uptime' })).toEqual({}))
})
