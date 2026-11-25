import { describe, expect, it } from 'vitest'
import { limitCanRise, limitFrom, overLimit } from './uploadLimit'

const MIB = 1024 * 1024

describe('limitFrom', () => {
  it('reads a positive whole number of MB', () => { expect(limitFrom('512')).toBe(512); expect(limitFrom(' 1024 ')).toBe(1024) })
  it('has none for a value a server would not use', () => {
    for (const value of [undefined, '', '0', '-1', '1.5', 'abc']) expect(limitFrom(value)).toBeNull()
  })
})

describe('overLimit', () => {
  it('is over only past the limit, and never for an unknown one', () => {
    expect(overLimit(MIB + 1, 1)).toBe(true)
    expect(overLimit(MIB, 1)).toBe(false)
    expect(overLimit(10 * 1024 * MIB, null)).toBe(false)
  })
})

describe('limitCanRise', () => {
  it('offers a higher limit below the most a server takes', () => {
    expect(limitCanRise(512, [600 * MIB])).toBe(true)
    expect(limitCanRise(null, [600 * MIB])).toBe(true)
  })
  it('does not at the most a server takes', () => { expect(limitCanRise(1024, [16])).toBe(false) })
  it('does not for files no limit lets through, unless one of them fits', () => {
    expect(limitCanRise(512, [2048 * MIB])).toBe(false)
    expect(limitCanRise(null, [1024 * MIB])).toBe(false)
    expect(limitCanRise(512, [2048 * MIB, 600 * MIB])).toBe(true)
  })
})
