// Log parser utilities for structured log viewing
// Parses raw log lines into structured entries for auth.log, ufw.log, etc.

// --- Types ---

export interface ParsedLogEntry {
  parsed: true
  timestamp: string
  rawLine: string
}

export interface RawLogEntry {
  parsed: false
  rawLine: string
}

export type LogEntry = ParsedLogEntry | RawLogEntry

export type AuthEvent = 'success' | 'failure' | 'sudo' | 'session' | 'other'

export interface AuthLogEntry extends ParsedLogEntry {
  service: string
  pid: string
  event: AuthEvent
  sourceIP: string | null
  user: string | null
  details: string
}


export interface ColumnDef<T extends ParsedLogEntry> {
  key: string
  i18nKey: string
  width?: string
  render: (entry: T) => { text: string; color?: string; pill?: boolean }
}

export interface LogParser<T extends ParsedLogEntry> {
  parse: (line: string) => T | RawLogEntry
  columns: ColumnDef<T>[]
}

// --- Syslog prefix ---

// Matches both traditional "Feb 27 20:43:56" and ISO 8601 "2026-02-27T20:43:56.123+09:00"
const SYSLOG_RE = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+[+-]\d{2}:\d{2}|\w{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s+(\S+)\s+(.*)/

// Shorten ISO 8601 timestamp: "2026-02-27T20:43:56.123+09:00" → "02-27 20:43:56"
function shortTimestamp(ts: string): string {
  if (ts.includes('T')) {
    const m = ts.match(/\d{4}-(\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})/)
    return m ? `${m[1]} ${m[2]}` : ts
  }
  return ts
}

// --- auth.log parser ---

const SERVICE_RE = /^(\S+?)(?:\[(\d+)\])?:\s+(.*)/

// Strip "(uid=N)" suffix from usernames: "root(uid=0)" → "root"
function cleanUser(raw: string | null): string | null {
  if (!raw) return null
  return raw.replace(/\(uid=\d+\)$/, '') || raw
}

// Strip pam_unix(...) prefix: "pam_unix(cron:session): session opened ..." → "session opened ..."
function cleanDetails(message: string): string {
  return message.replace(/^pam_unix\([^)]*\):\s*/, '')
}

function parseAuthEvent(service: string, message: string): {
  event: AuthEvent
  sourceIP: string | null
  user: string | null
  details: string
} {
  const msgLower = message.toLowerCase()

  // SSH accepted
  if (msgLower.startsWith('accepted')) {
    const m = message.match(/Accepted\s+(\S+)\s+for\s+(\S+)\s+from\s+(\S+)/)
    return {
      event: 'success',
      user: m?.[2] ?? null,
      sourceIP: m?.[3] ?? null,
      details: m ? `${m[1]} login from ${m[3]}` : cleanDetails(message),
    }
  }

  // SSH failed
  if (msgLower.startsWith('failed password') || msgLower.startsWith('authentication failure')) {
    const mFailed = message.match(/for\s+(?:invalid\s+user\s+)?(\S+)\s+from\s+(\S+)/)
    return {
      event: 'failure',
      user: mFailed?.[1] ?? null,
      sourceIP: mFailed?.[2] ?? null,
      details: cleanDetails(message),
    }
  }

  // Invalid user (pre-auth)
  if (msgLower.startsWith('invalid user')) {
    const mInvalid = message.match(/Invalid user\s+(\S+)\s+from\s+(\S+)/)
    return {
      event: 'failure',
      user: mInvalid?.[1] ?? null,
      sourceIP: mInvalid?.[2] ?? null,
      details: cleanDetails(message),
    }
  }

  // Connection closed by authenticating user (failed)
  if (msgLower.includes('connection closed by authenticating user')) {
    const mClosed = message.match(/user\s+(\S+)\s+(\S+)/)
    return {
      event: 'failure',
      user: mClosed?.[1] ?? null,
      sourceIP: mClosed?.[2] ?? null,
      details: cleanDetails(message),
    }
  }

  // sudo
  if (service === 'sudo' || msgLower.includes('sudo')) {
    const mSudo = message.match(/(\S+)\s*:.*COMMAND=(.*)/)
    return {
      event: 'sudo',
      user: cleanUser(mSudo?.[1] ?? null),
      sourceIP: null,
      details: mSudo ? mSudo[2].trim() : cleanDetails(message),
    }
  }

  // Session opened/closed
  if (msgLower.includes('session opened') || msgLower.includes('session closed') || msgLower.includes('new session') || msgLower.includes('removed session')) {
    const mUser = message.match(/for\s+(?:user\s+)?(\S+)/)
    const action = msgLower.includes('opened') || msgLower.includes('new') ? 'opened' : 'closed'
    return {
      event: 'session',
      user: cleanUser(mUser?.[1] ?? null),
      sourceIP: null,
      details: `session ${action}`,
    }
  }

  return { event: 'other', user: null, sourceIP: null, details: cleanDetails(message) }
}

