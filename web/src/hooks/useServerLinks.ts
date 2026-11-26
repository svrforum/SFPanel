import { useMemo } from 'react'
import { useServerScope } from '@/hooks/useServerScope'
import { fleetRunHrefFor, type FleetRunHref } from '@/lib/serverLinks'

/** Group task links for the server on screen; null where there are none to offer (a single server, a cluster of one). */
export function useFleetRunHref(): FleetRunHref | null {
  const { clusterEnabled, currentId, nodes } = useServerScope()
  const serverCount = nodes.length
  return useMemo(() => fleetRunHrefFor(clusterEnabled, currentId, serverCount), [clusterEnabled, currentId, serverCount])
}
