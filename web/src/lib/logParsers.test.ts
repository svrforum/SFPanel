import { describe, expect, it } from 'vitest'
import {
  getParser,
  hasParsedView,
  parseFirewallLine,
  parseLogLines,
  type AuthLogEntry,
  type Fail2banLogEntry,
  type FirewallLogEntry,
  type SFPanelLogEntry,
} from './logParsers'

// Only parseFirewallLine is exported directly; the other three parsers are
// reachable through the registry, which is also how the log viewer uses them.
const authParser = getParser('auth')!
const fail2banParser = getParser('fail2ban')!
const sfpanelParser = getParser('sfpanel')!

function parseAuth(line: string): AuthLogEntry {
  const entry = authParser.parse(line)
  if (!entry.parsed) throw new Error(`expected a parsed auth entry for: ${line}`)
  return entry as AuthLogEntry
}

function parseFirewall(line: string): FirewallLogEntry {
  const entry = parseFirewallLine(line)
  if (!entry.parsed) throw new Error(`expected a parsed firewall entry for: ${line}`)
  return entry
}

function parseFail2ban(line: string): Fail2banLogEntry {
  const entry = fail2banParser.parse(line)
  if (!entry.parsed) throw new Error(`expected a parsed fail2ban entry for: ${line}`)
  return entry as Fail2banLogEntry
}

function parseSFPanel(line: string): SFPanelLogEntry {
  const entry = sfpanelParser.parse(line)
  if (!entry.parsed) throw new Error(`expected a parsed sfpanel entry for: ${line}`)
  return entry as SFPanelLogEntry
}

describe('syslog prefix handling', () => {
  it('accepts the traditional syslog timestamp', () => {
    expect(parseAuth('Feb 27 20:43:56 host sshd[1234]: Started thing').timestamp).toBe('Feb 27 20:43:56')
  })

  it('accepts the ISO 8601 timestamp rsyslog emits with RSYSLOG_FileFormat', () => {
    const entry = parseAuth('2026-02-27T20:43:56.123+09:00 host sshd[1234]: Started thing')
    expect(entry.timestamp).toBe('2026-02-27T20:43:56.123+09:00')
  })

  it('shortens the ISO timestamp for display but keeps the traditional one as-is', () => {
    const [tsColumn] = authParser.columns
    const iso = parseAuth('2026-02-27T20:43:56.123+09:00 host sshd[1]: x')
    const traditional = parseAuth('Feb 27 20:43:56 host sshd[1]: x')
    expect(tsColumn.render(iso).text).toBe('02-27 20:43:56')
    expect(tsColumn.render(traditional).text).toBe('Feb 27 20:43:56')
  })

  it('falls back to a raw entry when the line has no syslog prefix', () => {
    expect(authParser.parse('not a syslog line')).toEqual({ parsed: false, rawLine: 'not a syslog line' })
  })

  it('falls back to a raw entry when the syslog message has no "service:" part', () => {
    const line = 'Feb 27 20:43:56 host just some text'
    expect(authParser.parse(line)).toEqual({ parsed: false, rawLine: line })
  })
})