function parseAuthLine(line: string): AuthLogEntry | RawLogEntry {
  const sysMatch = SYSLOG_RE.exec(line)
  if (!sysMatch) return { parsed: false, rawLine: line }

  const [, timestamp, , rest] = sysMatch
  const svcMatch = SERVICE_RE.exec(rest)
  if (!svcMatch) return { parsed: false, rawLine: line }

  const [, service, pid, message] = svcMatch
  const { event, sourceIP, user, details } = parseAuthEvent(service, message)

  return {
    parsed: true,
    timestamp,
    rawLine: line,
    service,
    pid: pid || '-',
    event,
    sourceIP,
    user,
    details,
  }
}

const AUTH_EVENT_COLORS: Record<AuthEvent, string> = {
  success: '#00c471',
  failure: '#f04452',
  sudo: '#f59e0b',
  session: '#3182f6',
  other: '#6b7280',
}

const authColumns: ColumnDef<AuthLogEntry>[] = [
  {
    key: 'timestamp',
    i18nKey: 'logs.col.timestamp',
    width: '140px',
    render: (e) => ({ text: shortTimestamp(e.timestamp) }),
  },
  {
    key: 'service',
    i18nKey: 'logs.col.service',
    width: '100px',
    render: (e) => ({ text: e.service }),
  },
  {
    key: 'event',
    i18nKey: 'logs.col.event',
    width: '80px',
    render: (e) => ({ text: e.event, color: AUTH_EVENT_COLORS[e.event], pill: true }),
  },
  {
    key: 'sourceIP',
    i18nKey: 'logs.col.sourceIP',
    width: '130px',
    render: (e) => ({ text: e.sourceIP || '-' }),
  },
  {
    key: 'user',
    i18nKey: 'logs.col.user',
    width: '100px',
    render: (e) => ({ text: e.user || '-' }),
  },
  {
    key: 'details',
    i18nKey: 'logs.col.details',
    render: (e) => ({ text: e.details }),
  },
]

// --- Firewall unified parser (UFW + DOCKER-USER) ---

// Matches both UFW and DOCKER-USER kernel log entries
const UFW_ACTION_RE = /\[UFW\s+(BLOCK|ALLOW|AUDIT|LIMIT)\]/
const DOCKER_FW_ACTION_RE = /\[DOCKER-USER\s+(DROP|ACCEPT)\]/
const DOCKER_FW_HPORT_RE = /HPORT=(\d+)/

export type FirewallSource = 'UFW' | 'Docker'
export type FirewallAction = 'BLOCK' | 'ALLOW' | 'AUDIT' | 'LIMIT' | 'DROP' | 'ACCEPT'

export interface FirewallLogEntry extends ParsedLogEntry {
  source: FirewallSource
  action: FirewallAction
  sourceIP: string
  destPort: string
  protocol: string
  iface: string
}

function extractKV(text: string, key: string): string {
  const re = new RegExp(`${key}=(\\S+)`)
  const m = re.exec(text)
  return m?.[1] ?? '-'
}

