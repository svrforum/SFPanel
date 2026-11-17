import { describe, expect, it } from 'vitest'
import { reachedOver } from './reachedOver'

describe('reachedOver', () => {
  it('matches the address the browser reached, with or without its prefix', () => {
    expect(reachedOver('100.64.0.1', ['100.64.0.1'])).toBe(true)
    expect(reachedOver('10.8.0.1', ['10.8.0.1/24, fd00:8::1/64'])).toBe(true)
    expect(reachedOver('fd00:8::1', ['10.8.0.1/24, fd00:8::1/64'])).toBe(true)
  })

  it('reads an IPv6 host the way a URL writes it', () => {
    expect(reachedOver('[fd7a:115c:a1e0::1]', ['fd7a:115c:a1e0::1'])).toBe(true)
  })

  it('counts a MagicDNS name as the tailnet', () => {
    expect(reachedOver('pve1.example.ts.net', ['100.64.0.1'], 'example.ts.net')).toBe(true)
    expect(reachedOver('PVE1.Example.ts.net', [], 'example.ts.net.')).toBe(true)
    expect(reachedOver('example.ts.net.evil.test', [], 'example.ts.net')).toBe(false)
  })

  it('says no for another address, an empty list, or no host', () => {
    expect(reachedOver('192.0.2.10', ['100.64.0.1'], 'example.ts.net')).toBe(false)
    expect(reachedOver('10.8.0.10', ['10.8.0.1/24'])).toBe(false)
    expect(reachedOver('localhost', [''])).toBe(false)
    expect(reachedOver(null, ['100.64.0.1'])).toBe(false)
  })
})

// The server leaves an interface's address out when its config has no Address
// line: the list then holds an undefined, and taking that interface down threw
// before any confirmation could be shown.
describe('reachedOver, with an interface that has no address', () => {
  it('says no instead of throwing', () => {
    expect(reachedOver('10.8.0.1', [undefined])).toBe(false)
    expect(reachedOver('10.8.0.1', [undefined, '10.8.0.1/24'])).toBe(true)
  })
})
