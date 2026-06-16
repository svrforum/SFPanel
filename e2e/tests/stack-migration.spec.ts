import { test, expect } from '@playwright/test'
import type { APIRequestContext } from '@playwright/test'
import { login, type AuthSession } from './helpers'

// Exercises the node-to-node stack migration feature end to end against a live
// 2-node cluster. The source node (PW_BASE_URL) creates a tiny stateless stack,
// runs the migration orchestrator at POST /docker/compose/:project/migrate, and
// we assert the stack lands RUNNING on the target node and the source is left
// in the expected state for the chosen disposition.
//
// The migration handler is the orchestrator: it runs LOCALLY on the source node
// and reaches the target over the cluster channel itself, so we always POST to
// PW_BASE_URL directly (no ?node=). We read the target node's id from the
// cluster nodes list (the node whose id != local_id) and query the target's
// stack state via the ?node= proxy on the compose list/services endpoints.
//
// Required env vars:
//   PW_BASE_URL       Source node URL (leader, absolute)   (required)
// Optional:
//   PW_USER, PW_PASS  Admin creds (default admin / TestPass123!; no TOTP)
//
// This spec is manual/local only — CI does not provision a 2-node cluster, so
// without PW_BASE_URL the whole describe block self-skips (clean no-op).

const baseURL = process.env.PW_BASE_URL

const haveEnv = !!baseURL
const skipReason = 'stack migration tests require PW_BASE_URL + a live 2-node cluster'

const STACK = 'e2e-migrate-test'
// High host port unlikely to collide with anything the lab already runs.
const HOST_PORT = 18099

// A single stateless service. retain/clone keep the source; this image pulls
// fast and needs no persistent state, so migration is a clean cold-copy.
function whoamiCompose(image = 'traefik/whoami'): string {
  return [
    'services:',
    '  whoami:',
    `    image: ${image}`,
    '    ports:',
    `      - "${HOST_PORT}:80"`,
    '',
  ].join('\n')
}

// resolveTargetNodeId returns the id of a node that is NOT the local (source)
// node, per GET /cluster/nodes. Returns null when the cluster reports fewer
// than two nodes (env misconfigured) so the test can fail with a clear message.
async function resolveTargetNodeId(
  request: APIRequestContext,
  session: AuthSession,
): Promise<string | null> {
  const res = await request.get(`${baseURL}/api/v1/cluster/nodes`, { headers: session.headers })
  const json = await res.json()
  if (!json.success) return null
  const localId: string = json.data.local_id ?? ''
  const nodes: Array<{ id: string }> = json.data.nodes ?? []
  const target = nodes.find((n) => n.id && n.id !== localId)
  return target?.id ?? null
}

// realStatus reads the real_status field for STACK from the compose listing on
// the given node (omit nodeId for the local/source node). Returns '' when the
// stack is absent from that node's listing.
async function realStatus(
  request: APIRequestContext,
  session: AuthSession,
  nodeId?: string,
): Promise<string> {
  const q = nodeId ? `?node=${nodeId}` : ''
  const res = await request.get(`${baseURL}/api/v1/docker/compose${q}`, { headers: session.headers })
  const json = await res.json()
  if (!json.success || !Array.isArray(json.data)) return ''
  const proj = json.data.find((p: { name: string }) => p.name === STACK)
  return proj?.real_status ?? ''
}

// pollStatus waits until realStatus(node) equals `want` or the timeout elapses,
// returning the last status seen. Migration + docker state settle is not
// synchronous from the caller's perspective, so we poll rather than assert once.
async function pollStatus(
  request: APIRequestContext,
  session: AuthSession,
  nodeId: string | undefined,
  want: string,
  timeoutMs = 60000,
): Promise<string> {
  const deadline = Date.now() + timeoutMs
  let last = ''
  while (Date.now() < deadline) {
    last = await realStatus(request, session, nodeId)
    if (last === want) return last
    await new Promise((r) => setTimeout(r, 1500))
  }
  return last
}

