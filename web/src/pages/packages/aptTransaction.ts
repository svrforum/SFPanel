import type { AptTransaction } from '@/types/api'

// A package transaction — an upgrade, or an install from the Packages page —
// runs in a transient unit on the node, writing a log the panel streams
// (internal/feature/packages/transaction.go). The stream is only a view of
// it: when the stream ends early, apt goes on, and the page follows it again.

/** The line the server ends a transaction's stream with. */
export const DONE = '[DONE]'

/** A server-sent event: its type ("message" unless named) and data. */
export interface SSEEvent {
  event: string
  data: string
}

/**
 * createSSEParser reads a text/event-stream in whatever chunks fetch hands
 * over. A transaction stream names its transaction as the event id first and
 * then carries the log as data lines; around them come what relays add —
 * keepalive comments, and the cluster relay's "event: error" when it lets go.
 */
export function createSSEParser(onEvent: (e: SSEEvent) => void) {
  let buffer = ''
  let type = ''
  let data: string[] = []
  let lastId = ''
  return {
    push(chunk: string) {
      buffer += chunk
      const lines = buffer.split('\n')
      buffer = lines.pop() ?? ''
      for (const raw of lines) {
        const line = raw.endsWith('\r') ? raw.slice(0, -1) : raw
        if (line === '') {
          if (data.length > 0) onEvent({ event: type || 'message', data: data.join('\n') })
          type = ''
          data = []
          continue
        }
        if (line.startsWith(':')) continue
        const colon = line.indexOf(':')
        const field = colon < 0 ? line : line.slice(0, colon)
        let value = colon < 0 ? '' : line.slice(colon + 1)
        if (value.startsWith(' ')) value = value.slice(1)
        if (field === 'event') type = value
        else if (field === 'data') data.push(value)
        else if (field === 'id') lastId = value
      }
    },
    /** The last event id the stream sent: the transaction's. */
    lastId: () => lastId,
  }
}

/** What following a transaction needs from the network. */
export interface TxIO {
  /** One request, its body handed over as it arrives. A refusal throws. */
  open: (
    path: string,
    onChunk: (text: string) => void,
    method: 'GET' | 'POST',
    body?: unknown,
    signal?: AbortSignal,
  ) => Promise<void>
  /** GET /packages/transaction's transaction. */
  status: () => Promise<AptTransaction | null>
  wait: (ms: number) => Promise<void>
}

/** How many times a stream that ended early is followed again. */
export const REFOLLOW_LIMIT = 100
/** How many attempts in a row to follow it may fail before giving up. */
export const REFOLLOW_FAILURES = 3
const REFOLLOW_DELAY_MS = 1000

/**
 * followTransaction streams a transaction's log to onLine until the server's
 * [DONE], and answers how the transaction ended. A stream that ends before it
 * — the cluster relay lets go after five minutes, a proxy or the network may
 * sooner — is followed again from the line it reached, for as long as the
 * transaction it named is still the node's. A refusal to start throws.
 * Aborting signal stops the following, and only that: it answers null.
 */
export async function followTransaction(
  io: TxIO,
  start: { path: string; method: 'GET' | 'POST'; body?: unknown },
  onLine: (line: string) => void,
  knownId = '',
  signal?: AbortSignal,
): Promise<AptTransaction | null> {
  let id = knownId
  let seen = 0
  let done = false
  const parser = () =>
    createSSEParser((e) => {
      // The relay's own events say only that it let go; the status below
      // decides what happens next.
      if (e.event !== 'message' || done) return
      if (e.data === DONE) {
        done = true
        return
      }
      seen++
      onLine(e.data)
    })

  let p = parser()
  try {
    await io.open(start.path, p.push, start.method, start.body, signal)
  } catch (err) {
    if (signal?.aborted) return null
    throw err
  }
  id = p.lastId() || id

  let failures = 0
  for (let tries = 0; !done && tries < REFOLLOW_LIMIT; tries++) {
    if (signal?.aborted) return null
    const tx = await io.status().catch(() => null)
    if (!tx || (id && tx.id !== id)) break // gone, or another one now
    id = tx.id
    if (tx.running) await io.wait(REFOLLOW_DELAY_MS)
    p = parser()
    try {
      await io.open(`/packages/transaction/stream?id=${encodeURIComponent(id)}&skip=${seen}`, p.push, 'GET', undefined, signal)
      failures = 0
    } catch {
      if (signal?.aborted) return null
      // Refused or unreachable: a few more tries, spaced out, then the
      // status that follows is the answer.
      if (++failures >= REFOLLOW_FAILURES) break
      await io.wait(REFOLLOW_DELAY_MS)
    }
  }
  if (signal?.aborted) return null
  const tx = await io.status().catch(() => null)
  return tx && (!id || tx.id === id) ? tx : null
}
