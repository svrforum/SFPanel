import { describe, it, expect } from 'vitest'
import { gatewayField } from './gatewayField'

describe('gatewayField', () => {
  it('omits a blank gateway the dialog could not fill', () => {
    // The case that deleted default routes: an MTU-only edit on a host whose
    // netplan the panel cannot read.
    expect(gatewayField('static', '', false)).toBeUndefined()
  })

  it('clears when the operator emptied a field they could see', () => {
    expect(gatewayField('static', '', true)).toBe('')
  })

  it('sends what was typed, whether or not the saved value was known', () => {
    expect(gatewayField('static', ' 10.0.0.1 ', false)).toBe('10.0.0.1')
    expect(gatewayField('static', '10.0.0.1', true)).toBe('10.0.0.1')
  })

  it('clears on DHCP, where static config goes anyway', () => {
    expect(gatewayField('dhcp', '10.0.0.1', true)).toBe('')
    expect(gatewayField('dhcp', '', false)).toBe('')
  })
})
