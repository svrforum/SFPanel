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

  it('falls through to the login form when the refresh never answers', async () => {
    // A front end that accepts the connection and answers nothing — a wedged
    // proxy in front of the panel. Before the deadline this hung the boot, and
    // the route guard renders a spinner until it resolves, so the operator got
    // no login form at all (issue #54's deployment shape).
    vi.useFakeTimers()
    try {
      const session = memoryStorage()
      vi.stubGlobal('sessionStorage', session)
      vi.stubGlobal('localStorage', memoryStorage())
      const calls: string[] = []
      vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push(String(input))
        return new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
        })
      })
      vi.resetModules()
      const { api } = await import('./api')

      let settled: boolean | 'pending' = 'pending'
      const boot = api.bootstrapSession().then((ok) => (settled = ok))

      await vi.advanceTimersByTimeAsync(9000)
      expect(settled).toBe('pending')

      await vi.advanceTimersByTimeAsync(2000)
      await boot
      expect(settled).toBe(false)
      expect(calls).toHaveLength(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('stops answering from the boot cache once the session is cleared', async () => {
    // Logout does not reload the page, so the resolved-true promise the boot
    // left behind would let the next client-side navigation into a protected
    // route render it with no token.
    const { api } = await freshApi()

    await expect(api.bootstrapSession()).resolves.toBe(true)
    api.clearToken()
    await expect(api.bootstrapSession()).resolves.toBe(false)
  })

  it('does not attempt a second time after a failed one', async () => {
    const { api, calls } = await freshApi({}, () => jsonResponse(401, { success: false }))

    await expect(api.bootstrapSession()).resolves.toBe(false)
    await expect(api.bootstrapSession()).resolves.toBe(false)
    expect(calls).toHaveLength(1)
  })
})

// The refresh cookie rotates on every use and the server revokes the whole
// family when a consumed one comes back (internal/feature/auth/refresh.go), so
// two tabs booting together must not present it at the same time. Each tab
// still needs its own refresh — sessionStorage is per-tab — so what these cases
// assert is that the two never overlap, not that one of them is skipped.
//
// Two tabs of one browser = two client instances over one localStorage. The
// module is a singleton, so `vi.resetModules()` between imports is what makes
// the second instance.

// lockManagerStub is the slice of the Web Locks API bootRefresh uses: requests
// for one name run one at a time, in arrival order.
function lockManagerStub(taken: string[]): LockManager {
  const queues = new Map<string, Promise<unknown>>()
  return {
    request: (name: string, _options: unknown, callback: () => Promise<unknown>) => {
      taken.push(name)
      const next = (queues.get(name) ?? Promise.resolve()).then(callback, callback)
      queues.set(name, next.catch(() => undefined))
      return next
    },
  } as unknown as LockManager
}

async function racingTabs(locks: LockManager | undefined) {
  vi.stubGlobal('sessionStorage', memoryStorage())
  vi.stubGlobal('localStorage', memoryStorage())
  vi.stubGlobal('navigator', locks ? { locks } : {})

  let inFlight = 0
  let peak = 0
  const calls: string[] = []
  vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
    calls.push(String(input))
    inFlight += 1
    peak = Math.max(peak, inFlight)
    // Long enough that an unserialised second tab would still be inside the
    // first tab's request, which is exactly the replay the server punishes.
    await new Promise((resolve) => setTimeout(resolve, 40))
    inFlight -= 1
    return jsonResponse(200, { success: true, data: { token: 'rotated' } })
  })

  vi.resetModules()
  const firstTab = (await import('./api')).api
  vi.resetModules()
  const secondTab = (await import('./api')).api
  return { firstTab, secondTab, calls, peak: () => peak }
}

describe('two tabs booting together', () => {
  it('never has two refreshes in flight at once (localStorage lease)', async () => {
    const { firstTab, secondTab, calls, peak } = await racingTabs(undefined)

    const results = await Promise.all([firstTab.bootstrapSession(), secondTab.bootstrapSession()])

    expect(results).toEqual([true, true])
    expect(calls).toHaveLength(2)
    expect(peak()).toBe(1)
  })

  it('never has two refreshes in flight at once (Web Locks)', async () => {
    const taken: string[] = []
    const { firstTab, secondTab, calls, peak } = await racingTabs(lockManagerStub(taken))

    const results = await Promise.all([firstTab.bootstrapSession(), secondTab.bootstrapSession()])

    expect(results).toEqual([true, true])
    expect(calls).toHaveLength(2)
    expect(peak()).toBe(1)
    // The lease would serialise these two on its own, so name the mechanism:
    // where the browser has Web Locks, that is what holds them apart.
    expect(taken).toEqual(['sfpanel_session_bootstrap', 'sfpanel_session_bootstrap'])
  })

  it('claims a lease a tab that died mid-refresh left behind', async () => {
    const { firstTab, calls } = await racingTabs(undefined)
    // Older than the wait, so it is claimable immediately instead of costing
    // the next boot five seconds. The other fail-open — a live lease held past
    // BOOT_LOCK_WAIT — is not asserted here: it would cost the suite that wait
    // in real time, and the timers this helper's fetch stub uses are real.
    localStorage.setItem('sfpanel_bootstrap_lock', `${Date.now() - 60_000}:dead-tab`)

    const started = Date.now()
    await expect(firstTab.bootstrapSession()).resolves.toBe(true)

    expect(calls).toHaveLength(1)
    expect(Date.now() - started).toBeLessThan(1000)
  })
})
