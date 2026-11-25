import { describe, it, expect } from 'vitest'
import { raidTone, raidNeedsAttention } from './raid'

// mdadm --detail joins its state words with commas ("clean, degraded"), so a
// whole-string match left a broken array grey.
describe('raidTone', () => {
  it('reads each word of an mdadm state', () => {
    expect(['clean', 'active'].map(raidTone)).toEqual(['ok', 'ok'])
    expect(['clean, degraded', 'active, degraded, recovering', 'clean, resyncing'].map(raidTone)).toEqual(['warn', 'warn', 'warn'])
    expect(['clean, FAILED', 'inactive'].map(raidTone)).toEqual(['bad', 'bad'])
    expect(raidTone('')).toBe('other')
  })
})

describe('raidNeedsAttention', () => {
  it('raises a degraded, failed or inactive array, or one with a failed member', () => {
    for (const a of [{ state: 'clean, degraded', failed: 0 }, { state: 'active, degraded, recovering', failed: 0 }, { state: 'inactive', failed: 0 }, { state: 'clean', failed: 1 }])
      expect(raidNeedsAttention(a)).toBe(true)
  })

  it('leaves a clean or resyncing array alone', () => {
    expect(raidNeedsAttention({ state: 'clean', failed: 0 })).toBe(false)
    expect(raidNeedsAttention({ state: 'clean, resyncing', failed: 0 })).toBe(false)
  })
})