// createAndUp creates STACK on the source node from `yaml` and brings it up.
async function createAndUp(request: APIRequestContext, session: AuthSession, yaml: string) {
  const create = await request.post(`${baseURL}/api/v1/docker/compose`, {
    headers: session.headers,
    data: { name: STACK, yaml },
  })
  const createJson = await create.json()
  expect(createJson.success, `create failed: ${JSON.stringify(createJson)}`).toBe(true)

  const up = await request.post(`${baseURL}/api/v1/docker/compose/${STACK}/up`, {
    headers: session.headers,
  })
  const upJson = await up.json()
  expect(upJson.success, `up failed: ${JSON.stringify(upJson)}`).toBe(true)
}

// cleanupBoth best-effort removes STACK (with volumes) from source and target.
// Always swallow errors — teardown must not turn a passing test red, and a
// stack that never landed on the target simply 500s on delete there.
async function cleanupBoth(request: APIRequestContext, session: AuthSession, targetNodeId: string | null) {
  const del = async (nodeId?: string) => {
    const q = nodeId ? `?node=${nodeId}&` : '?'
    try {
      await request.delete(`${baseURL}/api/v1/docker/compose/${STACK}${q}removeVolumes=true`, {
        headers: session.headers,
      })
    } catch {
      // best-effort
    }
  }
  await del()
  if (targetNodeId) await del(targetNodeId)
}

// consumeMigrateSSE POSTs the migrate request and reads the SSE stream to its
// terminal event, returning the terminal phase ('done' | 'rollback' | 'error')
// and the full phase sequence. We run this inside the browser context with
// fetch + a stream reader — same rationale as cluster-remote-node.spec.ts using
// page.evaluate for WS: exercise the real streaming transport rather than
// buffering, and Playwright's APIRequestContext has no incremental body reader.
async function consumeMigrateSSE(
  page: import('@playwright/test').Page,
  session: AuthSession,
  targetNodeId: string,
  disposition: string,
): Promise<{ terminal: string; phases: string[]; lastMessage: string }> {
  return page.evaluate(
    async ({ url, headers, stack, targetNodeId, disposition }) => {
      const res = await fetch(`${url}/api/v1/docker/compose/${stack}/migrate`, {
        method: 'POST',
        headers: { ...headers },
        body: JSON.stringify({ targetNodeId, disposition }),
      })
      const phases: string[] = []
      let terminal = ''
      let lastMessage = ''
      const reader = res.body?.getReader()
      if (!reader) return { terminal: 'error', phases, lastMessage: 'no response body' }
      const decoder = new TextDecoder()
      let buffer = ''
      // The server marks the last event of a successful or failed run with
      // done:true and a terminal phase (done|rollback|error). Read frames until
      // we see one of those or the stream closes.
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        const frames = buffer.split('\n\n')
        buffer = frames.pop() ?? ''
        for (const frame of frames) {
          const line = frame.split('\n').find((l) => l.startsWith('data: '))
          if (!line) continue
          try {
            const ev = JSON.parse(line.slice('data: '.length)) as {
              phase: string
              message?: string
              done?: boolean
            }
            phases.push(ev.phase)
            lastMessage = ev.message ?? lastMessage
            if (ev.done || ev.phase === 'done' || ev.phase === 'rollback' || ev.phase === 'error') {
              terminal = ev.phase
            }
          } catch {
            // ignore non-JSON keepalive frames
          }
        }
        if (terminal) break
      }
      try {
        await reader.cancel()
      } catch {
        // already closed
      }
      return { terminal, phases, lastMessage }
    },
    {
      url: baseURL!,
      headers: session.headers,
      stack: STACK,
      targetNodeId,
      disposition,
    },
  )
}

