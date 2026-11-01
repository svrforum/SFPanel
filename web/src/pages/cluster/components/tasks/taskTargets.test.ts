import { describe, expect, it } from 'vitest'
import type { ClusterNode, TaskModel } from '@/types/api'
import { checkInputs, countTargets, runInputs } from './taskTargets'

const node = (id: string, kind: 'manager' | 'agent' | undefined, group?: string): ClusterNode => ({
  id,
  name: id,
  kind,
  role: kind === 'agent' ? '' : 'voter',
  status: 'online',
  api_address: '192.0.2.1:3628',
  grpc_address: '192.0.2.1:3629',
  labels: group === undefined ? undefined : { group },
  joined_at: '',
  last_seen: '',
})

const NODES = [
  node('m-1', 'manager'),
  node('m-2', 'manager', 'web'),
  // An older leader sends no kind: a manager.
  node('m-3', undefined),
  node('a-1', 'agent', 'web'),
  node('a-2', 'agent', 'web'),
  node('a-3', 'agent', 'db'),
  node('a-4', 'agent'),
]

describe('countTargets', () => {
  it('counts nothing for no targets', () => {
    expect(countTargets(NODES, {})).toBe(0)
    expect(countTargets(NODES, { groups: [], kinds: [], nodes: [] })).toBe(0)
  })

  it('counts a group by its label', () => {
    expect(countTargets(NODES, { groups: ['web'] })).toBe(3)
    expect(countTargets(NODES, { groups: ['web', 'db'] })).toBe(4)
  })

  it('counts a kind, reading a node with no kind as a manager', () => {
    expect(countTargets(NODES, { kinds: ['manager'] })).toBe(3)
    expect(countTargets(NODES, { kinds: ['agent'] })).toBe(4)
  })

  it('counts a node in several targets once', () => {
    // a-1 is in web, an agent and named; m-2 is in web and a manager.
    expect(countTargets(NODES, { groups: ['web'], kinds: ['manager'], nodes: ['a-1', 'm-2'] })).toBe(5)
    expect(countTargets(NODES, { kinds: ['manager', 'agent'], groups: ['web'] })).toBe(7)
  })

  it('counts no node for a group no node is in, or an ID no node has', () => {
    expect(countTargets(NODES, { groups: ['nope'] })).toBe(0)
    expect(countTargets(NODES, { nodes: ['gone'] })).toBe(0)
  })

  it('matches the group label exactly, as the server does', () => {
    expect(countTargets([node('x', 'agent', ' web')], { groups: ['web'] })).toBe(0)
    expect(countTargets([node('x', 'agent', 'Web')], { groups: ['web'] })).toBe(0)
  })
})

const task = (inputs: TaskModel['inputs']): TaskModel => ({
  name: 'T',
  inputs,
  targets: {},
  batch: 1,
  stop_on_failure: true,
  steps: [{ kind: 'command', command: { run: 'true', timeout: 0 } }],
})

const KiB = 1024

describe('checkInputs', () => {
  it('passes values within the limits', () => {
    const t = task([{ name: 'a', required: true }, { name: 'b' }])
    expect(checkInputs(t, { a: 'x', b: '' })).toEqual([])
  })

  it('names a required input left empty', () => {
    const t = task([{ name: 'a', required: true }, { name: 'b', required: true }])
    expect(checkInputs(t, { a: 'x' })).toEqual([{ name: 'b', reason: 'required' }])
    expect(checkInputs(t, { a: '', b: 'y' })).toEqual([{ name: 'a', reason: 'required' }])
  })

  it('lets a required input with a default go empty', () => {
    const t = task([{ name: 'a', required: true, default: 'nginx.service' }])
    expect(checkInputs(t, {})).toEqual([])
    expect(checkInputs(t, { a: '' })).toEqual([])
  })

  it('refuses a value over 16 KiB, counted in bytes', () => {
    const t = task([{ name: 'a' }, { name: 'b' }])
    expect(checkInputs(t, { a: 'x'.repeat(16 * KiB) })).toEqual([])
    expect(checkInputs(t, { a: 'x'.repeat(16 * KiB + 1) })).toEqual([{ name: 'a', reason: 'tooLarge' }])
    // 가 is three bytes in UTF-8: 5462 of them is 16386 bytes.
    expect(checkInputs(t, { b: '가'.repeat(5462) })).toEqual([{ name: 'b', reason: 'tooLarge' }])
  })

  it('refuses values over 64 KiB together, as input *', () => {
    const names = ['a', 'b', 'c', 'd', 'e']
    const t = task(names.map((name) => ({ name })))
    const full = Object.fromEntries(names.slice(0, 4).map((n) => [n, 'x'.repeat(16 * KiB)]))
    expect(checkInputs(t, full)).toEqual([])
    expect(checkInputs(t, { ...full, e: 'x' })).toEqual([{ name: '*', reason: 'totalTooLarge' }])
  })

  it('counts only the task own inputs, as the server keeps only those', () => {
    const t = task([{ name: 'a' }])
    expect(checkInputs(t, { a: 'x', other: 'y'.repeat(80 * KiB) })).toEqual([])
  })

  it('checks nothing for a task with no inputs', () => {
    expect(checkInputs(task(undefined), {})).toEqual([])
  })
})

describe('runInputs', () => {
  it('sends the task own inputs that have a value, as typed', () => {
    const t = task([{ name: 'a' }, { name: 'b', default: 'd' }, { name: 'c' }])
    expect(runInputs(t, { a: ' x ', b: '', other: 'y' })).toEqual({ a: ' x ' })
  })
})
