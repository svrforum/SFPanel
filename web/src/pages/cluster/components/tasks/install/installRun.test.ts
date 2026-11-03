import { describe, expect, it } from 'vitest'
import i18next, { type TFunction } from 'i18next'
import en from '@/i18n/locales/en.json'
import ko from '@/i18n/locales/ko.json'
import type { RunNodeRecord, RunRecord } from '@/types/api'
import {
  INSTALL_STEP_COUNT,
  PANEL_START_COMMAND,
  cleanupCommand,
  clockText,
  credentialsGone,
  credentialsLeft,
  installBanner,
  installReasonText,
  installReview,
  installRowActions,
  installRowName,
  installRunReasonText,
  isInstallRun,
  keptPanelNote,
  needsCleanup,
  rowActionErrorText,
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
    expect(cleanupCommand('0.81.0')).toBe('curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/v0.81.0/scripts/install.sh | sudo bash -s -- uninstall --purge; sudo rm -rf /tmp/sfpanel-agent.*')
  })

  it("uses main's script for a version that is no release tag, and never pastes the value into the shell", () => {
    for (const v of ['', '0.81.0-dev', 'v0.81.0', '0.81.0; rm -rf /', '$(id)']) {
      expect(cleanupCommand(v), v).toBe('curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | sudo bash -s -- uninstall --purge; sudo rm -rf /tmp/sfpanel-agent.*')
    }
  })
})