test.describe('Stack node-to-node migration', () => {
  test.skip(!haveEnv, skipReason)

  test('happy path: retain migrates stack to target, leaves source stopped', async ({ page, request }) => {
    test.setTimeout(180000)
    const session = await login(request, { baseURL })
    const targetNodeId = await resolveTargetNodeId(request, session)
    expect(targetNodeId, 'cluster reported no second node — check PW_BASE_URL points at a live 2-node cluster').toBeTruthy()

    // Start clean in case a prior run left the stack behind.
    await cleanupBoth(request, session, targetNodeId)

    try {
      await createAndUp(request, session, whoamiCompose())
      expect(await pollStatus(request, session, undefined, 'running', 30000)).toBe('running')

      // Pre-flight: no hard blocks for a stateless single-port stack between
      // same-arch nodes. A port-conflict block would mean the target already
      // runs something on HOST_PORT — surface it rather than silently pass.
      const pf = await request.post(`${baseURL}/api/v1/docker/compose/${STACK}/migrate/preflight`, {
        headers: session.headers,
        data: { targetNodeId },
      })
      const pfJson = await pf.json()
      expect(pfJson.success, `preflight failed: ${JSON.stringify(pfJson)}`).toBe(true)
      expect(
        pfJson.data.blocks ?? [],
        `unexpected pre-flight blocks: ${JSON.stringify(pfJson.data.blocks)}`,
      ).toEqual([])

      // Migrate with disposition=retain and consume the SSE stream to terminal.
      const { terminal, phases, lastMessage } = await consumeMigrateSSE(page, session, targetNodeId!, 'retain')
      expect(
        terminal,
        `migration did not reach 'done' (phases=${phases.join('>')}, last="${lastMessage}")`,
      ).toBe('done')

      // Target now runs the stack; source is stopped (retain keeps source files
      // but leaves it down — see migrateFinalize/DispositionRetain).
      expect(await pollStatus(request, session, targetNodeId!, 'running', 90000)).toBe('running')
      expect(await pollStatus(request, session, undefined, 'stopped', 30000)).toBe('stopped')
    } finally {
      await cleanupBoth(request, session, targetNodeId)
    }
  })

  test('rollback: failing target up restores source and leaves no running stack on target', async ({ page, request }) => {
    test.setTimeout(180000)
    const session = await login(request, { baseURL })
    const targetNodeId = await resolveTargetNodeId(request, session)
    expect(targetNodeId, 'cluster reported no second node — check PW_BASE_URL points at a live 2-node cluster').toBeTruthy()

    await cleanupBoth(request, session, targetNodeId)

    try {
      // Image tag that will never pull → target-side `up` fails → orchestrator
      // rolls the source back to running before finalize.
      await createAndUp(request, session, whoamiCompose('traefik/whoami:nonexistent-tag-e2e'))
      // The source `up` itself may not fully run (bad tag), but the migration
      // path is what we're testing; proceed regardless of source run state.

      const { terminal, phases, lastMessage } = await consumeMigrateSSE(page, session, targetNodeId!, 'retain')
      // Terminal must be a failure signal — 'rollback' when the source was
      // restored, or 'error' if it aborted earlier (e.g. pre-flight/quiesce).
      expect(
        ['rollback', 'error'].includes(terminal),
        `expected rollback/error terminal, got '${terminal}' (phases=${phases.join('>')}, last="${lastMessage}")`,
      ).toBe(true)

      // The target must not be left with a running copy of the stack.
      const targetStatus = await realStatus(request, session, targetNodeId!)
      expect(
        targetStatus,
        `target left with running stack after failed migration (status=${targetStatus})`,
      ).not.toBe('running')

      // On rollback the source is restarted; tolerate non-running only when the
      // failure happened before quiesce (terminal 'error'), in which case the
      // source was never stopped. Either way the source must not be deleted.
      if (terminal === 'rollback') {
        const sourceStatus = await pollStatus(request, session, undefined, 'running', 30000)
        expect(['running', 'partial'].includes(sourceStatus), `source not restored after rollback (status=${sourceStatus})`).toBe(true)
      } else {
        expect(await realStatus(request, session, undefined)).not.toBe('')
      }
    } finally {
      await cleanupBoth(request, session, targetNodeId)
    }
  })
})
