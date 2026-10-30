import { describe, expect, it } from 'vitest'
import { isRiskyRefusal, migrationRiskLines, riskLines } from './composeRisk'

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

  it('lists the findings of a migration\'s compose-risky warning', () => {
    const lead = 'the stack asks for host access that runs on the target as it is: '
    const warnings = [
      { code: 'large-transfer', message: 'estimated transfer is large' },
      { code: 'compose-risky', message: lead + 'service "web" binds sensitive host path "/var/run/docker.sock" at "/var/run/docker.sock", service "web" sets privileged: true' },
    ]
    expect(migrationRiskLines(warnings)).toEqual([
      'service "web" binds sensitive host path "/var/run/docker.sock" at "/var/run/docker.sock"',
      'service "web" sets privileged: true',
    ])
    // A comma inside a quoted path is part of the finding.
    expect(migrationRiskLines([{ code: 'compose-risky', message: lead + 'service "a" binds sensitive host path "/srv/a, b" at "/x", service "a" sets privileged: true' }])).toEqual([
      'service "a" binds sensitive host path "/srv/a, b" at "/x"',
      'service "a" sets privileged: true',
    ])
    expect(migrationRiskLines([{ code: 'large-transfer', message: 'x' }])).toEqual([])
    expect(migrationRiskLines(undefined)).toEqual([])
  })
})
