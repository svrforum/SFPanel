import { describe, expect, it } from 'vitest'
import { parseSettingNumber } from './settingValue'

describe('parseSettingNumber', () => {
  it('takes a whole number at or above the floor', () => {
    expect(parseSettingNumber('45', 0)).toBe(45); expect(parseSettingNumber('0', 0)).toBe(0); expect(parseSettingNumber(' 2048 ', 1)).toBe(2048)
  })
  it('refuses one below the floor', () => { expect(parseSettingNumber('0', 1)).toBeNull(); expect(parseSettingNumber('-5', 0)).toBeNull() })
  // The server refuses a value over its ceiling (max_upload_size 1024 MB,
  // terminal_timeout 1440 minutes); the field says so before sending it.
  it('takes the ceiling and refuses one above it', () => {
    expect(parseSettingNumber('1024', 1, 1024)).toBe(1024); expect(parseSettingNumber('1440', 0, 1440)).toBe(1440)
    expect(parseSettingNumber('1025', 1, 1024)).toBeNull(); expect(parseSettingNumber('1441', 0, 1440)).toBeNull()
  })
  it('refuses what is not a whole number, instead of cutting it', () => {
    for (const raw of ['', 'abc', '1.5', '12abc']) expect(parseSettingNumber(raw, 0)).toBeNull()
  })
})
