import { useEffect, useRef } from 'react'

/**
 * Whether the component is still on screen. A write that answers after the
 * operator has moved on (another task, another menu) still lands, but
 * nothing more is started from it and the page is not moved to it.
 */
export function useMounted() {
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  return mounted
}
