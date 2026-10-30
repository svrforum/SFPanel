import { afterEach, describe, expect, it, vi } from 'vitest'

// Cluster routes are answered by the manager the browser is attached to.
// Sending them with the selected node's ?node= got a 409 from an agent, and
// had a different manager run the action — "leave" took that manager out of
// the cluster instead of this one. Other routes still follow the selection.
//
// Same harness as session.test.ts: the client is a module singleton reading
// storage in its constructor, and vitest runs in node with no DOM.

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

async function freshApi(seed: Record<string, string> = {}) {
  const local = memoryStorage()
  for (const [key, value] of Object.entries(seed)) local.setItem(key, value)
  vi.stubGlobal('sessionStorage', memoryStorage())
  vi.stubGlobal('localStorage', local)
  // request() reads window to tell the desktop wrapper apart, and the CSRF
  // cookie from document on writes.
  vi.stubGlobal('window', {})
  vi.stubGlobal('document', { cookie: '' })

  const urls: string[] = []
  vi.stubGlobal('fetch', (input: RequestInfo | URL) => {
    urls.push(String(input))
    return Promise.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ success: true, data: {} }),
    } as unknown as Response)
  })

  vi.resetModules()
  const { api } = await import('./api')
  return { api, urls }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('cluster routes', () => {
  it("asks the browser's manager for cluster routes whatever node is selected", async () => {
    const { api, urls } = await freshApi({ sfpanel_current_node: 'node-b' })
    await api.getClusterNodes()
    await api.removeClusterNode('agent-01')
    await api.leaveCluster()
    await api.getClusterEvents(20)
    const cluster = urls.filter((u) => u.includes('/cluster/'))
    expect(cluster).toHaveLength(4)
    expect(cluster.every((u) => !u.includes('node='))).toBe(true)
  })

  it('still sends other routes to the selected node', async () => {
    const { api, urls } = await freshApi({ sfpanel_current_node: 'node-b' })
    await api.getSystemInfo()
    expect(urls.at(-1)).toContain('node=node-b')
  })
})