describe('auth.log parser', () => {
  type AuthCase = {
    name: string
    line: string
    event: AuthLogEntry['event']
    user: string | null
    sourceIP: string | null
    details?: string
  }

  const cases: AuthCase[] = [
    {
      name: 'accepted publickey login',
      line: 'Feb 27 20:43:56 host sshd[1234]: Accepted publickey for admin from 10.0.0.5 port 51234 ssh2',
      event: 'success',
      user: 'admin',
      sourceIP: '10.0.0.5',
      details: 'publickey login from 10.0.0.5',
    },
    {
      name: 'failed password for an invalid user',
      line: 'Feb 27 20:43:56 host sshd[1234]: Failed password for invalid user root from 10.0.0.5 port 22 ssh2',
      event: 'failure',
      user: 'root',
      sourceIP: '10.0.0.5',
    },
    {
      name: 'invalid user probe',
      line: 'Feb 27 20:43:56 host sshd[1234]: Invalid user oracle from 10.0.0.5 port 22',
      event: 'failure',
      user: 'oracle',
      sourceIP: '10.0.0.5',
    },
    {
      name: 'connection closed by authenticating user',
      line: 'Feb 27 20:43:56 host sshd[1]: Connection closed by authenticating user root 10.0.0.5 port 22 [preauth]',
      event: 'failure',
      user: 'root',
      sourceIP: '10.0.0.5',
    },
    {
      name: 'sudo command',
      line: 'Feb 27 20:43:56 host sudo:  admin : TTY=pts/0 ; PWD=/root ; USER=root ; COMMAND=/usr/bin/apt update',
      event: 'sudo',
      user: 'admin',
      sourceIP: null,
      details: '/usr/bin/apt update',
    },
    {
      name: 'pam session opened',
      line: 'Feb 27 20:43:56 host cron[1]: pam_unix(cron:session): session opened for user root(uid=0) by (uid=0)',
      event: 'session',
      user: 'root',
      sourceIP: null,
      details: 'session opened',
    },
    {
      name: 'pam session closed',
      line: 'Feb 27 20:43:56 host cron[1]: pam_unix(cron:session): session closed for user root',
      event: 'session',
      user: 'root',
      sourceIP: null,
      details: 'session closed',
    },
    {
      name: 'unrecognised message',
      line: 'Feb 27 20:43:56 host systemd: Started thing',
      event: 'other',
      user: null,
      sourceIP: null,
      details: 'Started thing',
    },
  ]

  it.each(cases)('$name', ({ line, event, user, sourceIP, details }) => {
    const entry = parseAuth(line)
    expect(entry.event).toBe(event)
    expect(entry.user).toBe(user)
    expect(entry.sourceIP).toBe(sourceIP)
    if (details !== undefined) expect(entry.details).toBe(details)
  })

  it('keeps the pid when present and substitutes a dash when absent', () => {
    expect(parseAuth('Feb 27 20:43:56 host sshd[1234]: x').pid).toBe('1234')
    expect(parseAuth('Feb 27 20:43:56 host systemd: x').pid).toBe('-')
  })

  it('strips the pam_unix prefix from details', () => {
    const entry = parseAuth('Feb 27 20:43:56 host sshd[1]: pam_unix(sshd:auth): authentication failure; rhost=10.0.0.5')
    expect(entry.details).not.toContain('pam_unix')
  })

  it('preserves the raw line so raw view and search stay available', () => {
    const line = 'Feb 27 20:43:56 host sshd[1]: Accepted password for a from 1.2.3.4 port 1 ssh2'
    expect(parseAuth(line).rawLine).toBe(line)
  })

  it('colors the event column and renders it as a pill', () => {
    const eventColumn = authParser.columns.find((c) => c.key === 'event')!
    const success = eventColumn.render(parseAuth('Feb 27 20:43:56 host sshd[1]: Accepted publickey for a from 1.2.3.4'))
    expect(success).toEqual({ text: 'success', color: '#00c471', pill: true })
  })
})

describe('firewall parser', () => {
  it('parses a UFW block entry', () => {
    const entry = parseFirewall(
      'Feb 27 20:43:56 host kernel: [12345.678] [UFW BLOCK] IN=eth0 OUT= MAC=aa:bb SRC=10.0.0.5 DST=10.0.0.1 LEN=60 PROTO=TCP SPT=51234 DPT=22'
    )
    expect(entry).toMatchObject({
      source: 'UFW',
      action: 'BLOCK',
      sourceIP: '10.0.0.5',
      destPort: '22',
      protocol: 'TCP',
      iface: 'eth0',
    })
  })

  it('uppercases the protocol', () => {
    const entry = parseFirewall('Feb 27 20:43:56 host kernel: [UFW BLOCK] IN=eth0 SRC=10.0.0.5 PROTO=udp DPT=53')
    expect(entry.protocol).toBe('UDP')
  })

  it('substitutes a dash for missing key/value fields', () => {
    const entry = parseFirewall('Feb 27 20:43:56 host kernel: [UFW AUDIT] IN=eth0 SRC=10.0.0.5')
    expect(entry.destPort).toBe('-')
    expect(entry.protocol).toBe('-')
  })

  it('prefers the DOCKER-USER host port over the post-DNAT container port', () => {
    const entry = parseFirewall(
      'Feb 27 20:43:56 host kernel: [DOCKER-USER DROP] IN=eth0 OUT=docker0 SRC=10.0.0.5 DST=172.17.0.2 PROTO=tcp SPT=1 DPT=8080 HPORT=443'
    )
    expect(entry.source).toBe('Docker')
    expect(entry.action).toBe('DROP')
    expect(entry.destPort).toBe('443')
  })

  it('falls back to DPT when the DOCKER-USER entry carries no HPORT', () => {
    const entry = parseFirewall('Feb 27 20:43:56 host kernel: [DOCKER-USER ACCEPT] IN=eth0 SRC=10.0.0.5 PROTO=tcp DPT=53')
    expect(entry.destPort).toBe('53')
  })

  it('reports the OUT interface for outbound entries (empty IN=)', () => {
    const entry = parseFirewall('Feb 27 20:43:56 host kernel: [UFW BLOCK] IN= OUT=eth0 SRC=10.0.0.5 PROTO=TCP DPT=22')
    expect(entry.iface).toBe('eth0')
  })

  it('prefers the IN interface for inbound entries', () => {
    const entry = parseFirewall('Feb 27 20:43:56 host kernel: [UFW BLOCK] IN=eth0 OUT= SRC=10.0.0.5 PROTO=TCP DPT=22')
    expect(entry.iface).toBe('eth0')
  })

  it('falls back to the placeholder when neither interface is present', () => {
    const entry = parseFirewall('Feb 27 20:43:56 host kernel: [UFW BLOCK] SRC=10.0.0.5 PROTO=TCP DPT=22')
    expect(entry.iface).toBe('-')
  })

  it('leaves non-firewall kernel lines unparsed', () => {
    const line = 'Feb 27 20:43:56 host kernel: something entirely different'
    expect(parseFirewallLine(line)).toEqual({ parsed: false, rawLine: line })
  })
})

