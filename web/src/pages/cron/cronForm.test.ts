import { describe, expect, it } from 'vitest'
import { cronFormErrors } from './cronForm'

describe('cronFormErrors', () => {
  it('passes a five-field schedule and a command', () => {
    expect(cronFormErrors('*/5 * * * *', 'echo hi')).toEqual({})
    expect(cronFormErrors('  @daily ', 'echo hi')).toEqual({})
  })

  // Save stayed greyed out until both fields had something in them, with
  // nothing saying why; each field now says what it is missing.
  it('names each empty field', () => {
    expect(cronFormErrors('', '  ')).toEqual({ schedule: 'cron.scheduleRequired', command: 'cron.commandRequired' })
  })

  // The shape error was a toast, gone in seconds and away from the field.
  it('names a schedule of the wrong shape', () => {
    expect(cronFormErrors('every 5 minutes', 'echo hi')).toEqual({ schedule: 'cron.invalidSchedule' })
    expect(cronFormErrors('0 * *', 'echo hi')).toEqual({ schedule: 'cron.invalidSchedule' })
    expect(cronFormErrors('@sometimes', 'echo hi')).toEqual({ schedule: 'cron.invalidSchedule' })
  })
})
