import { describe, expect, it } from 'vitest'
import { pickLogSource } from './logSources'

describe('pickLogSource', () => {
  const S = [{ id: 'syslog', exists: true }, { id: 'firewall', exists: true }, { id: 'fail2ban', exists: false }]
  it('opens the source the address asks for', () => expect(pickLogSource(S, 'firewall')).toBe('firewall'))
  it('falls back to the first existing source for an unknown id', () => expect(pickLogSource(S, 'nope')).toBe('syslog'))
  it('does not open a source that is not on this server', () => expect(pickLogSource(S, 'fail2ban')).toBe('syslog'))
  it('is the first existing source without a request, and null with none', () => {
    expect(pickLogSource(S, null)).toBe('syslog')
    expect(pickLogSource([{ id: 'fail2ban', exists: false }], null)).toBeNull()
  })
})
