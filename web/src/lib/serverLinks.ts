import { taskRunHref } from '@/lib/taskPrefill'

/**
 * The longest folder a terminal address takes. Linux's PATH_MAX is 4096
 * bytes, and a path is never longer in UTF-16 units than in UTF-8 bytes.
 */
const MAX_DIR = 4096

/** The C0 controls and DEL: a line break or NUL has no place in a folder. */
const isControl = (ch: string) => {
  const c = ch.charCodeAt(0)
  return c < 0x20 || c === 0x7f
}

/** The terminal, asked to open a new session in dir (an absolute path). */
export function terminalDirHref(dir: string): string {
  return `/terminal?dir=${encodeURIComponent(dir)}`
}

/**
 * The folder a terminal address asks for: an absolute path of at most 4096
 * characters with no control characters, else null.
 */
export function dirParam(search: string): string | null {
  const dir = new URLSearchParams(search).get('dir')
  if (!dir || !dir.startsWith('/') || dir.length > MAX_DIR) return null
  if ([...dir].some(isControl)) return null
  return dir
}

/** A group task's run setup, opened with the server on screen picked and the task's inputs filled. */
export type FleetRunHref = (taskId: string, inputs?: Record<string, string>) => string

/**
 * Where "on several servers…" leads from a server's screen: in a cluster of
 * more than one server, the task's run setup with the server shown picked,
 * for the operator to add the rest. Null on a single server, in a cluster of
 * one (as moving a Docker stack is offered), or while the server shown or the
 * node list is not known yet: there is nothing to run it on but this one.
 */
export function fleetRunHrefFor(clusterEnabled: boolean | null, currentId: string, serverCount: number): FleetRunHref | null {
  if (clusterEnabled !== true || !currentId || serverCount <= 1) return null
  return (taskId, inputs) => taskRunHref(taskId, { nodes: [currentId], inputs })
}
