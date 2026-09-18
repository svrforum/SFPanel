import { afterEach, describe, expect, it, vi } from 'vitest'

// Covers api.bootstrapSession(): the one silent refresh a page load is allowed
// before the route guard decides. The refresh cookie is httpOnly, so the client
// cannot see whether a session is waiting for it — the only way to find out is
// to ask, exactly once.
//
// The API client is a module-level singleton whose constructor reads
// sessionStorage, and vitest runs these files in the default node environment
// where no DOM exists. So each case installs in-memory storage stubs *before*
// re-importing the module, which is also what gives every case its own client.

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

// jsonResponse is the minimum of Response that tryRefresh touches: ok + json().
function jsonResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response
}

// freshApi re-imports the client with empty storage (plus whatever the case
// seeds) and a fetch stub recording every URL it is called with.
async function freshApi(seed: Record<string, string> = {}, respond: () => Response = () => jsonResponse(200, { success: true, data: { token: 'new' } })) {
  const session = memoryStorage()
  for (const [key, value] of Object.entries(seed)) session.setItem(key, value)
  vi.stubGlobal('sessionStorage', session)
  vi.stubGlobal('localStorage', memoryStorage())

  const calls: string[] = []
  vi.stubGlobal('fetch', (input: RequestInfo | URL) => {
    calls.push(String(input))
    return Promise.resolve(respond())
  })

  vi.resetModules()
  const { api } = await import('./api')
  return { api, calls }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('bootstrapSession', () => {
  it('restores the token from the refresh endpoint when sessionStorage is empty', async () => {
    // No refresh_token in sessionStorage on purpose: after a browser restart
    // the cookie is all that is left, and it rides along same-origin.
    const { api, calls } = await freshApi()

    await expect(api.bootstrapSession()).resolves.toBe(true)
    expect(api.getToken()).toBe('new')
    expect(calls).toHaveLength(1)
    expect(calls[0].endsWith('/auth/refresh')).toBe(true)
  })

  it('resolves false and stores nothing when the refresh is rejected', async () => {
    const { api, calls } = await freshApi({}, () => jsonResponse(401, { success: false, error: { code: 'INVALID_TOKEN' } }))

    await expect(api.bootstrapSession()).resolves.toBe(false)
    expect(api.getToken()).toBeNull()
    expect(calls).toHaveLength(1)
  })

  it('spends no round trip on a session it already has', async () => {
    const { api, calls } = await freshApi({ token: 'existing' })

    await expect(api.bootstrapSession()).resolves.toBe(true)
    expect(api.getToken()).toBe('existing')
    expect(calls).toHaveLength(0)
  })

  it('shares one attempt between concurrent callers', async () => {
    const { api, calls } = await freshApi()

    const [first, second] = await Promise.all([api.bootstrapSession(), api.bootstrapSession()])
    expect(first).toBe(true)
    expect(second).toBe(true)
    expect(calls).toHaveLength(1)
  })

  it('does not attempt a second time after a failed one', async () => {
    const { api, calls } = await freshApi({}, () => jsonResponse(401, { success: false }))

    await expect(api.bootstrapSession()).resolves.toBe(false)
    await expect(api.bootstrapSession()).resolves.toBe(false)
    expect(calls).toHaveLength(1)
  })
})