export function parseFirewallLine(line: string): FirewallLogEntry | RawLogEntry {
  const sysMatch = SYSLOG_RE.exec(line)
  if (!sysMatch) return { parsed: false, rawLine: line }

  const [, timestamp, , rest] = sysMatch

  // Try UFW first
  const ufwMatch = UFW_ACTION_RE.exec(rest)
  if (ufwMatch) {
    return {
      parsed: true,
      timestamp,
      rawLine: line,
      source: 'UFW',
      action: ufwMatch[1] as FirewallAction,
      sourceIP: extractKV(rest, 'SRC'),
      destPort: extractKV(rest, 'DPT'),
      protocol: extractKV(rest, 'PROTO').toUpperCase(),
      iface: extractKV(rest, 'IN') || extractKV(rest, 'OUT'),
    }
  }

  // Try DOCKER-USER
  const dockerMatch = DOCKER_FW_ACTION_RE.exec(rest)
  if (dockerMatch) {
    // Prefer HPORT (host port from LOG prefix) over DPT (container port after DNAT)
    const hportMatch = DOCKER_FW_HPORT_RE.exec(rest)
    const destPort = hportMatch ? hportMatch[1] : extractKV(rest, 'DPT')
    return {
      parsed: true,
      timestamp,
      rawLine: line,
      source: 'Docker',
      action: dockerMatch[1] as FirewallAction,
      sourceIP: extractKV(rest, 'SRC'),
      destPort,
      protocol: extractKV(rest, 'PROTO').toUpperCase(),
      iface: extractKV(rest, 'IN') || extractKV(rest, 'OUT'),
    }
  }

  return { parsed: false, rawLine: line }
}

const FIREWALL_ACTION_COLORS: Record<string, string> = {
  BLOCK: '#f04452',
  DROP: '#f04452',
  ALLOW: '#00c471',
  ACCEPT: '#00c471',
  AUDIT: '#3182f6',
  LIMIT: '#f59e0b',
}

const FIREWALL_SOURCE_COLORS: Record<FirewallSource, string> = {
  UFW: '#3182f6',
  Docker: '#f59e0b',
}

const firewallColumns: ColumnDef<FirewallLogEntry>[] = [
  {
    key: 'timestamp',
    i18nKey: 'logs.col.timestamp',
    width: '140px',
    render: (e) => ({ text: shortTimestamp(e.timestamp) }),
  },
  {
    key: 'source',
    i18nKey: 'logs.col.source',
    width: '70px',
    render: (e) => ({ text: e.source, color: FIREWALL_SOURCE_COLORS[e.source], pill: true }),
  },
  {
    key: 'action',
    i18nKey: 'logs.col.action',
    width: '80px',
    render: (e) => ({ text: e.action, color: FIREWALL_ACTION_COLORS[e.action], pill: true }),
  },
  {
    key: 'sourceIP',
    i18nKey: 'logs.col.sourceIP',
    width: '130px',
    render: (e) => ({ text: e.sourceIP }),
  },
  {
    key: 'destPort',
    i18nKey: 'logs.col.destPort',
    width: '80px',
    render: (e) => ({ text: e.destPort }),
  },
  {
    key: 'protocol',
    i18nKey: 'logs.col.protocol',
    width: '80px',
    render: (e) => ({ text: e.protocol }),
  },
  {
    key: 'iface',
    i18nKey: 'logs.col.interface',
    width: '80px',
    render: (e) => ({ text: e.iface }),
  },
]

// --- sfpanel.log parser ---

// SFPanel logs come in two formats:
// 1. Echo JSON: {"time":"...","method":"GET","uri":"/api/...","status":200,...}
// 2. Go log: "2026/02/27 21:53:11 Some message"

export type SFPanelLogType = 'request' | 'startup' | 'other'

export interface SFPanelLogEntry extends ParsedLogEntry {
  logType: SFPanelLogType
  method: string
  uri: string
  status: number
  latency: string
  remoteIP: string
  message: string
}

const GO_LOG_RE = /^(\d{4}\/\d{2}\/\d{2}\s+\d{2}:\d{2}:\d{2})\s+(.*)/

function statusColor(status: number): string {
  if (status >= 500) return '#f04452'
  if (status >= 400) return '#f59e0b'
  if (status >= 300) return '#3182f6'
  if (status >= 200) return '#00c471'
  return '#6b7280'
}

