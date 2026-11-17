import { describe, expect, it } from 'vitest'
import { reniceStart, reniceToSend, statusLabelKey } from './processView'

// Opening the renice dialog and pressing its button sent 19: the list carried
// getpriority's 20 - nice, the dialog started from that 20, and the clamp
// turned it into the lowest priority there is. The dialog sends only a value
// the operator picked.
describe('reniceStart', () => {
  it('starts from the process own nice value', () => {
    expect(reniceStart(0)).toBe(0)
    expect(reniceStart(-4)).toBe(-4)
    expect(reniceStart(19)).toBe(19)
  })

  // An older node still reports 20 - nice: 20 for nice 0, 24 for -4.
  it('starts from nothing when the number is not a nice value', () => {
    expect(reniceStart(20)).toBeNull()
    expect(reniceStart(24)).toBeNull()
    expect(reniceStart(-21)).toBeNull()
  })
})

describe('reniceToSend', () => {
  it('sends nothing before a value is picked', () => {
    expect(reniceToSend(null, 0)).toBeNull()
  })

  it('sends nothing for the value the process already has', () => {
    expect(reniceToSend(0, 0)).toBeNull()
    expect(reniceToSend(-4, -4)).toBeNull()
  })

  it('sends the value picked', () => {
    expect(reniceToSend(5, 0)).toBe(5)
    expect(reniceToSend(-20, 0)).toBe(-20)
    // A process listed as 20 by an older node: 0 is a choice, not a no-op.
    expect(reniceToSend(0, 20)).toBe(0)
  })

  it('never sends a value outside -20..19', () => {
    expect(reniceToSend(25, 0)).toBe(19)
    expect(reniceToSend(-40, 0)).toBe(-20)
  })
})

// The server sends gopsutil's words — sleep, stop, blocked — and the page
// looked for sleeping and stopped, so the state column read "sleep" in Korean.
describe('statusLabelKey', () => {
  it.each([
    ['running', 'processes.running'],
    ['sleep', 'processes.sleeping'],
    ['stop', 'processes.stopped'],
    ['idle', 'processes.idle'],
    ['zombie', 'processes.zombie'],
    ['blocked', 'processes.blocked'],
  ])('%s reads as %s', (status, key) => {
    expect(statusLabelKey(status)).toBe(key)
  })

  it('has no key for a state it does not know', () => {
    expect(statusLabelKey('sleeping')).toBeNull()
    expect(statusLabelKey('')).toBeNull()
  })
})
