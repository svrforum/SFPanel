import { describe, expect, it } from 'vitest'
import i18next, { type TFunction } from 'i18next'
import en from '@/i18n/locales/en.json'
import ko from '@/i18n/locales/ko.json'
import type { RunNodeRecord, RunRecord } from '@/types/api'
import {
  INSTALL_STEP_COUNT,
  cleanupCommand,
  clockText,
  credentialsGone,
  credentialsLeft,
  installBanner,
  installReasonText,
  installRowActions,
  installRowName,
  installRunReasonText,
  isInstallRun,
  needsCleanup,
  reviewRows,
  safeCheckURL,
} from './installRun'

async function translator(lng: 'en' | 'ko'): Promise<TFunction> {
  const i18n = i18next.createInstance()
  await i18n.init({ lng, resources: { en: { translation: en }, ko: { translation: ko } }, interpolation: { escapeValue: false } })
  return i18n.t.bind(i18n) as TFunction
}

const running = (over: Partial<RunNodeRecord>): RunNodeRecord => ({ status: 'running', ...over })

describe('safeCheckURL', () => {
  it('links an https address, and says its host', () => {
    expect(safeCheckURL('https://login.example/a/x')).toEqual({ href: 'https://login.example/a/x', host: 'login.example' })
    expect(safeCheckURL('https://login.example:8443/a?b=c')).toEqual({ href: 'https://login.example:8443/a?b=c', host: 'login.example:8443' })
  })

  it('links nothing else', () => {
    for (const url of [
      'http://login.example/a/x',
      'javascript:alert(1)',
      'JAVASCRIPT:alert(1)',
      'data:text/html,<b>x</b>',
      '/a/x',
      '//login.example/a/x',
      'login.example/a/x',
      'https://',
      'https://user:pw@evil.example/a',
      'https://login.example@evil.example/a',
      '',
      undefined,
    ]) {
      expect(safeCheckURL(url), String(url)).toBeNull()
    }
  })
})

describe('credentialsLeft', () => {
  const until = '2026-11-02T10:10:00Z'
  const at = (iso: string) => Date.parse(iso)

  it('counts the seconds until the credentials go', () => {
    expect(credentialsLeft(until, at('2026-11-02T10:00:00Z'))).toBe(600)
    expect(credentialsLeft(until, at('2026-11-02T10:09:59.400Z'))).toBe(1)
  })

  it('is 0 once they are gone', () => {
    expect(credentialsLeft(until, at('2026-11-02T10:10:00Z'))).toBe(0)
    expect(credentialsLeft(until, at('2026-11-02T11:00:00Z'))).toBe(0)
  })

  it('is null when the run keeps no deadline', () => {
    expect(credentialsLeft(undefined, at('2026-11-02T10:00:00Z'))).toBeNull()
    expect(credentialsLeft('', at('2026-11-02T10:00:00Z'))).toBeNull()
    expect(credentialsLeft('soon', at('2026-11-02T10:00:00Z'))).toBeNull()
  })
})

describe('cleanupCommand', () => {
  it("removes SFPanel with the installed version's own script", () => {
    expect(cleanupCommand('0.81.0')).toBe('curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/v0.81.0/scripts/install.sh | sudo bash -s -- uninstall --purge')
  })

  it("uses main's script for a version that is no release tag, and never pastes the value into the shell", () => {
    for (const v of ['', '0.81.0-dev', 'v0.81.0', '0.81.0; rm -rf /', '$(id)']) {
      expect(cleanupCommand(v), v).toBe('curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | sudo bash -s -- uninstall --purge')
    }
  })
})

