import { describe, it, expect } from 'vitest'
import { modeStringToOctal } from './modeString'

describe('modeStringToOctal', () => {
  // The dialog opens pre-filled from this, so a misread turns "look at the
  // permissions" into "change them".
  it('reads the common modes', () => {
    expect(modeStringToOctal('-rw-r--r--')).toBe('644')
    expect(modeStringToOctal('-rwxr-xr-x')).toBe('755')
    expect(modeStringToOctal('-rw-------')).toBe('600')
    expect(modeStringToOctal('----------')).toBe('000')
    expect(modeStringToOctal('-rwxrwxrwx')).toBe('777')
    expect(modeStringToOctal('-rw-rw----')).toBe('660')
  })

  it('ignores the leading type character', () => {
    expect(modeStringToOctal('drwxr-xr-x')).toBe('755')
    expect(modeStringToOctal('lrwxrwxrwx')).toBe('777')
    expect(modeStringToOctal('crw-rw-rw-')).toBe('666')
  })

  // Lowercase s/t means the execute bit is also on; uppercase means it is not.
  // The special bit itself is neither read nor written here.
  it('handles setuid, setgid and sticky', () => {
    expect(modeStringToOctal('-rwsr-xr-x')).toBe('755')
    expect(modeStringToOctal('-rwSr--r--')).toBe('644')
    expect(modeStringToOctal('drwxrwxrwt')).toBe('777')
    expect(modeStringToOctal('drwxrwxrwT')).toBe('776')
  })

  // Go's FileMode.String() prefixes extra characters for some file types, and
  // a listing from a future version could carry something unexpected. Falling
  // back to a sane default beats writing NaN into a chmod.
  it('falls back rather than producing garbage', () => {
    expect(modeStringToOctal('')).toBe('644')
    expect(modeStringToOctal('short')).toBe('644')
    expect(modeStringToOctal('not-a-mode-string')).toBe('644')
  })
})
