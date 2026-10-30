import { afterEach, describe, expect, it, vi } from 'vitest'

// A refusal reads the same whichever way it arrived. The cluster update stream
// rejected a 409 UPDATE_IN_PROGRESS as a bare "HTTP 409": the code, the
// details and the server's sentence were all dropped before the page saw them.
//
// The client is a module singleton reading storage in its constructor, and
// vitest runs in node with no DOM (same harness as session.test.ts).

function memoryStorage(): Storage {
  const map = new Map<string, string>()
  return {
    get length() {
      return map.size
    },
    clear: () => {
      map.clear()
    },
    getItem: (key: string) => map.get(key) ?? null,
    key: (index: number) => Array.from(map.keys())[index] ?? null,
    removeItem: (key: string) => {
      map.delete(key)
    },
    setItem: (key: string, value: string) => {
      map.set(key, value)
    },
  }
}

async function freshApi() {
  const session = memoryStorage()
  session.setItem('token', 'test-token')
  vi.stubGlobal('sessionStorage', session)
  vi.stubGlobal('localStorage', memoryStorage())
  vi.stubGlobal('window', {})
  vi.stubGlobal('document', { cookie: '' })
  vi.resetModules()
  return import('./api')
}

function envelope(status: number, error: Record<string, unknown>): Response {
  return new Response(JSON.stringify({ success: false, error }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('apiErrorFrom / errorFromResponse', () => {
  it('keeps the code, status and details of an error envelope', async () => {
    const { errorFromResponse } = await freshApi()
    const res = envelope(409, { code: 'CLUSTER_QUORUM', message: 'quorum', details: { a: 'b' } })
    const err = await errorFromResponse(res)
    expect([err.status, err.code, err.details]).toEqual([409, 'CLUSTER_QUORUM', { a: 'b' }])
  })

  it('names the status when the body is not an envelope', async () => {
    const { errorFromResponse } = await freshApi()
    const err = await errorFromResponse(new Response('Bad Gateway', { status: 502 }))
    expect([err.message, err.code]).toEqual(['HTTP 502', undefined])
  })

  it('keeps the server message for a code it does not word', async () => {
    const { apiErrorFrom, setApiTranslator } = await freshApi()
    // A translator that answers with the key, so wording the code would show.
    setApiTranslator((key) => key)
    const err = apiErrorFrom(409, { success: false, error: { code: 'CLUSTER_CONVERT_REFUSED', message: 'node-b is the leader' } })
    expect(err.message).toBe('node-b is the leader')
  })

  it('words a code it knows through the translator, with the details filled in', async () => {
    const { apiErrorFrom, setApiTranslator } = await freshApi()
    setApiTranslator((key, _fallback, params) => `${key}|${params?.node_id ?? ''}`)
    const err = apiErrorFrom(200, {
      success: false,
      error: { code: 'CLUSTER_LEAVE_UNACKNOWLEDGED', message: 'no leader acknowledged', details: { node_id: 'node-a' } },
    })
    expect(err.message).toBe('cluster.warnings.leaveUnacknowledged|node-a')
  })
})

describe('clusterUpdateStream', () => {
  it('rejects a refused update with its code, not a bare status', async () => {
    const { api } = await freshApi()
    vi.stubGlobal('fetch', () => Promise.resolve(envelope(409, { code: 'UPDATE_IN_PROGRESS', message: 'update already running' })))
    const err = await api.clusterUpdateStream('rolling', () => {}).then(
      () => undefined,
      (e: unknown) => e as { status?: number; code?: string; message: string },
    )
    expect([err?.status, err?.code, err?.message]).toEqual([409, 'UPDATE_IN_PROGRESS', 'update already running'])
  })
})
