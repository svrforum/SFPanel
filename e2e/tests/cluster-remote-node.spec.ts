import { test, expect } from '@playwright/test'

// Covers the cluster remote-node proxy path: after selecting a non-local
// node in the sidebar NodeSelector, REST calls and WebSocket streams must
// route to that node via the ClusterProxy + WrapEchoWSHandler paths.
//
// This test runs against a 2-node cluster. Both nodes must be online. The
// following env vars control behavior:
//   BASE_URL          Leader's panel URL                (required)
//   PW_JWT            Pre-signed JWT for leader         (required)
//   PW_REMOTE_NODE_ID Remote node UUID                  (required)
//   PW_REMOTE_HOST    Remote node's expected hostname   (required — used to
//                     assert the proxy is actually hitting the remote)

const baseURL = process.env.PW_BASE_URL
const token = process.env.PW_JWT
const remoteId = process.env.PW_REMOTE_NODE_ID
const remoteHost = process.env.PW_REMOTE_HOST

const skipReason = 'cluster tests require PW_BASE_URL, PW_JWT, PW_REMOTE_NODE_ID, PW_REMOTE_HOST'
const haveEnv = baseURL && token && remoteId && remoteHost

test.describe('Cluster remote-node proxy', () => {
  test.skip(!haveEnv, skipReason)

  test('REST ?node=<remote> returns remote hostname', async ({ request }) => {
    const res = await request.get(`${baseURL}/api/v1/system/info?node=${remoteId}`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(res.ok()).toBe(true)
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data.host.hostname).toBe(remoteHost)
  })

  test('REST without ?node returns local (leader) hostname', async ({ request }) => {
    const res = await request.get(`${baseURL}/api/v1/system/info`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data.host.hostname).not.toBe(remoteHost)
  })

  test('WS /ws/metrics?node=<remote> delivers remote metrics', async ({ page }) => {
    test.setTimeout(20000)
    const wsBase = baseURL!.replace(/^http/, 'ws')

    // Evaluate in the browser context so we're using real WS transport.
    const result = await page.evaluate(
      async ({ wsBase, token, remoteId }) => {
        return new Promise<{ label: string; mem: number | null; err?: string }>((resolve) => {
          const ws = new WebSocket(`${wsBase}/ws/metrics?token=${token}&node=${remoteId}`)
          const timeout = setTimeout(() => {
            try { ws.close() } catch {}
            resolve({ label: 'remote', mem: null, err: 'timeout waiting for first metric' })
          }, 10000)
          ws.addEventListener('message', (e) => {
            clearTimeout(timeout)
            try {
              const d = JSON.parse(e.data as string)
              resolve({ label: 'remote', mem: d.mem_total ?? null })
            } catch (err) {
              resolve({ label: 'remote', mem: null, err: String(err) })
            } finally {
              try { ws.close() } catch {}
            }
          })
          ws.addEventListener('error', () => {
            clearTimeout(timeout)
            resolve({ label: 'remote', mem: null, err: 'ws error' })
          })
        })
      },
      { wsBase, token: token!, remoteId: remoteId! },
    )
    expect(result.err).toBeFalsy()
    expect(result.mem).toBeGreaterThan(0)

    // Also grab a local sample to confirm the two streams are distinct.
    const local = await page.evaluate(
      async ({ wsBase, token }) => {
        return new Promise<{ mem: number | null }>((resolve) => {
          const ws = new WebSocket(`${wsBase}/ws/metrics?token=${token}`)
          const timeout = setTimeout(() => {
            try { ws.close() } catch {}
            resolve({ mem: null })
          }, 10000)
          ws.addEventListener('message', (e) => {
            clearTimeout(timeout)
            try {
              const d = JSON.parse(e.data as string)
              resolve({ mem: d.mem_total ?? null })
            } finally {
              try { ws.close() } catch {}
            }
          })
        })
      },
      { wsBase, token: token! },
    )
    expect(local.mem).toBeGreaterThan(0)
    expect(local.mem).not.toBe(result.mem) // different hosts → different RAM
  })
})