describe('installBanner', () => {
  it('shows the whole host key to compare', () => {
    expect(installBanner(running({ awaiting: 'host_key', host_key: 'ED25519 SHA256:AbC+/123' }))).toEqual({ kind: 'host_key', hostKey: 'ED25519 SHA256:AbC+/123' })
  })

  it('links the Tailscale check only when the address is https', () => {
    expect(installBanner(running({ awaiting: 'check', check_url: 'https://login.example/a/x' }))).toEqual({
      kind: 'check',
      link: { href: 'https://login.example/a/x', host: 'login.example' },
    })
    expect(installBanner(running({ awaiting: 'check', check_url: 'javascript:alert(1)' }))).toEqual({ kind: 'check', link: null })
    expect(installBanner(running({ awaiting: 'check' }))).toEqual({ kind: 'check', link: null })
  })

  it('asks about a standalone panel and the review', () => {
    expect(installBanner(running({ awaiting: 'standalone' }))).toEqual({ kind: 'standalone' })
    expect(installBanner(running({ awaiting: 'review' }))).toEqual({ kind: 'review' })
  })

  it('shows nothing for a row not waiting, a row that ended, a question it cannot answer or one it does not know', () => {
    expect(installBanner(running({}))).toBeNull()
    expect(installBanner(undefined)).toBeNull()
    expect(installBanner({ status: 'failed', awaiting: 'review' })).toBeNull()
    expect(installBanner(running({ awaiting: 'host_key' }))).toBeNull()
    expect(installBanner(running({ awaiting: 'captcha' as RunNodeRecord['awaiting'] }))).toBeNull()
  })
})

describe('installRowName', () => {
  it('is the name asked for, else the host as given, else the row key', () => {
    expect(installRowName('s1', { status: 'pending', host: '192.0.2.21', name: 'web-1' })).toBe('web-1')
    expect(installRowName('s2', { status: 'pending', host: 'peer-node.example.ts.net' })).toBe('peer-node.example.ts.net')
    expect(installRowName('s3', { status: 'pending' })).toBe('s3')
    expect(installRowName('s4', undefined)).toBe('s4')
  })
})

describe('isInstallRun', () => {
  const head = (kind?: RunRecord['kind']): RunRecord => ({
    id: 'r', task_id: 'builtin/agent-install', task_name: 'x', rev: '', nodes: [], waves: [], batch: 3,
    stop_on_failure: false, status: 'running', started_by: 'admin', started_at: '2026-11-02T10:00:00Z', kind,
  })

  it('is an install by its kind', () => {
    expect(isInstallRun(head('agent-install'))).toBe(true)
    expect(isInstallRun(head())).toBe(false)
    expect(INSTALL_STEP_COUNT).toBe(5)
  })
})