describe('fail2ban parser', () => {
  const cases: [name: string, line: string, action: Fail2banLogEntry['action'], ip: string, jail: string][] = [
    [
      'found',
      '2026-03-09 21:19:54,123 fail2ban.filter [12345]: INFO    [sshd] Found 10.0.0.5 - 2026-03-09 21:19:54',
      'Found',
      '10.0.0.5',
      'sshd',
    ],
    ['ban', '2026-03-09 21:19:54,123 fail2ban.actions [12345]: NOTICE  [sshd] Ban 10.0.0.5', 'Ban', '10.0.0.5', 'sshd'],
    ['unban', '2026-03-09 21:19:54,123 fail2ban.actions [12345]: NOTICE  [sshd] Unban 10.0.0.5', 'Unban', '10.0.0.5', 'sshd'],
    // "Restore Ban" is checked inside the Ban branch, so it classifies as Ban
    // rather than Restore; only a bare "Restore …" reaches the Restore branch.
    [
      'restore ban classifies as ban',
      '2026-03-09 21:19:54,123 fail2ban.actions [12345]: NOTICE  [sshd] Restore Ban 10.0.0.5',
      'Ban',
      '10.0.0.5',
      'sshd',
    ],
    [
      'ignore',
      '2026-03-09 21:19:54,123 fail2ban.filter [12345]: INFO    [sshd] Ignore 10.0.0.5 by ip',
      'Ignore',
      '10.0.0.5',
      'sshd',
    ],
  ]

  it.each(cases)('%s', (_name, line, action, ip, jail) => {
    const entry = parseFail2ban(line)
    expect(entry.action).toBe(action)
    expect(entry.ip).toBe(ip)
    expect(entry.jail).toBe(jail)
  })

  it('parses server lines that carry no jail', () => {
    const entry = parseFail2ban('2026-03-09 21:19:54,123 fail2ban.server [12345]: INFO    Starting Fail2ban v1.0.2')
    expect(entry).toMatchObject({ module: 'server', level: 'INFO', jail: '-', action: 'other', ip: '-' })
    expect(entry.message).toBe('Starting Fail2ban v1.0.2')
  })

  it('leaves non-fail2ban lines unparsed', () => {
    const line = 'Feb 27 20:43:56 host sshd[1]: Accepted publickey for a from 1.2.3.4'
    expect(fail2banParser.parse(line)).toEqual({ parsed: false, rawLine: line })
  })
})

