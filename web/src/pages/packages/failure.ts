// A failure toast names what failed and, when the server or the browser said,
// why: "Failed to search packages: apt-cache is not installed". The bare
// sentence left no way to tell a refused request from an empty answer.
export function failure(what: string, err: unknown): string {
  const why = err instanceof Error ? err.message : ''
  return why ? `${what}: ${why}` : what
}
