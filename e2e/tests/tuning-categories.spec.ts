import { test, expect } from '@playwright/test'

// Guards the tuning-status API payload against future regressions. The
// structure is load-bearing for the /settings tuning UI (categories +
// per-param current vs. recommended values + applied flag).
//
// Requires a valid admin session. Env vars:
//   PW_BASE_URL   panel URL
//   PW_USER       admin username
//   PW_PASS       admin password

const baseURL = process.env.PW_BASE_URL
const user = process.env.PW_USER
const pass = process.env.PW_PASS

const haveEnv = baseURL && user && pass

async function login(request: import('@playwright/test').APIRequestContext): Promise<string> {
  const res = await request.post(`${baseURL}/api/v1/auth/login`, {
    headers: { 'Content-Type': 'application/json' },
    data: { username: user, password: pass },
  })
  const json = await res.json()
  expect(json.success).toBe(true)
  return json.data.token as string
}

test.describe('System tuning categories', () => {
  test.skip(!haveEnv, 'requires PW_BASE_URL, PW_USER, PW_PASS')

  test('response shape and category presence', async ({ request }) => {
    const token = await login(request)
    const res = await request.get(`${baseURL}/api/v1/system/tuning`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(res.ok()).toBe(true)
    const json = await res.json()
    expect(json.success).toBe(true)

    const cats = json.data.categories as Array<{ name: string; params: Array<{ key: string }> }>
    const names = cats.map((c) => c.name)

    // The four always-present categories.
    expect(names).toContain('network')
    expect(names).toContain('memory')
    expect(names).toContain('filesystem')
    expect(names).toContain('security')

    // Newly-added essentials must be reachable (by key) in their categories.
    const network = cats.find((c) => c.name === 'network')!.params.map((p) => p.key)
    expect(network).toContain('net.ipv4.ip_forward')
    expect(network).toContain('net.bridge.bridge-nf-call-iptables')
    expect(network).toContain('net.ipv4.tcp_slow_start_after_idle')
    expect(network).toContain('net.ipv4.tcp_notsent_lowat')
    expect(network).toContain('net.ipv4.tcp_rfc1337')

    const memory = cats.find((c) => c.name === 'memory')!.params.map((p) => p.key)
    expect(memory).toContain('vm.max_map_count')
    expect(memory).toContain('kernel.pid_max')

    const fs = cats.find((c) => c.name === 'filesystem')!.params.map((p) => p.key)
    expect(fs).toContain('fs.protected_symlinks')
    expect(fs).toContain('fs.suid_dumpable')

    const security = cats.find((c) => c.name === 'security')!.params.map((p) => p.key)
    expect(security).toContain('kernel.kptr_restrict')
    expect(security).toContain('kernel.unprivileged_bpf_disabled')
    expect(security).toContain('net.core.bpf_jit_harden')
    expect(security).toContain('kernel.yama.ptrace_scope')
  })

  test('conntrack category appears when nf_conntrack is loaded', async ({ request }) => {
    const token = await login(request)
    const res = await request.get(`${baseURL}/api/v1/system/tuning`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    const json = await res.json()
    const cats = json.data.categories as Array<{ name: string; params: Array<{ key: string }> }>
    const conntrack = cats.find((c) => c.name === 'conntrack')

    // On a Docker host (this is one) the module is loaded — category must
    // appear. On a bare box without Docker/netfilter it would be absent;
    // we don't fail the test in that case, we just skip the assertions.
    if (!conntrack) {
      test.info().annotations.push({ type: 'note', description: 'nf_conntrack not loaded; conntrack category intentionally absent' })
      return
    }
    const keys = conntrack.params.map((p) => p.key)
    expect(keys).toContain('net.netfilter.nf_conntrack_max')
    expect(keys).toContain('net.netfilter.nf_conntrack_tcp_timeout_established')
    expect(keys).toContain('net.netfilter.nf_conntrack_tcp_timeout_close_wait')
  })
})
