import type { PortMapRow } from '@/types/api'

export type ProtoFilter = 'all' | 'tcp' | 'udp'

// What a search matches on one row: the port, the protocol, the listening
// process and its PID, every address, and each container with its stack.
function searchable(r: PortMapRow): string[] {
  return [
    String(r.port),
    r.proto,
    r.process?.name ?? '',
    r.process && r.process.pid > 0 ? String(r.process.pid) : '',
    ...(r.addresses ?? []),
    ...(r.containers ?? []).flatMap((c) => [c.name, c.stack]),
  ]
}

/** The rows of the protocol that contain the query (trimmed, any case) in one
 * of their fields. An older node sends no addresses, so its rows are found by
 * everything else. */
export function filterPortRows(rows: PortMapRow[], query: string, proto: ProtoFilter): PortMapRow[] {
  const q = query.trim().toLowerCase()
  return rows.filter((r) =>
    (proto === 'all' || r.proto === proto) &&
    (!q || searchable(r).some((field) => field.toLowerCase().includes(q))),
  )
}

/** A rule is offered where something listens — what the old open-ports table
 * listed. A port Docker publishes with no listener ("bound") is the container
 * port rules tab's to govern. */
export function canAddRule(row: PortMapRow): boolean {
  return row.state === 'listening'
}