// Every reason a row can end on (internal/feature/agentinstall), with the
// details its return sites send.
const REASONS: [string, Record<string, string> | undefined][] = [
  ['AGENT_INSTALL_UNREACHABLE', { host: 'peer-node.example' }],
  ['AGENT_INSTALL_UNREACHABLE', { address: '192.0.2.21:22' }],
  ['AGENT_INSTALL_AUTH_FAILED', undefined],
  ['AGENT_INSTALL_AUTH_FAILED', { check: 'timeout' }],
  ['AGENT_INSTALL_HOST_KEY_CHANGED', { host_key: 'ED25519 SHA256:AbC' }],
  ['AGENT_INSTALL_NOT_ROOT', { sudo: 'none' }],
  ['AGENT_INSTALL_NOT_ROOT', { sudo: 'password' }],
  ['AGENT_INSTALL_NOT_ROOT', { sudo: 'refused' }],
  ['AGENT_INSTALL_NOT_ROOT', { sudo: 'later' }],
  ['AGENT_INSTALL_OS', { os_id: 'fedora', os_like: '' }],
  ['AGENT_INSTALL_OS', { os_id: '', os_like: '' }],
  ['AGENT_INSTALL_ARCH', { arch: 'armv7l' }],
  ['AGENT_INSTALL_NO_SYSTEMD', undefined],
  ['AGENT_INSTALL_NO_TOOLS', { missing: 'bash,curl' }],
  ['AGENT_INSTALL_DISK', { free_kb: '20480', need_kb: '204800' }],
  ['AGENT_INSTALL_PORT_BUSY', { port: '3628', owner: 'nginx' }],
  ['AGENT_INSTALL_PORT_BUSY', { port: '3629', owner: '?' }],
  ['AGENT_INSTALL_CLOCK', { skew_seconds: '-95' }],
  ['AGENT_INSTALL_CANNOT_REACH_MANAGER', { manager: '192.0.2.10:3629' }],
  ['AGENT_INSTALL_IP_NOT_LOCAL', { ip: '192.0.2.21' }],
  ['AGENT_INSTALL_NO_INTERNET', undefined],
  ['AGENT_INSTALL_ALREADY_AGENT', undefined],
  ['AGENT_INSTALL_ALREADY_MANAGER', undefined],
  ['AGENT_INSTALL_MANAGER_FILES', { files: '/var/lib/sfpanel/cluster/ca.key,/var/lib/sfpanel/cluster/raft' }],
  ['AGENT_INSTALL_OPERATOR_TLS', undefined],
  ['AGENT_INSTALL_NEWER_INSTALLED', { installed: '0.82.0', version: '0.81.0' }],
  ['AGENT_INSTALL_NEWER_INSTALLED', { installed: '', version: '0.81.0' }],
  ['AGENT_INSTALL_NAME_INVALID', { name: 'web 1' }],
  ['AGENT_INSTALL_NAME_INVALID', { hostname: 'localhost.localdomain_' }],
  ['AGENT_INSTALL_NAME_INVALID', { hostname: '' }],
  ['AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'proxy_rotation' }],
  ['AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'ca_rotation' }],
  ['AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'managers' }],
  ['AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'max_agents' }],
  ['AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'ca_key' }],
  ['AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'other' }],
  ['AGENT_INSTALL_IDENTITY_TAKEN', { name: 'web-1', ip: '192.0.2.21' }],
  ['AGENT_INSTALL_IDENTITY_TAKEN', { ip: '192.0.2.21', row: 's1' }],
  ['AGENT_INSTALL_IDENTITY_TAKEN', { name: 'web-1', row: 's2' }],
  ['AGENT_INSTALL_FAILED', { step: 'ssh' }],
  ['AGENT_INSTALL_FAILED', { step: 'login_check', exit: '127' }],
  ['AGENT_INSTALL_FAILED', { step: 'probe', exit: '2' }],
  ['AGENT_INSTALL_FAILED', { step: 'checksums' }],
  ['AGENT_INSTALL_FAILED', { step: 'archive' }],
  ['AGENT_INSTALL_FAILED', { step: 'ca_pin' }],
  ['AGENT_INSTALL_FAILED', { step: 'mktemp', exit: '1' }],
  ['AGENT_INSTALL_FAILED', { step: 'systemd-run', exit: '1' }],
  ['AGENT_INSTALL_FAILED', { step: 'install', exit: '3' }],
  ['AGENT_INSTALL_FAILED', { step: 'install', timeout_minutes: '30' }],
  ['AGENT_INSTALL_FAILED', { step: 'register', ip: '192.0.2.21' }],
  ['AGENT_INSTALL_FAILED', { step: 'labels' }],
  ['AGENT_INSTALL_FAILED', { step: 'something-new' }],
  ['AGENT_INSTALL_FAILED', { no_answer: 'host_key' }],
  ['AGENT_INSTALL_FAILED', { no_answer: 'standalone' }],
  ['AGENT_INSTALL_FAILED', { no_answer: 'review' }],
  ['AGENT_INSTALL_FAILED', { error: 'context deadline exceeded' }],
  ['AGENT_INSTALL_FAILED', { mode: 'sideways' }],
  ['AGENT_INSTALL_FAILED', undefined],
  ['AGENT_INSTALL_OFFLINE', { cause: 'agent_to_managers', port: '3629' }],
  ['AGENT_INSTALL_OFFLINE', { cause: 'version', version: '0.80.0' }],
  ['AGENT_INSTALL_OFFLINE', { cause: 'managers_to_agent', ports: '3629,3628', failed: 'relay,panel' }],
  ['AGENT_INSTALL_OFFLINE', { cause: 'managers_to_agent', ports: '', failed: 'panel' }],
  ['AGENT_INSTALL_OFFLINE', undefined],
  ['AGENT_INSTALL_LEADER_CHANGED', undefined],
  ['AGENT_INSTALL_CREDENTIALS_GONE', undefined],
  ['AGENT_INSTALL_CREDENTIALS_GONE', { reverted: 'removed' }],
  // A reverted row keeps its reason and gains reverted.
  ['AGENT_INSTALL_OFFLINE', { cause: 'agent_to_managers', port: '3629', reverted: 'removing' }],
  ['AGENT_INSTALL_OFFLINE', { cause: 'agent_to_managers', port: '3629', reverted: 'purged' }],
  ['AGENT_INSTALL_OFFLINE', { cause: 'agent_to_managers', port: '3629', reverted: 'cleanup_failed' }],
  ['AGENT_INSTALL_OFFLINE', { cause: 'agent_to_managers', port: '3629', reverted: 'removed', standalone: '1' }],
]

describe('installReasonText', () => {
  it.each(['en', 'ko'] as const)('words every row reason in %s, with nothing left unfilled', async (lng) => {
    const t = await translator(lng)
    for (const [reason, details] of REASONS) {
      const text = installReasonText({ status: 'failed', reason, reason_details: details }, t)
      const what = `${reason} ${JSON.stringify(details)}`
      expect(text, what).not.toBeNull()
      expect(text, what).not.toContain('AGENT_INSTALL')
      expect(text, what).not.toMatch(/cluster\.tasks|\{\{/)
      if (lng === 'en') expect(text, what).not.toMatch(/[가-힣]/)
    }
  })

  it('says the values the server sent', async () => {
    const t = await translator('en')
    const word = (reason: string, details?: Record<string, string>, over: Partial<RunNodeRecord> = {}) =>
      installReasonText({ status: 'failed', reason, reason_details: details, ...over }, t) ?? ''
    expect(word('AGENT_INSTALL_PORT_BUSY', { port: '3628', owner: 'nginx' })).toContain('3628')
    expect(word('AGENT_INSTALL_PORT_BUSY', { port: '3628', owner: 'nginx' })).toContain('nginx')
    expect(word('AGENT_INSTALL_PORT_BUSY', { port: '3628', owner: '?' })).toContain('another program')
    expect(word('AGENT_INSTALL_CLOCK', { skew_seconds: '-95' })).toContain('95 seconds')
    // A detail sent empty reads as one not sent: a server with no os-release.
    expect(word('AGENT_INSTALL_OS', { os_id: '', os_like: '' })).toContain('runs another system.')
    expect(word('AGENT_INSTALL_DISK', { free_kb: '20480', need_kb: '204800' })).toContain('20 MiB free, 200 MiB needed')
    expect(word('AGENT_INSTALL_NO_TOOLS', { missing: 'bash,curl' })).toContain('bash, curl')
    expect(word('AGENT_INSTALL_NOT_ROOT', { sudo: 'refused' })).toContain('sudo refused the sudo password')
    expect(word('AGENT_INSTALL_NOT_ROOT', { sudo: 'none' })).toContain('no sudo')
    expect(word('AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'max_agents' })).toContain('as many agents as it takes')
    expect(word('AGENT_INSTALL_IDENTITY_TAKEN', { ip: '192.0.2.21', row: 's1' })).toContain('server 1')
    expect(word('AGENT_INSTALL_FAILED', { step: 'install', exit: '3' })).toContain('exit code 3')
    expect(word('AGENT_INSTALL_FAILED', { no_answer: 'host_key' })).toContain('host key')
    expect(word('AGENT_INSTALL_OFFLINE', { cause: 'managers_to_agent', ports: '3629,3628', failed: 'relay' })).toContain('3629, 3628')
    expect(word('AGENT_INSTALL_NAME_INVALID', { hostname: 'bad_host_' })).toContain('bad_host_')
    expect(word('AGENT_INSTALL_UNREACHABLE', { host: 'peer-node.example' })).toContain('peer-node.example')
    // A registered row lost to a leader change is an agent: the node page shows it.
    expect(word('AGENT_INSTALL_LEADER_CHANGED', undefined, { node_id: 'n1' })).toContain('node page')
    expect(word('AGENT_INSTALL_LEADER_CHANGED')).toContain('cleanup command')
    // A revert says how far it went.
    expect(word('AGENT_INSTALL_OFFLINE', { cause: 'version', version: '0.80.0', reverted: 'purged' })).toContain('SFPanel was removed from the server')
    expect(word('AGENT_INSTALL_OFFLINE', { cause: 'version', version: '0.80.0', reverted: 'removed', standalone: '1' })).toContain('standalone panel')
  })

  it('reads in Korean with the particles written for both endings', async () => {
    const t = await translator('ko')
    expect(installReasonText({ status: 'failed', reason: 'AGENT_INSTALL_PORT_BUSY', reason_details: { port: '3628', owner: 'nginx' } }, t)).toBe(
      '포트 3628을(를) nginx이(가) 쓰고 있습니다. 에이전트에는 3628·3629 포트가 필요하니 그 프로그램을 멈추거나 다른 포트로 옮긴 뒤 다시 설치하세요.',
    )
  })

  it('is null for a row with no reason, and the code as sent for one it does not know', async () => {
    const t = await translator('en')
    expect(installReasonText({ status: 'running' }, t)).toBeNull()
    expect(installReasonText(undefined, t)).toBeNull()
    expect(installReasonText({ status: 'failed', reason: 'AGENT_INSTALL_SOMETHING_NEW' }, t)).toBe('AGENT_INSTALL_SOMETHING_NEW')
  })
})

// ---- The progress view ----

const head = (over: Partial<RunRecord> = {}): RunRecord => ({
  id: 'i-1', kind: 'agent-install', task_id: 'builtin/agent-install', task_name: '에이전트 설치 (SSH)', rev: '',
  nodes: ['s1', 's2'], waves: [['s1', 's2']], batch: 2, stop_on_failure: false,
  status: 'failed', started_by: 'admin', started_at: '2026-11-01T12:00:00Z', ended_at: '2026-11-01T12:05:00Z', ...over,
})
const phases = (...states: string[]) => states.map((status) => ({ status })) as RunNodeRecord['steps']
// A registered server whose connection check failed, not taken back.
const offline = (over: Partial<RunNodeRecord> = {}): RunNodeRecord => ({
  status: 'failed', node_id: 'a-10', reason: 'AGENT_INSTALL_OFFLINE', reason_details: { cause: 'agent_to_managers', port: '3629' }, ...over,
})

describe('clockText', () => {
  it('is minutes and two-digit seconds', () => {
    expect(clockText(600)).toBe('10:00')
    expect(clockText(65)).toBe('1:05')
    expect(clockText(5)).toBe('0:05')
    expect(clockText(0)).toBe('0:00')
  })
})

describe('credentialsGone', () => {
  const now = Date.parse('2026-11-01T12:10:00Z')
  it('follows the deadline while there is one', () => {
    expect(credentialsGone(head({ credentials_until: '2026-11-01T12:10:01Z' }), now)).toBe(false)
    expect(credentialsGone(head({ credentials_until: '2026-11-01T12:10:00Z' }), now)).toBe(true)
  })

  it('is gone for an ended install with no deadline, and not for one under way', () => {
    expect(credentialsGone(head(), now)).toBe(true)
    expect(credentialsGone(head({ status: 'stopped' }), now)).toBe(true)
    for (const status of ['running', 'stopping', 'handing_over'] as const) {
      expect(credentialsGone(head({ status, ended_at: undefined }), now)).toBe(false)
    }
  })
})

describe('needsCleanup', () => {
  it('is a registered server left as it failed, once the credentials are gone', () => {
    expect(needsCleanup(offline(), true)).toBe(true)
    expect(needsCleanup(offline(), false)).toBe(false)
    // Not registered: the install cleaned up after itself over SSH.
    expect(needsCleanup(offline({ node_id: undefined }), true)).toBe(false)
    expect(needsCleanup({ status: 'succeeded', node_id: 'a-10' }, true)).toBe(false)
    expect(needsCleanup({ status: 'skipped' }, true)).toBe(false)
    expect(needsCleanup(undefined, true)).toBe(false)
  })

  it('follows how far a revert went', () => {
    const reverted = (d: Record<string, string>) => offline({ reason_details: { cause: 'agent_to_managers', ...d } })
    expect(needsCleanup(reverted({ reverted: 'cleanup_failed' }), false)).toBe(true)
    expect(needsCleanup(reverted({ reverted: 'removed' }), true)).toBe(true)
    // A standalone panel before: its account is gone, and it is left as it is.
    expect(needsCleanup(reverted({ reverted: 'removed', standalone: '1' }), true)).toBe(false)
    expect(needsCleanup(reverted({ reverted: 'purged' }), true)).toBe(false)
    expect(needsCleanup(reverted({ reverted: 'removing' }), true)).toBe(false)
  })

  it('is a server a leader change left mid-install, not one it never reached', () => {
    const left = (steps: RunNodeRecord['steps'], over: Partial<RunNodeRecord> = {}): RunNodeRecord =>
      ({ status: 'failed', reason: 'AGENT_INSTALL_LEADER_CHANGED', steps, ...over })
    expect(needsCleanup(left(phases('succeeded', 'succeeded', 'unknown')), false)).toBe(true)
    expect(needsCleanup(left(phases('succeeded', 'running')), false)).toBe(false)
    expect(needsCleanup(left(phases('pending', 'pending', 'pending')), false)).toBe(false)
    expect(needsCleanup(left(undefined), false)).toBe(false)
    // Registered: an agent the node page shows.
    expect(needsCleanup(left(phases('succeeded', 'succeeded', 'succeeded', 'succeeded', 'unknown'), { node_id: 'a-10' }), true)).toBe(false)
  })
})

describe('installRowActions', () => {
  const none = { recheck: false, revert: false, openNode: false }
  it('offers Recheck and Revert to a registered server that failed, not yet taken back', () => {
    expect(installRowActions(head(), offline())).toEqual({ recheck: true, revert: true, openNode: false })
    // The engine refuses a recheck once the install was stopped; a revert still removes the node.
    expect(installRowActions(head({ status: 'stopped' }), offline())).toEqual({ recheck: false, revert: true, openNode: false })
  })

  it('offers nothing to a server that did not register, or was taken back', () => {
    expect(installRowActions(head(), offline({ node_id: undefined }))).toEqual(none)
    expect(installRowActions(head(), offline({ reason_details: { reverted: 'purged' } }))).toEqual(none)
    expect(installRowActions(head(), { status: 'running', node_id: 'a-10' })).toEqual(none)
    expect(installRowActions(head(), undefined)).toEqual(none)
  })

  it('offers nothing to undo once the leader that held the install changed', () => {
    expect(installRowActions(head({ reason: 'AGENT_INSTALL_LEADER_CHANGED' }), offline())).toEqual(none)
    expect(installRowActions(head(), offline({ reason: 'AGENT_INSTALL_LEADER_CHANGED', reason_details: undefined })))
      .toEqual({ ...none, openNode: true })
  })

  it('opens the node of a server that succeeded', () => {
    expect(installRowActions(head(), { status: 'succeeded', node_id: 'a-1' })).toEqual({ ...none, openNode: true })
    expect(installRowActions(head(), { status: 'succeeded' })).toEqual(none)
  })
})

describe('reviewRows', () => {
  it('lists the servers waiting for the review, in run order', () => {
    const nodes = {
      s1: running({ awaiting: 'review' }),
      s2: running({ awaiting: 'host_key', host_key: 'ED25519 SHA256:x' }),
      s3: { status: 'failed', awaiting: 'review' } as RunNodeRecord,
      s4: running({ awaiting: 'review' }),
    }
    expect(reviewRows(head({ nodes: ['s4', 's1', 's2', 's3'] }), nodes)).toEqual(['s4', 's1'])
  })
})

describe('installRunReasonText', () => {
  it('words the run reason a leader change leaves, and shows any other as sent', async () => {
    const t = await translator('en')
    expect(installRunReasonText('AGENT_INSTALL_LEADER_CHANGED', t)).toMatch(/^The cluster leader changed during the install/)
    expect(installRunReasonText('stopped by admin', t)).toBe('stopped by admin')
    expect(installRunReasonText(undefined, t)).toBeNull()
    const ko = await translator('ko')
    expect(installRunReasonText('AGENT_INSTALL_LEADER_CHANGED', ko)).toMatch(/^설치 도중 클러스터 리더가 바뀌어/)
  })
})
