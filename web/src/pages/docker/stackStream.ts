export type StackStreamEvent = { phase: string; line: string }

/**
 * Runs a deploy or update stream and rejects when the stream reported a
 * failure. The server answers these streams 200 and carries a failure as an
 * "error" event before ending normally, so the request resolving says nothing
 * about the outcome.
 */
export async function runStackStream(
  run: (onEvent: (event: StackStreamEvent) => void) => Promise<void>,
  onEvent: (event: StackStreamEvent) => void,
): Promise<void> {
  let failure: string | null = null
  await run((event) => {
    if (event.phase === 'error') failure = event.line
    onEvent(event)
  })
  if (failure !== null) throw new Error(failure)
}

/** Runs op on every item in turn, going on past a failure; returns the items that failed. */
export async function runEach<T>(items: T[], op: (item: T) => Promise<unknown>): Promise<T[]> {
  const failed: T[] = []
  for (const item of items) {
    try {
      await op(item)
    } catch {
      failed.push(item)
    }
  }
  return failed
}
