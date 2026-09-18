import { describe, expect, it } from 'vitest'
import { isRiskyRefusal, riskLines } from './composeRisk'

const refusal = (code: string, message: string) => Object.assign(new Error(message), { code, status: 400 })

describe('composeRisk', () => {
  it('offers the confirm only for a refusal the operator can lift', () => {
    expect(isRiskyRefusal(refusal('COMPOSE_RISKY', 'service "dozzle" binds sensitive host path "/var/run/docker.sock"'))).toBe(true)
    // The panel's own secrets: no dialog, because no answer changes it.
    expect(isRiskyRefusal(refusal('COMPOSE_FORBIDDEN', 'service "x" binds sensitive host path "/etc/sfpanel"'))).toBe(false)
    expect(isRiskyRefusal(refusal('COMPOSE_ERROR', 'boom'))).toBe(false)
    expect(isRiskyRefusal(new Error('network'))).toBe(false)
    expect(isRiskyRefusal(null)).toBe(false)
  })

  it('splits the server\'s joined message into one line per finding', () => {
    const err = refusal('COMPOSE_RISKY', 'service "a" sets privileged: true; service "a" binds sensitive host path "/var/run/docker.sock"')
    expect(riskLines(err)).toEqual([
      'service "a" sets privileged: true',
      'service "a" binds sensitive host path "/var/run/docker.sock"',
    ])
    expect(riskLines(refusal('COMPOSE_RISKY', 'one finding'))).toEqual(['one finding'])
    expect(riskLines(new Error('x'))).toEqual([])
  })
})
