import { test, expect } from '@playwright/test'

// Covers the cluster remote-node proxy path: after selecting a non-local
// node in the sidebar NodeSelector, REST calls and WebSocket streams must
// route to that node via the ClusterProxy + WrapEchoWSHandler paths.
//
// Requires a live 2-node cluster. Env vars:
//   PW_BASE_URL       Leader's panel URL           (required)
//   PW_USER           Admin username on leader     (required)
//   PW_PASS           Admin password               (required)
//   PW_REMOTE_NODE_ID Remote node UUID             (required)
//   PW_REMOTE_HOST    Remote node's expected hostname (required)

const baseURL = process.env.PW_BASE_URL
const user = process.env.PW_USER
const pass = process.env.PW_PASS
const remoteId = process.env.PW_REMOTE_NODE_ID
const remoteHost = process.env.PW_REMOTE_HOST

const haveEnv = baseURL && user && pass && remoteId && remoteHost
const skipReason = 'cluster tests require PW_BASE_URL, PW_USER, PW_PASS, PW_REMOTE_NODE_ID, PW_REMOTE_HOST'

// login() hits the panel's real /auth/login so these tests exercise the
// production auth flow, not a hand-minted JWT. The token is captured once
// per spec and reused by the following tests via the `token` fixture.
async function login(request: import('@playwright/test').APIRequestContext): Promise<string> {
  const res = await request.post(`${baseURL}/api/v1/auth/login`, {
    headers: { 'Content-Type': 'application/json' },
    data: { username: user, password: pass },
  })
  const json = await res.json()
  if (!json.success || !json.data?.token) {
    throw new Error(`login failed: ${JSON.stringify(json)}`)
  }
  return json.data.token as string
}

test.describe('Cluster remote-node proxy', () => {
  test.skip(!haveEnv, skipReason)

  test('REST ?node=<remote> returns remote hostname', async ({ request }) => {
    const token = await login(request)
    const res = await request.get(`${baseURL}/api/v1/system/info?node=${remoteId}`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(res.ok()).toBe(true)
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data.host.hostname).toBe(remoteHost)
  })

  test('REST without ?node returns local (leader) hostname', async ({ request }) => {
    const token = await login(request)
    const res = await request.get(`${baseURL}/api/v1/system/info`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data.host.hostname).not.toBe(remoteHost)
  })

  test('WS /ws/metrics?node=<remote> delivers remote metrics', async ({ page, request }) => {
    test.setTimeout(20000)
    const token = await login(request)
    const wsBase = baseURL!.replace(/^http/, 'ws')

    // Evaluate in the browser context so we're exercising real WS transport.
    const remote = await page.evaluate(
      async ({ wsBase, token, remoteId }) => {
        return new Promise<{ mem: number | null; err?: string }>((resolve) => {
          const ws = new WebSocket(`${wsBase}/ws/metrics?token=${token}&node=${remoteId}`)
          const timeout = setTimeout(() => {
            try { ws.close() } catch {}
            resolve({ mem: null, err: 'timeout' })
          }, 10000)
          ws.addEventListener('message', (e) => {
            clearTimeout(timeout)
            try {
              const d = JSON.parse(e.data as string)
              resolve({ mem: d.mem_total ?? null })
            } catch (err) {
              resolve({ mem: null, err: String(err) })
            } finally {
              try { ws.close() } catch {}
            }
          })
          ws.addEventListener('error', () => {
            clearTimeout(timeout)
            resolve({ mem: null, err: 'ws error' })
          })
        })
      },
      { wsBase, token, remoteId: remoteId! },
    )
    expect(remote.err).toBeFalsy()
    expect(remote.mem).toBeGreaterThan(0)

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
      { wsBase, token },
    )
    expect(local.mem).toBeGreaterThan(0)
    expect(local.mem).not.toBe(remote.mem) // different hosts → different RAM
  })
})
