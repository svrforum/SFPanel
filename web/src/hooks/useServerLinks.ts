import { useMemo } from 'react'
import { useServerScope } from '@/hooks/useServerScope'
import { fleetRunHrefFor, type FleetRunHref } from '@/lib/serverLinks'

/** Group task links for the server on screen; null where there are none to offer (a single server). */
export function useFleetRunHref(): FleetRunHref | null {
  const { clusterEnabled, currentId } = useServerScope()
  return useMemo(() => fleetRunHrefFor(clusterEnabled, currentId), [clusterEnabled, currentId])
}
