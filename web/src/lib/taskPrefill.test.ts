import { describe, expect, it } from 'vitest'
import type { TaskModel } from '@/types/api'
import { NO_PREFILL, parseRunPrefill, prefilledValues, taskRunHref, withoutInputs } from './taskPrefill'

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

describe('withoutInputs', () => {
  it('drops every value and keeps the rest as written', () => {
    expect(withoutInputs('?nodes=a-1,m-2&input.command=echo%20s3cret&keep=1&input.x=')).toBe('?nodes=a-1,m-2&keep=1')
    expect(parseRunPrefill(withoutInputs('?nodes=a-1&input.command=uptime'))).toEqual({ nodes: ['a-1'], inputs: {} })
  })

  it('drops a value whose name is escaped, as the address reads it', () =>
    expect(withoutInputs('?input%2Ecommand=uptime&nodes=a-1')).toBe('?nodes=a-1'))

  it('leaves no query when nothing else is there', () => {
    expect(withoutInputs('?input.command=uptime')).toBe('')
    expect(withoutInputs('')).toBe('')
  })

  it('keeps a part whose name cannot be read', () =>
    expect(withoutInputs('?%E0%A4=1&input.a=b')).toBe('?%E0%A4=1'))
})
