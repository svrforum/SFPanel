// isPlausibleCronSchedule does a lightweight shape check so the most
// common typos ('every 5 minutes' typed into the field, three-field
// entries, etc.) get a clear error before round-tripping to the server.
// The server still validates strictly — this is just to short-circuit
// obvious garbage with a faster, localized error.
export function isPlausibleCronSchedule(s: string): boolean {
  if (s.startsWith('@')) {
    return /^@(reboot|yearly|annually|monthly|weekly|daily|midnight|hourly)$/.test(s)
  }
  const fields = s.split(/\s+/)
  return fields.length === 5
}

export interface CronFormErrors {
  schedule?: string
  command?: string
}

/** The locale key of what is wrong with each field, shown beside it; empty when the form can be sent. */
export function cronFormErrors(schedule: string, command: string): CronFormErrors {
  const errors: CronFormErrors = {}
  const s = schedule.trim()
  if (!s) errors.schedule = 'cron.scheduleRequired'
  else if (!isPlausibleCronSchedule(s)) errors.schedule = 'cron.invalidSchedule'
  if (!command.trim()) errors.command = 'cron.commandRequired'
  return errors
}