describe('installBanner', () => {
  it('shows the whole host key to compare', () => {
    expect(installBanner(running({ awaiting: 'host_key', host_key: 'ED25519 SHA256:AbC+/123' }))).toEqual({ kind: 'host_key', hostKey: 'ED25519 SHA256:AbC+/123' })
  })

  it("carries Tailscale's comparison of the key, and nothing for a value it does not know", () => {
    const asked = (host_key_tailscale: unknown) =>
      installBanner(running({ awaiting: 'host_key', host_key: 'ED25519 SHA256:x', host_key_tailscale } as Partial<RunNodeRecord>))
    expect(asked('match')).toStrictEqual({ kind: 'host_key', hostKey: 'ED25519 SHA256:x', tailscale: 'match' })
    expect(asked('mismatch')).toStrictEqual({ kind: 'host_key', hostKey: 'ED25519 SHA256:x', tailscale: 'mismatch' })
    expect(asked(undefined)).toStrictEqual({ kind: 'host_key', hostKey: 'ED25519 SHA256:x' })
    expect(asked('unsure')).toStrictEqual({ kind: 'host_key', hostKey: 'ED25519 SHA256:x' })
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

  // A revert's cleanup dials the server again on a row that failed, and its
  // Tailscale SSH may ask for a check there.
  it('asks for a Tailscale check whatever the row status', () => {
    expect(installBanner({ status: 'failed', awaiting: 'check', check_url: 'https://login.example/a/x' })).toEqual({
      kind: 'check',
      link: { href: 'https://login.example/a/x', host: 'login.example' },
    })
    expect(installBanner({ status: 'stopped', awaiting: 'check' })).toEqual({ kind: 'check', link: null })
  })

  it('shows nothing for a row not waiting, a row that ended, a question it cannot answer or one it does not know', () => {
    expect(installBanner(running({}))).toBeNull()
    expect(installBanner(undefined)).toBeNull()
    expect(installBanner({ status: 'failed', awaiting: 'review' })).toBeNull()
    expect(installBanner({ status: 'failed', awaiting: 'host_key', host_key: 'ED25519 SHA256:x' })).toBeNull()
    expect(installBanner({ status: 'failed', awaiting: 'standalone' })).toBeNull()
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
  ['AGENT_INSTALL_UNREACHABLE', { address: '100.64.0.31:22', tailscale: 'leader_none', managers: 'm2,m3' }],
  ['AGENT_INSTALL_UNREACHABLE', { address: '100.64.0.31:22', tailscale: 'leader_none', managers: '' }],
  ['AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'join_address' }],
  ['AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'join_address', tailscale: 'leader_none', managers: 'm2' }],
  ['AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'join_address', tailscale: 'leader_none', managers: '' }],
  ['AGENT_INSTALL_UNREACHABLE', { host: 'peer-node.example' }],
  ['AGENT_INSTALL_UNREACHABLE', { address: '192.0.2.21:22' }],
  ['AGENT_INSTALL_AUTH_FAILED', undefined],
  ['AGENT_INSTALL_AUTH_FAILED', { check: 'timeout' }],
  ['AGENT_INSTALL_HOST_KEY_CHANGED', { host_key: 'ED25519 SHA256:AbC', approved: 'ED25519 SHA256:XyZ' }],
  ['AGENT_INSTALL_HOST_KEY_CHANGED', { approved: 'ED25519 SHA256:XyZ' }],
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
  ['AGENT_INSTALL_OPERATOR_TLS', { ca_file: '1' }],
  ['AGENT_INSTALL_OPERATOR_TLS', { ca_file: '0' }],
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
  ['AGENT_INSTALL_CREDENTIALS_GONE', { registered: '1', reverted: 'removed' }],
  ['AGENT_INSTALL_STOPPED', { registered: '1' }],
  ['AGENT_INSTALL_STOPPED', { registered: '1', reverted: 'removed', standalone: '1' }],
  ['AGENT_INSTALL_LABEL_FAILED', { group: 'web' }],
  ['AGENT_INSTALL_LOGIN_OFF_NETWORK', { address: '203.0.113.9' }],
  ['AGENT_INSTALL_LOGIN_OFF_NETWORK', { address: '' }],
  ['AGENT_INSTALL_LOGIN_OFF_NETWORK', { address: '100.64.0.31', tailnet: 'not_listed' }],
  ['AGENT_INSTALL_LOGIN_OFF_NETWORK', { address: '100.64.0.31', tailnet: 'unknown' }],
  ['AGENT_INSTALL_FAILED', { step: 'install', exit: '1', panel: 'restarted' }],
  ['AGENT_INSTALL_FAILED', { step: 'install', exit: '1', panel: 'restart_failed' }],
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
    // A revert says how far it went.
    expect(word('AGENT_INSTALL_OFFLINE', { cause: 'version', version: '0.80.0', reverted: 'purged' })).toContain('SFPanel was removed from the server')
    expect(word('AGENT_INSTALL_OFFLINE', { cause: 'version', version: '0.80.0', reverted: 'removed', standalone: '1' })).toContain('standalone panel')
  })

  it('tells a server at a Tailscale address the leader is not on which managers the leadership can go to', async () => {
    const t = await translator('en')
    const word = (reason: string, details: Record<string, string>) =>
      installReasonText({ status: 'failed', reason, reason_details: details }, t) ?? ''
    const out = word('AGENT_INSTALL_UNREACHABLE', { address: '100.64.0.31:22', tailscale: 'leader_none', managers: 'm2,m3' })
    expect(out).toContain('100.64.0.31:22 is a Tailscale address, and the leader is not on Tailscale')
    expect(out).toContain('(m2, m3)')
    expect(word('AGENT_INSTALL_UNREACHABLE', { address: '100.64.0.31:22', tailscale: 'leader_none', managers: '' })).toContain(
      'no manager of this cluster is on Tailscale',
    )
    expect(word('AGENT_INSTALL_UNREACHABLE', { address: '192.0.2.31:22' })).toContain('cannot open SSH to 192.0.2.31:22')
    const join = word('AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'join_address', tailscale: 'leader_none', managers: 'm2' })
    expect(join).toContain('no address of the leader to join at')
    expect(join).toContain('(m2)')
    expect(word('AGENT_INSTALL_CLUSTER_NOT_READY', { gate: 'join_address' })).toContain("no address on this server's network")
    const ko = await translator('ko')
    expect(
      installReasonText(
        { status: 'failed', reason: 'AGENT_INSTALL_UNREACHABLE', reason_details: { address: '100.64.0.31:22', tailscale: 'leader_none', managers: 'm2,m3' } },
        ko,
      ),
    ).toBe('100.64.0.31:22은(는) Tailscale 주소인데 리더가 Tailscale에 없습니다. 노드 화면에서 Tailscale에 있는 매니저(m2, m3)로 리더십을 이전한 뒤 다시 설치하세요.')
  })

  it("says a saved login's password goes only to private and tailnet servers, and to type the account for this one", async () => {
    const t = await translator('en')
    const off = (details: Record<string, string>) => installReasonText({ status: 'failed', reason: 'AGENT_INSTALL_LOGIN_OFF_NETWORK', reason_details: details }, t)
    expect(off({ address: '203.0.113.9' })).toBe(
      "A saved login's password goes only to servers on a private network or in the leader's tailnet, and 203.0.113.9 is on neither. Install this server with the account typed in.",
    )
    expect(off({ address: '' })).toContain('and the server is on neither')
    const ko = await translator('ko')
    expect(installReasonText({ status: 'failed', reason: 'AGENT_INSTALL_LOGIN_OFF_NETWORK', reason_details: { address: '203.0.113.9' } }, ko)).toBe(
      '저장된 로그인의 비밀번호는 사설망이나 리더의 tailnet에 있는 서버에만 보냅니다. 이 서버(203.0.113.9)는 둘 다 아니니 계정을 직접 입력해 설치하세요.',
    )
  })

  // A Tailscale address says what the leader's tailnet said of it.
  it("says a Tailscale address the leader's tailnet does not list, and one it could not check, each with its address", async () => {
    const off = (t: TFunction, tailnet: string) =>
      installReasonText({ status: 'failed', reason: 'AGENT_INSTALL_LOGIN_OFF_NETWORK', reason_details: { address: '100.64.0.31', tailnet } }, t)
    const en = await translator('en')
    expect(off(en, 'not_listed')).toBe(
      "A saved login's password goes only to servers on a private network or in the leader's tailnet. The leader's tailnet has no device at 100.64.0.31. Check the address, or install this server with the account typed in.",
    )
    expect(off(en, 'unknown')).toBe(
      'The leader could not check its tailnet (Tailscale is off or not answering), so the saved login was not sent to 100.64.0.31. Transfer the leadership to a manager with Tailscale on, then install again.',
    )
    // A value a newer leader may send reads as the code's own sentence.
    expect(off(en, 'sideways')).toBe(
      "A saved login's password goes only to servers on a private network or in the leader's tailnet, and 100.64.0.31 is on neither. Install this server with the account typed in.",
    )
    const ko = await translator('ko')
    expect(off(ko, 'not_listed')).toBe(
      '저장된 로그인의 비밀번호는 사설망이나 리더의 tailnet에 있는 서버에만 보냅니다. 리더의 tailnet에는 100.64.0.31인 기기가 없습니다. 주소를 확인하거나 계정을 직접 입력해 설치하세요.',
    )
    expect(off(ko, 'unknown')).toBe(
      '리더가 자기 tailnet을 확인하지 못해(Tailscale이 꺼져 있거나 응답하지 않음) 저장된 로그인을 100.64.0.31에 보내지 않았습니다. Tailscale이 켜진 매니저로 리더를 옮긴 뒤 다시 설치하세요.',
    )
  })

  it('reads in Korean with the particles written for both endings', async () => {
    const t = await translator('ko')
    expect(installReasonText({ status: 'failed', reason: 'AGENT_INSTALL_PORT_BUSY', reason_details: { port: '3628', owner: 'nginx' } }, t)).toBe(
      '포트 3628을(를) nginx이(가) 쓰고 있습니다. 에이전트에는 3628·3629 포트가 필요하니 그 프로그램을 멈추거나 다른 포트로 옮긴 뒤 다시 설치하세요.',
    )
  })

  it('says a registered server that was stopped stays in the cluster until it is reverted', async () => {
    const t = await translator('en')
    expect(installReasonText({ status: 'stopped', node_id: 'a-1', reason: 'AGENT_INSTALL_STOPPED', reason_details: { registered: '1' } }, t)).toBe(
      'The install was stopped after this server registered as an agent. It stays in the cluster until you revert it.',
    )
  })

  it('names the group a succeeded agent could not be given, to set on the node page', async () => {
    const t = await translator('en')
    const text = installReasonText({ status: 'succeeded', node_id: 'a-1', reason: 'AGENT_INSTALL_LABEL_FAILED', reason_details: { group: 'web' } }, t)
    expect(text).toContain('group web')
    expect(text).toContain('node page')
  })

  it('names the key the server now presents and the one approved, each in full', async () => {
    const presented = 'ED25519 SHA256:Pr3sentedKeyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
    const approved = 'ED25519 SHA256:ApprovedKeyBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB'
    const changed = (d: Record<string, string>): RunNodeRecord => ({ status: 'failed', reason: 'AGENT_INSTALL_HOST_KEY_CHANGED', reason_details: d })
    for (const lng of ['en', 'ko'] as const) {
      const t = await translator(lng)
      const both = installReasonText(changed({ host_key: presented, approved }), t) ?? ''
      expect(both, lng).toContain(presented)
      expect(both, lng).toContain(approved)
      // The presented key comes first: it is what the server shows now.
      expect(both.indexOf(presented), lng).toBeLessThan(both.indexOf(approved))
      // No key of the approved type: only the approved one is named.
      const none = installReasonText(changed({ approved }), t) ?? ''
      expect(none, lng).toContain(approved)
      expect(none, lng).not.toContain(presented)
    }
    const en = await translator('en')
    expect(installReasonText(changed({ host_key: presented, approved }), en)).toBe(
      `The server now presents the host key ${presented}, not the approved ${approved}. Another machine may be answering at this address: check the key on the server itself before you install it again.`,
    )
  })

  it("asks a panel with its own certificate to let the panel manage one, or to be joined by hand", async () => {
    const t = await translator('en')
    for (const ca of ['0', '1']) {
      const text = installReasonText({ status: 'failed', reason: 'AGENT_INSTALL_OPERATOR_TLS', reason_details: { ca_file: ca } }, t) ?? ''
      expect(text).toContain('serves its own certificate')
      expect(text).toContain('server.tls.cert_file, key_file and ca_file')
      expect(text).toContain('join the server by hand')
    }
  })

  // How far the server got when the leader changed decides what is left on it.
  it('words a leader change by how far the server got', async () => {
    const t = await translator('en')
    const left = (steps: RunNodeRecord['steps'], over: Partial<RunNodeRecord> = {}) =>
      installReasonText({ status: 'failed', reason: 'AGENT_INSTALL_LEADER_CHANGED', host: 'web-1.example', ip: '192.0.2.21', steps, ...over }, t) ?? ''
    // The install phase never began: nothing was installed.
    for (const steps of [undefined, phases('succeeded', 'unknown'), phases('succeeded', 'succeeded', 'pending'), phases('succeeded', 'failed', 'skipped')]) {
      const text = left(steps)
      expect(text, JSON.stringify(steps)).toContain('Nothing was installed on this server')
      expect(text, JSON.stringify(steps)).not.toContain('cleanup command')
    }
    // It began: an agent at its address may be in the node list, and the server is cleaned with the command.
    const began = left(phases('succeeded', 'succeeded', 'unknown'))
    expect(began).toContain('an agent at 192.0.2.21')
    expect(began).toContain('the cleanup command below')
    // Its address as given when the leader never resolved one.
    expect(left(phases('succeeded', 'succeeded', 'unknown'), { ip: undefined })).toContain('an agent at web-1.example')
    // It began on a server that had SFPanel: no cleanup command.
    const kept = left(phases('succeeded', 'succeeded', 'unknown'), { sfpanel_before: true })
    expect(kept).toContain('an agent at 192.0.2.21')
    expect(kept).not.toContain('cleanup command')
    // Registered: an agent the node page shows.
    expect(left(phases('succeeded', 'succeeded', 'succeeded', 'unknown'), { node_id: 'a-1' })).toContain('node page')
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

  // Purging such a server would take the operator's own SFPanel and its data.
  it('is never a server that had SFPanel before', () => {
    const kept = { sfpanel_before: true }
    expect(needsCleanup(offline(kept), true)).toBe(false)
    for (const reverted of ['removed', 'cleanup_failed']) {
      expect(needsCleanup(offline({ ...kept, reason_details: { cause: 'agent_to_managers', reverted } }), true), reverted).toBe(false)
    }
    expect(needsCleanup({ status: 'failed', reason: 'AGENT_INSTALL_LEADER_CHANGED', steps: phases('succeeded', 'succeeded', 'unknown'), ...kept }, true)).toBe(false)
    expect(needsCleanup({ status: 'stopped', node_id: 'a-10', reason: 'AGENT_INSTALL_CREDENTIALS_GONE', reason_details: { registered: '1', reverted: 'removed' }, ...kept }, true)).toBe(false)
  })

  it('is a registered server that was stopped, once its revert removed the node', () => {
    const stopped = (over: Partial<RunNodeRecord> = {}): RunNodeRecord =>
      ({ status: 'stopped', node_id: 'a-10', reason: 'AGENT_INSTALL_STOPPED', reason_details: { registered: '1' }, ...over })
    // Still in the cluster: revert it first.
    expect(needsCleanup(stopped(), true)).toBe(false)
    expect(needsCleanup(stopped({ reason: 'AGENT_INSTALL_CREDENTIALS_GONE', reason_details: { registered: '1', reverted: 'removed' } }), true)).toBe(true)
  })

  it('is never an agent that is online with its group label missing', () => {
    expect(needsCleanup({ status: 'succeeded', node_id: 'a-10', reason: 'AGENT_INSTALL_LABEL_FAILED', reason_details: { group: 'web' } }, true)).toBe(false)
  })
})

describe('keptPanelNote', () => {
  const failed = (over: Partial<RunNodeRecord> = {}): RunNodeRecord =>
    ({ status: 'failed', reason: 'AGENT_INSTALL_FAILED', reason_details: { step: 'install', exit: '1' }, sfpanel_before: true, ...over })

  it('says nothing was removed from a server that had SFPanel before', async () => {
    const t = await translator('en')
    expect(keptPanelNote(failed(), t)).toEqual({ text: 'SFPanel was on this server before the install, so nothing was removed from it.', start: false })
    expect(keptPanelNote(failed({ status: 'stopped' }), t)?.text).toContain('nothing was removed')
  })

  it('notes a panel started again, and asks to start one that could not be', async () => {
    const t = await translator('en')
    const restarted = keptPanelNote(failed({ reason_details: { step: 'install', exit: '1', panel: 'restarted' } }), t)
    expect(restarted?.text).toContain('started again')
    expect(restarted?.start).toBe(false)
    expect(keptPanelNote(failed({ reason_details: { step: 'install', exit: '1', panel: 'restart_failed' } }), t)?.start).toBe(true)
    expect(PANEL_START_COMMAND).toBe('sudo systemctl start sfpanel')
  })

  it('asks to start the panel of a server a leader change left during its install', async () => {
    const t = await translator('en')
    const left = (steps: RunNodeRecord['steps'], over: Partial<RunNodeRecord> = {}) =>
      keptPanelNote(failed({ reason: 'AGENT_INSTALL_LEADER_CHANGED', reason_details: undefined, steps, ...over }), t)
    expect(left(phases('succeeded', 'succeeded', 'unknown'))?.start).toBe(true)
    expect(left(phases('succeeded', 'succeeded', 'pending'))?.start).toBe(false)
    // Registered: the agent runs there now.
    expect(left(phases('succeeded', 'succeeded', 'succeeded', 'unknown'), { node_id: 'a-1' })?.start).toBe(false)
  })

  it('is null for a server that had no SFPanel, one that became an agent, and one whose revert said it', async () => {
    const t = await translator('en')
    expect(keptPanelNote(failed({ sfpanel_before: undefined }), t)).toBeNull()
    expect(keptPanelNote(failed({ status: 'succeeded', node_id: 'a-1', reason: undefined }), t)).toBeNull()
    expect(keptPanelNote(failed({ status: 'running' }), t)).toBeNull()
    expect(keptPanelNote(failed({ status: 'skipped' }), t)).toBeNull()
    expect(keptPanelNote(failed({ reason_details: { reverted: 'removed', standalone: '1' } }), t)).toBeNull()
    expect(keptPanelNote(undefined, t)).toBeNull()
  })

  it('reads in Korean', async () => {
    const t = await translator('ko')
    expect(keptPanelNote(failed(), t)?.text).toMatch(/[가-힣]/)
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
    // Online with its group label missing: only the node page, where the label is set.
    expect(installRowActions(head(), { status: 'succeeded', node_id: 'a-1', reason: 'AGENT_INSTALL_LABEL_FAILED', reason_details: { group: 'web' } }))
      .toEqual({ ...none, openNode: true })
  })

  // A stop drops the session's context and the credentials: no recheck, and
  // a revert removes the node.
  it('offers Revert alone to a server stopped after it registered, until it is reverted', () => {
    const stopped = (over: Partial<RunNodeRecord> = {}): RunNodeRecord =>
      ({ status: 'stopped', node_id: 'a-10', reason: 'AGENT_INSTALL_STOPPED', reason_details: { registered: '1' }, ...over })
    expect(installRowActions(head({ status: 'stopped' }), stopped())).toEqual({ ...none, revert: true })
    expect(installRowActions(head({ status: 'failed' }), stopped())).toEqual({ ...none, revert: true })
    expect(installRowActions(head(), stopped({ reason: 'AGENT_INSTALL_CREDENTIALS_GONE', reason_details: { registered: '1', reverted: 'removed' } }))).toEqual(none)
    expect(installRowActions(head(), stopped({ node_id: undefined, reason: undefined, reason_details: undefined }))).toEqual(none)
    expect(installRowActions(head({ reason: 'AGENT_INSTALL_LEADER_CHANGED' }), stopped())).toEqual(none)
  })
})

describe('installReview', () => {
  const run = (over: Partial<RunRecord> = {}) =>
    head({ status: 'running', ended_at: undefined, nodes: ['s1', 's2', 's3'], inputs: { mode: 'auto', review: 'true', version: '0.81.0' }, ...over })
  const atReview = running({ awaiting: 'review', steps: phases('succeeded', 'succeeded') })

  it('opens once every server waits for it or has ended, naming those waiting in run order', () => {
    const nodes = {
      s1: atReview,
      s2: { status: 'failed', reason: 'AGENT_INSTALL_ARCH', steps: phases('succeeded', 'failed', 'skipped') } as RunNodeRecord,
      s3: atReview,
    }
    expect(installReview(run({ nodes: ['s3', 's1', 's2'] }), nodes)).toEqual({ waiting: ['s3', 's1'], open: true, passed: 2, total: 3 })
    // A server the operator skipped has ended too.
    expect(installReview(run(), { ...nodes, s2: { status: 'skipped', steps: phases('succeeded', 'succeeded', 'skipped') } })?.open).toBe(true)
  })

  it('stays shut while a server is still before it, and counts those that passed', () => {
    for (const before of [
      running({ awaiting: 'host_key', host_key: 'ED25519 SHA256:x', steps: phases('running') }),
      running({ awaiting: 'check', steps: phases('running') }),
      running({ awaiting: 'standalone', steps: phases('succeeded', 'running') }),
      running({ steps: phases('succeeded', 'running') }),
      { status: 'pending' } as RunNodeRecord,
      undefined,
    ]) {
      const nodes: Record<string, RunNodeRecord> = { s1: atReview, s3: atReview }
      if (before) nodes.s2 = before
      expect(installReview(run(), nodes), JSON.stringify(before)).toEqual({ waiting: ['s1', 's3'], open: false, passed: 2, total: 3 })
    }
    // None has passed yet: it says so.
    expect(installReview(run(), { s1: running({ steps: phases('running') }) })).toEqual({ waiting: [], open: false, passed: 0, total: 3 })
  })

  it('is null once the review was answered, and for an install that asked for none', () => {
    // Every server went on to its install, or ended.
    const installing = running({ steps: phases('succeeded', 'succeeded', 'running') })
    expect(installReview(run(), { s1: installing, s2: installing, s3: { status: 'failed', steps: phases('failed') } })).toBeNull()
    expect(installReview(run({ status: 'failed', ended_at: '2026-11-01T12:05:00Z' }), { s1: { status: 'stopped' } })).toBeNull()
    expect(installReview(run({ inputs: { mode: 'auto', review: 'false', version: '0.81.0' } }), { s1: running({ steps: phases('running') }) })).toBeNull()
  })
})

describe('rowActionErrorText', () => {
  const refusal = (status: number, code: string, message: string) => Object.assign(new Error(message), { status, code })

  it('says the session is gone and what to do by hand, when the leader no longer holds the install', async () => {
    const t = await translator('en')
    const text = rowActionErrorText(refusal(404, 'AGENT_INSTALL_NOT_FOUND', 'The cluster leader no longer holds this install'), offline(), t)
    expect(text).toContain('The session that ran this install is gone')
    expect(text).toContain('remove the node on its page')
    expect(text).toContain('clean the server by hand')
    // A server that had SFPanel before is not cleaned.
    const kept = rowActionErrorText(refusal(404, 'AGENT_INSTALL_NOT_FOUND', 'x'), offline({ sfpanel_before: true }), t)
    expect(kept).toContain('remove the node on its page')
    expect(kept).not.toContain('clean the server')
  })

  it('keeps any other refusal as it was worded', async () => {
    const t = await translator('en')
    expect(rowActionErrorText(refusal(409, 'AGENT_INSTALL_NOT_WAITING', 'Worded by its code.'), offline(), t)).toBe('Worded by its code.')
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