function methodColor(method: string): string {
  switch (method) {
    case 'GET': return '#3182f6'
    case 'POST': return '#00c471'
    case 'PUT': return '#f59e0b'
    case 'DELETE': return '#f04452'
    default: return '#6b7280'
  }
}

function parseSFPanelLine(line: string): SFPanelLogEntry | RawLogEntry {
  // Try Echo JSON format first
  if (line.startsWith('{')) {
    try {
      const obj = JSON.parse(line)
      if (obj.time && obj.method && obj.uri) {
        // Strip query params (may contain tokens)
        let uri = obj.uri as string
        const qIdx = uri.indexOf('?')
        if (qIdx !== -1) uri = uri.substring(0, qIdx)

        return {
          parsed: true,
          timestamp: obj.time,
          rawLine: line,
          logType: 'request',
          method: obj.method,
          uri,
          status: obj.status ?? 0,
          latency: obj.latency_human ?? '-',
          remoteIP: obj.remote_ip ?? '-',
          message: '',
        }
      }
    } catch {
      // Not valid JSON, fall through
    }
  }

  // Try Go log format: "2026/02/27 21:53:11 message"
  const goMatch = GO_LOG_RE.exec(line)
  if (goMatch) {
    return {
      parsed: true,
      timestamp: goMatch[1],
      rawLine: line,
      logType: 'startup',
      method: '',
      uri: '',
      status: 0,
      latency: '',
      remoteIP: '',
      message: goMatch[2],
    }
  }

  // Echo startup banner
  if (line.startsWith('\u21e8')) {
    return {
      parsed: true,
      timestamp: '',
      rawLine: line,
      logType: 'startup',
      method: '',
      uri: '',
      status: 0,
      latency: '',
      remoteIP: '',
      message: line,
    }
  }

  return { parsed: false, rawLine: line }
}

const sfpanelColumns: ColumnDef<SFPanelLogEntry>[] = [
  {
    key: 'timestamp',
    i18nKey: 'logs.col.timestamp',
    width: '140px',
    render: (e) => ({ text: shortTimestamp(e.timestamp) }),
  },
  {
    key: 'method',
    i18nKey: 'logs.col.method',
    width: '70px',
    render: (e) => {
      if (e.logType !== 'request') return { text: '' }
      return { text: e.method, color: methodColor(e.method), pill: true }
    },
  },
  {
    key: 'status',
    i18nKey: 'logs.col.status',
    width: '60px',
    render: (e) => {
      if (e.logType !== 'request') return { text: '' }
      return { text: String(e.status), color: statusColor(e.status), pill: true }
    },
  },
  {
    key: 'latency',
    i18nKey: 'logs.col.latency',
    width: '100px',
    render: (e) => ({ text: e.logType === 'request' ? e.latency : '' }),
  },
  {
    key: 'remoteIP',
    i18nKey: 'logs.col.sourceIP',
    width: '120px',
    render: (e) => ({ text: e.logType === 'request' ? e.remoteIP : '' }),
  },
  {
    key: 'details',
    i18nKey: 'logs.col.details',
    render: (e) => {
      if (e.logType === 'request') return { text: e.uri }
      return { text: e.message }
    },
  },
]

// --- Registry ---

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const LOG_PARSERS: Record<string, LogParser<any>> = {
  'auth': { parse: parseAuthLine, columns: authColumns },
  'firewall': { parse: parseFirewallLine, columns: firewallColumns },
  'sfpanel': { parse: parseSFPanelLine, columns: sfpanelColumns },
}

export function hasParsedView(sourceId: string | null): boolean {
  if (!sourceId) return false
  return sourceId in LOG_PARSERS
}

export function getParser(sourceId: string): LogParser<ParsedLogEntry> | null {
  return LOG_PARSERS[sourceId] ?? null
}

export function parseLogLines(sourceId: string, lines: string[]): LogEntry[] {
  const parser = LOG_PARSERS[sourceId]
  if (!parser) return lines.map((l) => ({ parsed: false, rawLine: l }))
  return lines.map((l) => parser.parse(l))
}
