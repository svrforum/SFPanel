import { NetworkView } from '../running/network/NetworkView'
import { SearchField } from '../running/SearchField'
import { useMapContext } from './mapContext'

/** The map's devices tab: each network the servers are on, then each server from the inside. */
export default function MapDevices() {
  const { sections, scope, round, query, setQuery } = useMapContext()
  return (
    <>
      <SearchField value={query} onChange={setQuery} network />
      <NetworkView sections={sections} scope={scope} query={query} round={round} level={2} onClearQuery={() => setQuery('')} />
    </>
  )
}
