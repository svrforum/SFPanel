import { useEffect, useState } from 'react'

/**
 * Date.now(), drawn again every intervalMs: for a countdown or an "updated
 * 5s ago" that has to move while nothing else on the page changes.
 */
export function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(timer)
  }, [intervalMs])
  return now
}
