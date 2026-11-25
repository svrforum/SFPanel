import { RackView } from '../running/rack/RackView'
import { useMapContext } from './mapContext'

/** The map's rack tab: the racks the operator draws, read from every server as one. */
export default function MapRack() {
  const { sections, round } = useMapContext()
  return <RackView sections={sections} round={round} level={2} />
}