describe('sfpanel.log parser', () => {
  it('parses the Echo JSON access-log format', () => {
    const entry = parseSFPanel(
      '{"time":"2026-02-27T20:43:56.123+09:00","remote_ip":"10.0.0.5","method":"GET","uri":"/api/v1/logs","status":200,"latency_human":"1.2ms"}'
    )
    expect(entry).toMatchObject({
      logType: 'request',
      method: 'GET',
      uri: '/api/v1/logs',
      status: 200,
      latency: '1.2ms',
      remoteIP: '10.0.0.5',
    })
  })

  // Query strings can carry tokens (the WS/SSE endpoints take ?token=), so the
  // parser drops them before the URI reaches the viewer.
  it('strips the query string from the request URI', () => {
    const entry = parseSFPanel(
      '{"time":"t","method":"GET","uri":"/api/v1/terminal/ws?token=SECRET&node=n1","status":101}'
    )
    expect(entry.uri).toBe('/api/v1/terminal/ws')
    expect(entry.uri).not.toContain('SECRET')
  })

  it('defaults the optional JSON fields', () => {
    const entry = parseSFPanel('{"time":"t","method":"GET","uri":"/x"}')
    expect(entry).toMatchObject({ status: 0, latency: '-', remoteIP: '-' })
  })

  it('falls through to a raw entry when the JSON lacks the access-log fields', () => {
    expect(sfpanelParser.parse('{"time":"t"}')).toEqual({ parsed: false, rawLine: '{"time":"t"}' })
  })

  it('falls through to a raw entry when the line only looks like JSON', () => {
    expect(sfpanelParser.parse('{not json')).toEqual({ parsed: false, rawLine: '{not json' })
  })

  it('parses the Go log format as a startup line', () => {
    const entry = parseSFPanel('2026/02/27 21:53:11 starting server')
    expect(entry).toMatchObject({ logType: 'startup', timestamp: '2026/02/27 21:53:11', message: 'starting server' })
  })

  it('parses the Echo startup banner', () => {
    const line = '⇨ http server started on [::]:3628'
    const entry = parseSFPanel(line)
    expect(entry).toMatchObject({ logType: 'startup', timestamp: '', message: line })
  })

  it('blanks the request-only columns for startup lines', () => {
    const entry = parseSFPanel('2026/02/27 21:53:11 starting server')
    for (const key of ['method', 'status', 'latency', 'remoteIP']) {
      const column = sfpanelParser.columns.find((c) => c.key === key)!
      expect(column.render(entry)).toEqual({ text: '' })
    }
  })
})

describe('parser registry', () => {
  it.each(['auth', 'firewall', 'fail2ban', 'sfpanel'])('exposes a parser for %s', (id) => {
    expect(hasParsedView(id)).toBe(true)
    expect(getParser(id)).not.toBeNull()
  })

  it('reports no parsed view for unknown or absent sources', () => {
    expect(hasParsedView(null)).toBe(false)
    expect(hasParsedView('custom-nginx')).toBe(false)
    expect(getParser('custom-nginx')).toBeNull()
  })

  // Inherited Object.prototype names must not masquerade as log sources: the
  // lookups would otherwise hand back a prototype member that dies on .parse().
  it('does not treat Object.prototype keys as known sources', () => {
    for (const key of ['constructor', 'toString', 'hasOwnProperty', '__proto__']) {
      expect(hasParsedView(key)).toBe(false)
      expect(getParser(key)).toBeNull()
    }
    expect(parseLogLines('constructor', ['x'])).toEqual([{ parsed: false, rawLine: 'x' }])
  })

  it('returns raw entries for every line of an unknown source', () => {
    expect(parseLogLines('custom-nginx', ['a', 'b'])).toEqual([
      { parsed: false, rawLine: 'a' },
      { parsed: false, rawLine: 'b' },
    ])
  })

  it('parses every line of a known source', () => {
    const entries = parseLogLines('auth', [
      'Feb 27 20:43:56 host sshd[1]: Accepted publickey for a from 1.2.3.4',
      'garbage',
    ])
    expect(entries.map((e) => e.parsed)).toEqual([true, false])
  })

  it.each(['auth', 'firewall', 'fail2ban', 'sfpanel'])('%s columns satisfy the viewer contract', (id) => {
    const { columns } = getParser(id)!
    // The viewer lays columns out with a fixed width each, except for at most
    // one free-width column that gets the ellipsis + highlight treatment.
    expect(columns.length).toBeGreaterThan(0)
    expect(columns.filter((c) => c.flex).length).toBeLessThanOrEqual(1)
    for (const column of columns) {
      expect(column.i18nKey).toMatch(/^logs\.col\./)
      expect(column.flex ? true : Boolean(column.width)).toBe(true)
    }
    expect(new Set(columns.map((c) => c.key)).size).toBe(columns.length)
  })
})
