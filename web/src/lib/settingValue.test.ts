import { describe, expect, it } from 'vitest'
import { parseSettingNumber } from './settingValue'

describe('parseSettingNumber', () => {
  it('takes a whole number at or above the floor', () => {
    expect(parseSettingNumber('45', 0)).toBe(45); expect(parseSettingNumber('0', 0)).toBe(0); expect(parseSettingNumber(' 2048 ', 1)).toBe(2048)
  })
  it('refuses one below the floor', () => { expect(parseSettingNumber('0', 1)).toBeNull(); expect(parseSettingNumber('-5', 0)).toBeNull() })
  it('refuses what is not a whole number, instead of cutting it', () => {
    for (const raw of ['', 'abc', '1.5', '12abc']) expect(parseSettingNumber(raw, 0)).toBeNull()
  })
})
