import { BaseEdge, type Edge, type EdgeProps } from '@xyflow/react'
import { CANVAS, edgePath, type Route } from '@/lib/runningLayout'

/** The edge from a guest tile to the box of the server that guest is, carrying how it runs. */
export type GuestEdge = Edge<Route, 'guest'>

// Where the layout gave no route (a box the operator moved keeps its edge,
// and one drawn before the layout knew of it has none yet): the first
// channel and the first lane.
const FALLBACK: Route = { channel: CANVAS.boxBorder + CANVAS.boxPadding + CANVAS.channelBase, lane: CANVAS.laneBase }

/**
 * Round the boxes, not through them: out of the tile, up the channel beside
 * the host, along the lane above the row and down into the server's box. The
 * library's own step edge takes the shortest way, which with the servers in
 * a row is behind the boxes between.
 */
export function GuestEdgeView({ sourceX, sourceY, targetX, targetY, data }: EdgeProps<GuestEdge>) {
  // Nothing here is clicked or picked, so no wide invisible twin to catch the pointer.
  return <BaseEdge path={edgePath(sourceX, sourceY, targetX, targetY, data ?? FALLBACK)} interactionWidth={0} />
}
