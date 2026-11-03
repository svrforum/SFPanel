import { describe, expect, it } from 'vitest'
import type { TFunction } from 'i18next'
import en from '@/i18n/locales/en.json'
import ko from '@/i18n/locales/ko.json'
import {
  buildInstallRequest,
  emptyInstallForm,
  formProblemText,
  formProblems,
  lineProblemText,
  parseServerLines,
  problemFromError,
  validGroup,
  validHost,
  validPort,
  type InstallForm,
  type InstallServerRow,
} from './installForm'

const row = (host: string, port = '22', name = '', group = ''): InstallServerRow => ({ host, port, name, group })

function form(over: Partial<InstallForm> = {}): InstallForm {
  return { ...emptyInstallForm(), servers: [row('192.0.2.21')], user: 'ops', method: 'password', password: 'pw', ...over }
}

function leaf(tree: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>(
    (node, part) => (node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined),
    tree,
  )
}
// A t that fills placeholders from the tree it reads, as i18next does.
const lookup = (tree: unknown) => ((key: string, params?: Record<string, unknown>) => {
  const v = leaf(tree, key)
  if (typeof v !== 'string') return key
  return v.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (params && name in params ? String(params[name]) : m))
}) as unknown as TFunction

describe('parseServerLines', () => {
  it('reads host[:port] [name] [group], one server a line', () => {
    const { servers, problems } = parseServerLines([
      '192.0.2.21',
      '192.0.2.22:2222 web-2',
      '  198.51.100.7   db-1   db  ',
      'peer-node.example.ts.net:22 peer web',
    ].join('\n'))
    expect(problems).toEqual([])
    expect(servers).toEqual([
      row('192.0.2.21'),
      row('192.0.2.22', '2222', 'web-2'),
      row('198.51.100.7', '22', 'db-1', 'db'),
      row('peer-node.example.ts.net', '22', 'peer', 'web'),
    ])
  })

  it('takes port 22 when none is given', () => {
    expect(parseServerLines('203.0.113.5 edge').servers).toEqual([row('203.0.113.5', '22', 'edge')])
  })

  it('skips comments and blank lines, and counts them in the line numbers', () => {
    const { servers, problems } = parseServerLines('# web servers\n\n192.0.2.21 web-1 # the first\r\n\t\n192.0.2.22:0\n')
    expect(servers).toEqual([row('192.0.2.21', '22', 'web-1')])
    expect(problems).toEqual([{ line: 5, field: 'port' }])
  })

  it('reads IPv6, bracketed with a port or bare without one', () => {
    const { servers, problems } = parseServerLines('[2001:db8::21]:2222 v6-a\n2001:db8::22 v6-b\n[2001:db8::23]')
    expect(problems).toEqual([])
    expect(servers).toEqual([row('2001:db8::21', '2222', 'v6-a'), row('2001:db8::22', '22', 'v6-b'), row('2001:db8::23')])
  })

  it('names the line and the field of each problem, and leaves the line out', () => {
    const { servers, problems } = parseServerLines([
      '192.0.2.21:70000', //   1 port out of range
      '192.0.2.22: web', //    2 port empty
      '192.0.2.23 -web', //    3 name may not start with -
      '192.0.2.24 web 웹서버', // 4 group
      '192.0.2.25 web web extra', // 5 a fourth word
      'host/with/slash', //    6 host
      '192.0.2.026', //        7 an address with a leading zero
      '[2001:db8::1', //       8 unclosed bracket
      '192.0.2.27 ok', //      9 fine
      '[2001:db8::%eth0]:22', // 10 a zone
    ].join('\n'))
    expect(problems).toEqual([
      { line: 1, field: 'port' },
      { line: 2, field: 'port' },
      { line: 3, field: 'name' },
      { line: 4, field: 'group' },
      { line: 5, field: 'extra' },
      { line: 6, field: 'host' },
      { line: 7, field: 'host' },
      { line: 8, field: 'host' },
      { line: 10, field: 'host' },
    ])
    expect(servers).toEqual([row('192.0.2.27', '22', 'ok')])
  })

  it('refuses the same host twice, naming the line it came on first', () => {
    const { servers, problems } = parseServerLines([
      'Peer-Node.example.',
      '192.0.2.21',
      'peer-node.example:2222 other',
      '192.0.2.21:2200',
      '[2001:DB8:0::1]',
      '2001:db8::1',
    ].join('\n'))
    expect(servers).toEqual([row('Peer-Node.example.'), row('192.0.2.21'), row('2001:DB8:0::1')])
    expect(problems).toEqual([
      { line: 3, field: 'host', sameLine: 1 },
      { line: 4, field: 'host', sameLine: 2 },
      { line: 6, field: 'host', sameLine: 5 },
    ])
  })

  it('words a line problem with its line number in both languages', () => {
    expect(lineProblemText({ line: 4, field: 'port' }, lookup(en))).toBe('Line 4: The SSH port is a number from 1 to 65535.')
    expect(lineProblemText({ line: 3, field: 'host', sameLine: 1 }, lookup(en))).toBe('Line 3: This server is already on line 1.')
    expect(lineProblemText({ line: 4, field: 'port' }, lookup(ko))).toBe('4번째 줄: SSH 포트는 1~65535 사이의 숫자입니다.')
    for (const field of ['host', 'port', 'name', 'group', 'extra'] as const) {
      expect(lineProblemText({ line: 2, field }, lookup(en)), field).not.toMatch(/cluster\.tasks|\{\{|[가-힣]/)
      expect(lineProblemText({ line: 2, field }, lookup(ko)), field).not.toMatch(/cluster\.tasks|\{\{/)
    }
  })
})

describe('validHost, validPort, validGroup', () => {
  it('takes addresses and host names', () => {
    for (const h of ['192.0.2.21', '0.0.0.0', '2001:db8::1', '::ffff:192.0.2.1', 'peer-node', 'peer-node.example.ts.net', 'peer-node.example.', 'srv_1.lan']) {
      expect(validHost(h), h).toBe(true)
    }
  })

  it('refuses what the leader could neither parse nor resolve', () => {
    for (const h of ['', ' ', '192.0.2.256', '192.0.2', '192.0.2.01', '1.2.3.4.5', 'a b', 'a/b', 'user@host', 'host:22', '[2001:db8::1]', '2001:db8::g', 'fe80::1%eth0', '-peer', 'peer-', 'a..b', 'x'.repeat(64) + '.example', 'http://host']) {
      expect(validHost(h), h).toBe(false)
    }
  })

  it('takes a port from 1 to 65535', () => {
    for (const p of ['1', '22', '2222', '65535']) expect(validPort(p), p).toBe(true)
    for (const p of ['', '0', '65536', '-1', '22a', '2.2', ' 22', '100000']) expect(validPort(p), p).toBe(false)
  })

  it('takes a group the label check takes, empty for none', () => {
    for (const g of ['', 'web', 'web.eu-1_a', 'x'.repeat(63)]) expect(validGroup(g), g).toBe(true)
    for (const g of ['x'.repeat(64), 'web 1', '웹', 'web/1', 'a:b']) expect(validGroup(g), g).toBe(false)
  })
})

describe('formProblems', () => {
  it('finds none in a good form', () => {
    expect(formProblems(form())).toEqual([])
    expect(formProblems(form({ method: 'key', password: '', key: '-----BEGIN OPENSSH PRIVATE KEY-----\n…' }))).toEqual([])
    expect(formProblems(form({ method: 'tailscale', password: '' }))).toEqual([])
  })

  it('asks for a server, and at most 50', () => {
    expect(formProblems(form({ servers: [] }))).toEqual([{ field: 'servers' }])
    expect(formProblems(form({ servers: [row(''), row('  ', '')] }))).toEqual([{ field: 'servers' }])
    const many = Array.from({ length: 51 }, (_, i) => row(`192.0.2.${i + 1}`))
    expect(formProblems(form({ servers: many }))).toEqual([{ field: 'servers', max: 50 }])
    expect(formProblems(form({ servers: many.slice(0, 50) }))).toEqual([])
  })

  it('names the table row of each server problem, skipping blank rows', () => {
    const servers = [row(''), row('192.0.2.21', ''), row('bad host'), row('192.0.2.23', '99999'), row('192.0.2.24', '22', '_x'), row('192.0.2.25', '22', '', 'a b'), row('192.0.2.21.')]
    expect(formProblems(form({ servers }))).toEqual([
      { field: 'host', index: 2 },
      { field: 'port', index: 3 },
      { field: 'name', index: 4 },
      { field: 'group', index: 5 },
      { field: 'host', index: 6 },
    ])
  })

  it('refuses a host given twice, naming the row it came on first', () => {
    const servers = [row('PEER.example'), row('192.0.2.9'), row('peer.example.', '2222')]
    expect(formProblems(form({ servers }))).toEqual([{ field: 'host', index: 2, sameAs: 0 }])
  })

  it("asks for each login method's own secret and the user", () => {
    expect(formProblems(form({ user: ' ' }))).toEqual([{ field: 'user' }])
    expect(formProblems(form({ password: '' }))).toEqual([{ field: 'password' }])
    expect(formProblems(form({ method: 'key', password: 'pw', key: ' \n' }))).toEqual([{ field: 'key' }])
    // Tailscale SSH takes none, whatever the other fields still hold.
    expect(formProblems(form({ method: 'tailscale', password: '', key: '' }))).toEqual([])
  })

  it('asks nothing of the account a saved login stands for, and still checks the servers', () => {
    expect(formProblems(form({ user: '', password: '' }), true)).toEqual([])
    expect(formProblems(form({ method: 'key', key: '' }), true)).toEqual([])
    expect(formProblems(form({ user: '', servers: [row('not/a/host')] }), true)).toEqual([{ field: 'host', index: 0 }])
    expect(formProblems(form({ user: '', concurrency: 9 }), true)).toEqual([{ field: 'concurrency' }])
  })

  it('keeps the mode and how many at once inside what the server takes', () => {
    expect(formProblems(form({ concurrency: 0 }))).toEqual([{ field: 'concurrency' }])
    expect(formProblems(form({ concurrency: 6 }))).toEqual([{ field: 'concurrency' }])
    expect(formProblems(form({ concurrency: 2.5 }))).toEqual([{ field: 'concurrency' }])
    expect(formProblems(form({ mode: 'sideways' as InstallForm['mode'] }))).toEqual([{ field: 'mode' }])
  })

  it('words each problem in both languages', () => {
    const all = [
      { field: 'servers' }, { field: 'servers', max: 50 }, { field: 'host', index: 1 }, { field: 'host', index: 2, sameAs: 0 },
      { field: 'port' }, { field: 'name' }, { field: 'group' }, { field: 'user' }, { field: 'method' }, { field: 'password' },
      { field: 'key' }, { field: 'passphrase' }, { field: 'sudo_same' }, { field: 'sudo_password' }, { field: 'mode' },
      { field: 'concurrency' }, { field: 'body' }, { field: 'something-new' },
    ]
    for (const p of all) {
      expect(formProblemText(p, lookup(en)), p.field).not.toMatch(/cluster\.tasks|\{\{|[가-힣]/)
      expect(formProblemText(p, lookup(ko)), p.field).not.toMatch(/cluster\.tasks|\{\{/)
    }
    expect(formProblemText({ field: 'host', index: 2, sameAs: 0 }, lookup(en))).toBe('This server is already on row 1.')
    expect(formProblemText({ field: 'servers', max: 50 }, lookup(ko))).toBe('한 번에 50대까지 설치할 수 있습니다.')
  })
})

describe('problemFromError', () => {
  const invalid = (details: Record<string, string>) => Object.assign(new Error('x'), { status: 400, code: 'AGENT_INSTALL_INVALID', details })

  it("puts the server's row back on the table row it came from", () => {
    // s1 is the second table row: the blank first row is not sent.
    const f = form({ servers: [row(''), row('192.0.2.21'), row(' '), row('192.0.2.22'), row('192.0.2.23')] })
    expect(problemFromError(invalid({ field: 'port', row: 's2' }), f)).toEqual({ field: 'port', index: 3 })
    expect(problemFromError(invalid({ field: 'host', row: 's3', same_as: 's1' }), f)).toEqual({ field: 'host', index: 4, sameAs: 1 })
    expect(problemFromError(invalid({ field: 'servers', max: '50' }), f)).toEqual({ field: 'servers', max: 50 })
    expect(problemFromError(invalid({ field: 'sudo_password' }), f)).toEqual({ field: 'sudo_password' })
  })

  it('is null for any other refusal, or a row the form does not have', () => {
    const f = form()
    expect(problemFromError(Object.assign(new Error('x'), { status: 409, code: 'AGENT_INSTALL_ACTIVE' }), f)).toBeNull()
    expect(problemFromError(new Error('network'), f)).toBeNull()
    expect(problemFromError(invalid({}), f)).toBeNull()
    expect(problemFromError(invalid({ field: 'port', row: 's9' }), f)).toEqual({ field: 'port' })
  })
})

describe('buildInstallRequest', () => {
  it('sends each server with its port, and a name and group only when given', () => {
    const req = buildInstallRequest(form({ servers: [row(''), row(' 192.0.2.21 ', ''), row('2001:db8::1', '2222', ' web-1 ', ' web ')] }), false)
    expect(req.servers).toEqual([
      { host: '192.0.2.21', port: 22 },
      { host: '2001:db8::1', port: 2222, name: 'web-1', group: 'web' },
    ])
    expect([req.review, req.mode, req.concurrency]).toEqual([false, 'auto', 3])
    expect(buildInstallRequest(form({ mode: 'offline', concurrency: 5 }), true)).toMatchObject({ review: true, mode: 'offline', concurrency: 5 })
  })

  it('sends sudo_same only for a password login, and never with sudo_password', () => {
    const typed = { password: 'login-pw', sudoPassword: 'sudo-pw', key: 'KEY', passphrase: 'pass' }
    expect(buildInstallRequest(form({ ...typed, method: 'password', sudoSame: true }), false).account).toEqual({
      user: 'ops', method: 'password', password: 'login-pw', sudo_same: true,
    })
    expect(buildInstallRequest(form({ ...typed, method: 'password', sudoSame: false }), false).account).toEqual({
      user: 'ops', method: 'password', password: 'login-pw', sudo_password: 'sudo-pw',
    })
    expect(buildInstallRequest(form({ ...typed, method: 'key', sudoSame: true }), false).account).toEqual({
      user: 'ops', method: 'key', key: 'KEY', passphrase: 'pass', sudo_password: 'sudo-pw',
    })
    expect(buildInstallRequest(form({ ...typed, method: 'tailscale', sudoSame: true }), false).account).toEqual({
      user: 'ops', method: 'tailscale', sudo_password: 'sudo-pw',
    })
  })

  it("sends only the chosen method's secrets, whatever the other fields still hold", () => {
    const typed = { password: 'login-pw', key: 'KEY', passphrase: 'pass', sudoSame: false, sudoPassword: '' }
    expect(buildInstallRequest(form({ ...typed, method: 'password' }), false).account).toEqual({ user: 'ops', method: 'password', password: 'login-pw' })
    expect(buildInstallRequest(form({ ...typed, method: 'key' }), false).account).toEqual({ user: 'ops', method: 'key', key: 'KEY', passphrase: 'pass' })
    expect(buildInstallRequest(form({ ...typed, method: 'key', passphrase: '' }), false).account).toEqual({ user: 'ops', method: 'key', key: 'KEY' })
    expect(buildInstallRequest(form({ ...typed, method: 'tailscale' }), false).account).toEqual({ user: 'ops', method: 'tailscale' })
  })

  it('sends no sudo secret for root, and the secrets exactly as typed', () => {
    expect(buildInstallRequest(form({ user: ' root ', password: ' p w ', sudoSame: true, sudoPassword: 'x' }), false).account).toEqual({
      user: 'root', method: 'password', password: ' p w ',
    })
  })

  it("sends a saved login's ID alone as the account, whatever the fields still hold", () => {
    const typed = form({ method: 'key', password: 'login-pw', key: 'KEY', passphrase: 'pass', sudoSame: false, sudoPassword: 'sudo-pw' })
    const req = buildInstallRequest(typed, true, '3f9a1c0b7d2e4a61')
    expect(req.account).toEqual({ login: '3f9a1c0b7d2e4a61' })
    expect(Object.keys(req.account)).toEqual(['login'])
    expect(JSON.stringify(req)).not.toMatch(/login-pw|KEY|pass"|sudo-pw|"user"|"method"/)
    // The rest of the start is as typed.
    expect(req).toMatchObject({ servers: [{ host: '192.0.2.21', port: 22 }], review: true, mode: 'auto', concurrency: 3 })
    // Without one, the account is the one typed.
    expect(buildInstallRequest(typed, false).account).toEqual({ user: 'ops', method: 'key', key: 'KEY', passphrase: 'pass', sudo_password: 'sudo-pw' })
    expect(buildInstallRequest(typed, false, '').account).toEqual({ user: 'ops', method: 'key', key: 'KEY', passphrase: 'pass', sudo_password: 'sudo-pw' })
  })
})
