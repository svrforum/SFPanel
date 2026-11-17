// What the process page shows and sends, kept apart from the page so the
// rules can be tested.

const NICE_MIN = -20
const NICE_MAX = 19

export const clampNice = (n: number) => Math.max(NICE_MIN, Math.min(NICE_MAX, Math.round(n)))

const isNice = (n: number) => Number.isInteger(n) && n >= NICE_MIN && n <= NICE_MAX

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
