import { describe, expect, it } from 'vitest'
import { vpnSubnets } from './wgSubnets'

describe('vpnSubnets', () => {
  it('turns interface addresses into the networks they serve', () => {
    expect(vpnSubnets('10.8.0.1/24')).toBe('10.8.0.0/24')
    expect(vpnSubnets('172.16.5.9/20')).toBe('172.16.0.0/20')
    expect(vpnSubnets('10.8.0.1/24, fd00:8::1/64')).toBe('10.8.0.0/24, fd00:8::/64')
    expect(vpnSubnets('2001:db8:1:2:3:4:5:6/48')).toBe('2001:db8:1::/48')
  })

  it('keeps a single address whole', () => {
    expect(vpnSubnets('10.8.0.1')).toBe('10.8.0.1/32')
    expect(vpnSubnets('fd00::1/128')).toBe('fd00::1/128')
  })

  it('leaves out what does not parse, and gives nothing for no address', () => {
    expect(vpnSubnets('')).toBe('')
    expect(vpnSubnets('wg-host/24, 10.8.0.1/24')).toBe('10.8.0.0/24')
    expect(vpnSubnets('10.8.0.1/33')).toBe('')
    expect(vpnSubnets('1:2:3::4::5/64')).toBe('')
  })
})
