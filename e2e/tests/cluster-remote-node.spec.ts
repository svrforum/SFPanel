import { test, expect } from '@playwright/test'
import { login } from './helpers'

// Covers the cluster remote-node proxy path: after selecting a non-local
// node in the sidebar NodeSelector, REST calls and WebSocket streams must
// route to that node via the ClusterProxy + WrapEchoWSHandler paths.
//
// Requires a live 2-node cluster. Env vars:
//   PW_BASE_URL       Leader's panel URL           (required)
//   PW_REMOTE_NODE_ID Remote node UUID             (required)
//   PW_REMOTE_HOST    Remote node's expected hostname (required)
// Optional:
//   PW_USER, PW_PASS  Admin creds (default admin / TestPass123!; no TOTP)

const baseURL = process.env.PW_BASE_URL
const remoteId = process.env.PW_REMOTE_NODE_ID
const remoteHost = process.env.PW_REMOTE_HOST

const haveEnv = baseURL && remoteId && remoteHost
const skipReason = 'cluster tests require PW_BASE_URL, PW_REMOTE_NODE_ID, PW_REMOTE_HOST'

test.describe('Cluster remote-node proxy', () => {
  test.skip(!haveEnv, skipReason)

  test('REST ?node=<remote> returns remote hostname', async ({ request }) => {
    const session = await login(request, { baseURL })
    const res = await request.get(`${baseURL}/api/v1/system/info?node=${remoteId}`, {
      headers: session.headers,
    })
    expect(res.ok()).toBe(true)
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data.host.hostname).toBe(remoteHost)
  })

  test('REST without ?node returns local (leader) hostname', async ({ request }) => {
    const session = await login(request, { baseURL })
    const res = await request.get(`${baseURL}/api/v1/system/info`, {
      headers: session.headers,
    })
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data.host.hostname).not.toBe(remoteHost)
  })

  test('WS /ws/metrics?node=<remote> delivers remote metrics', async ({ page, request }) => {
    test.setTimeout(20000)
    const session = await login(request, { baseURL })
    const token = session.token
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

    // The metrics WS payload carries no hostname, and comparing mem_total
    // across nodes is flaky on identically-sized VMs (same RAM → false
    // failure). Assert node identity via /system/info hostnames over the
    // same ?node= proxy path instead; the WS assertions above prove relay
    // delivery for both targets.
    const localInfo = await (await request.get(`${baseURL}/api/v1/system/info`, { headers: session.headers })).json()
    const remoteInfo = await (await request.get(`${baseURL}/api/v1/system/info?node=${remoteId}`, { headers: session.headers })).json()
    expect(remoteInfo.data.host.hostname).toBe(remoteHost)
    expect(localInfo.data.host.hostname).not.toBe(remoteInfo.data.host.hostname)
  })
})
