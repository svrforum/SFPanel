import { describe, expect, it } from 'vitest'
import { bootLabelKey, matchesFilter, sortServices, stateLabelKey } from './serviceState'

const svc = (name: string, active_state: string, sub_state: string) => ({ name, active_state, sub_state })

describe('sortServices', () => {
  const list = [
    svc('b.service', 'active', 'running'),
    svc('z.service', 'failed', 'failed'),
    svc('A.service', 'inactive', 'dead'),
    svc('c.service', 'failed', 'failed'),
  ]

  // 231 units by name buried the two that failed somewhere in the middle.
  it('puts failed units first under All, each part by name', () => {
    expect(sortServices(list, 'all').map((s) => s.name)).toEqual(['c.service', 'z.service', 'A.service', 'b.service'])
  })

  it('sorts by name alone under any other filter', () => {
    expect(sortServices(list, 'running').map((s) => s.name)).toEqual(['A.service', 'b.service', 'c.service', 'z.service'])
  })

  it('leaves the list it was given alone', () => {
    sortServices(list, 'all')
    expect(list[0].name).toBe('b.service')
  })
})

describe('matchesFilter', () => {
  it('reads running as active and running, and the others by active state', () => {
    expect(matchesFilter(svc('a', 'active', 'running'), 'running')).toBe(true)
    expect(matchesFilter(svc('a', 'active', 'exited'), 'running')).toBe(false)
    expect(matchesFilter(svc('a', 'failed', 'failed'), 'failed')).toBe(true)
    expect(matchesFilter(svc('a', 'inactive', 'dead'), 'inactive')).toBe(true)
    expect(matchesFilter(svc('a', 'activating', 'start'), 'inactive')).toBe(false)
    expect(matchesFilter(svc('a', 'activating', 'start'), 'all')).toBe(true)
  })
})

describe('stateLabelKey', () => {
  // The badge said "running" under a filter that said "실행 중".
  it('words the sub-state systemd reports', () => {
    expect(stateLabelKey('active', 'running')).toBe('services.state.running')
    expect(stateLabelKey('active', 'exited')).toBe('services.state.exited')
    expect(stateLabelKey('inactive', 'dead')).toBe('services.state.dead')
    expect(stateLabelKey('failed', 'failed')).toBe('services.state.failed')
    expect(stateLabelKey('activating', 'auto-restart')).toBe('services.state.autoRestart')
    expect(stateLabelKey('activating', 'start-pre')).toBe('services.state.starting')
    expect(stateLabelKey('deactivating', 'stop-sigterm')).toBe('services.state.stopping')
    expect(stateLabelKey('reloading', 'reload')).toBe('services.state.reloading')
  })

  it('falls back to the active state when there is no sub-state', () => {
    expect(stateLabelKey('failed', '')).toBe('services.state.failed')
    expect(stateLabelKey('activating', '')).toBe('services.state.starting')
  })

  it('has no key for a word it does not know, so the raw word is shown', () => {
    expect(stateLabelKey('active', 'plugged')).toBeNull()
    expect(stateLabelKey('maintenance', '')).toBeNull()
  })
})

describe('bootLabelKey', () => {
  it('words the unit-file states list-unit-files reports', () => {
    expect(bootLabelKey('enabled')).toBe('services.bootState.enabled')
    expect(bootLabelKey('disabled')).toBe('services.bootState.disabled')
    expect(bootLabelKey('static')).toBe('services.bootState.static')
    expect(bootLabelKey('masked')).toBe('services.bootState.masked')
    expect(bootLabelKey('masked-runtime')).toBe('services.bootState.masked')
    expect(bootLabelKey('unknown')).toBe('services.bootState.unknown')
  })

  it('has no key for a word it does not know', () => {
    expect(bootLabelKey('something-new')).toBeNull()
  })
})
