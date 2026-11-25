import { useOutletContext } from 'react-router-dom'
import type { Scope } from '../running/targets'
import type { BoardSection } from '../running/useRunningBoard'

/** What the map page hands its tabs: the servers it reads, and the search and refresh the tabs share. */
export interface MapContext {
  sections: BoardSection[]
  scope: Scope
  /** Goes up when the operator asks for a refresh: the tabs' own reads are asked again then, not with the page's timer. */
  round: number
  query: string
  setQuery: (query: string) => void
}

export function useMapContext(): MapContext {
  return useOutletContext<MapContext>()
}
