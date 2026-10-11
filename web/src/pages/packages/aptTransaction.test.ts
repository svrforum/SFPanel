import { describe, expect, it } from 'vitest'
import type { AptTransaction } from '@/types/api'
import { createSSEParser, DONE, followTransaction, REFOLLOW_FAILURES, type SSEEvent, type TxIO } from './aptTransaction'

const tx = (over: Partial<AptTransaction> = {}): AptTransaction => ({
  id: '20261012-101500-abcd',
  kind: 'upgrade',
  packages: [],
  command: 'apt-get -y dist-upgrade',
  started_at: '2026-10-12T10:15:00Z',
  running: true,
  ...over,
})

describe('createSSEParser', () => {
  it('reads events split anywhere across chunks, and the id the stream opens with', () => {
    const events: SSEEvent[] = []
    const p = createSSEParser((e) => events.push(e))
    const body = 'id: 20261012-101500-abcd\n\ndata: >>> apt-get update\n\n: keepalive\n\ndata: \n\ndata: Unpacking example (1.1) ...\r\n\r\n'
    for (const chunk of body.match(/.{1,7}/gs) ?? []) p.push(chunk)
    expect(events).toEqual([
      { event: 'message', data: '>>> apt-get update' },
      { event: 'message', data: '' },
      { event: 'message', data: 'Unpacking example (1.1) ...' },
    ])
    expect(p.lastId()).toBe('20261012-101500-abcd')
  })

  it('tells the cluster relay letting go from a line of the log', () => {
    const events: SSEEvent[] = []
    const p = createSSEParser((e) => events.push(e))
    p.push('data: one\n\nevent: error\ndata: {"message":"upstream stream interrupted"}\n\n')
    expect(events).toEqual([
      { event: 'message', data: 'one' },
      { event: 'error', data: '{"message":"upstream stream interrupted"}' },
    ])
  })
})

// A scripted network: each open() replays the next body, and status()
// answers from a list.
function scripted(bodies: string[], statuses: (AptTransaction | null)[]) {
  const opened: { path: string; method: string }[] = []
  const io: TxIO = {
    open: async (path, onChunk, method) => {
      opened.push({ path, method })
      const body = bodies.shift()
      if (body === undefined) throw new Error('Stream failed (404)')
      onChunk(body)
    },
    status: async () => (statuses.length > 1 ? statuses.shift()! : statuses[0]),
    wait: async () => {},
  }
  return { io, opened }
}

describe('followTransaction', () => {
  it('follows a cut stream again from the line it reached, and answers how it ended', async () => {
    const { io, opened } = scripted(
      [
        'id: 20261012-101500-abcd\n\ndata: one\n\ndata: two\n\nevent: error\ndata: {"message":"upstream stream interrupted"}\n\n',
        'id: 20261012-101500-abcd\n\ndata: three\n\ndata: >>> Packages upgraded successfully\n\ndata: [DONE]\n\n',
      ],
      [tx(), tx({ running: false, result: 'success', exit_code: 0 })],
    )
    const lines: string[] = []
    const end = await followTransaction(io, { path: '/packages/upgrade', method: 'POST', body: {} }, (l) => lines.push(l))
    expect(lines).toEqual(['one', 'two', 'three', '>>> Packages upgraded successfully'])
    expect(opened[1]).toEqual({ path: '/packages/transaction/stream?id=20261012-101500-abcd&skip=2', method: 'GET' })
    expect(end?.result).toBe('success')
  })

  it('does not follow another transaction that started meanwhile', async () => {
    const { io, opened } = scripted(['id: 20261012-101500-abcd\n\ndata: one\n\n'], [tx({ id: '20261012-110000-ffff' })])
    const end = await followTransaction(io, { path: '/packages/upgrade', method: 'POST' }, () => {})
    expect(opened).toHaveLength(1)
    expect(end).toBeNull()
  })

  it('stops at the end of the stream', async () => {
    const { io, opened } = scripted([`id: 20261012-101500-abcd\n\ndata: done\n\ndata: ${DONE}\n\n`], [tx({ running: false, result: 'failed', exit_code: 100 })])
    const end = await followTransaction(io, { path: '/packages/install-stream', method: 'POST', body: { name: 'htop' } }, () => {})
    expect(opened).toHaveLength(1)
    expect(end?.exit_code).toBe(100)
  })

  it('gives up on a transaction it cannot reach any more', async () => {
    const { io, opened } = scripted(['id: 20261012-101500-abcd\n\ndata: one\n\n'], [tx()])
    await followTransaction(io, { path: '/packages/upgrade', method: 'POST' }, () => {})
    expect(opened).toHaveLength(1 + REFOLLOW_FAILURES)
  })

  it('lets a refusal through to the page', async () => {
    const io: TxIO = {
      open: async () => {
        throw Object.assign(new Error('A package transaction is already running'), { code: 'APT_TRANSACTION_RUNNING' })
      },
      status: async () => null,
      wait: async () => {},
    }
    await expect(followTransaction(io, { path: '/packages/upgrade', method: 'POST' }, () => {})).rejects.toMatchObject({
      code: 'APT_TRANSACTION_RUNNING',
    })
  })
})
