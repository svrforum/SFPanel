// What the process page shows and sends, kept apart from the page so the
// rules can be tested.

const NICE_MIN = -20
const NICE_MAX = 19

export const clampNice = (n: number) => Math.max(NICE_MIN, Math.min(NICE_MAX, Math.round(n)))

const isNice = (n: number) => Number.isInteger(n) && n >= NICE_MIN && n <= NICE_MAX

// The nice values of a process list as the server sent it. A node still on the
// server before the fix lists getpriority's raw 20 - nice for every process: an
// ordinary one reads 20, a kernel thread 40. The fixed server never sends more
// than 19, so one such number says the whole list is raw, and it is read back
// here rather than shown (a process at nice 5 read "15", and picking 10 to
// raise it lowered it). A raw 0 is a read that failed: it stays 0.
export function nicesAsListed(listed: number[]): number[] {
  if (!listed.some((n) => n > NICE_MAX)) return listed
  return listed.map((n) => (n >= 1 && n <= 40 ? 20 - n : 0))
}

// The value the renice dialog starts from: the process's own nice value, or
// nothing when the listed number is not one. A node older than the fix still
// lists getpriority's 20 - nice (20 for an ordinary process), and starting from
// that number is how pressing the button sent 19.
export function reniceStart(current: number): number | null {
  return isNice(current) ? current : null
}

// The nice value the dialog may send: the one the operator picked, kept to
// -20..19, and nothing while none is picked or the pick is what the process
// already has.
export function reniceToSend(value: number | null, current: number): number | null {
  if (value === null) return null
  const nice = clampNice(value)
  return nice === current ? null : nice
}

// The server sends gopsutil's state words (process.go: running, sleep, stop,
// idle, zombie, blocked for Linux's D). A state with no key shows as sent.
const STATUS_KEYS: Record<string, string> = {
  running: 'processes.running',
  sleep: 'processes.sleeping',
  stop: 'processes.stopped',
  idle: 'processes.idle',
  zombie: 'processes.zombie',
  blocked: 'processes.blocked',
}

export function statusLabelKey(status: string): string | null {
  return Object.hasOwn(STATUS_KEYS, status) ? STATUS_KEYS[status] : null
}
