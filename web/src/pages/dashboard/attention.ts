import type { AlertHistoryEntry, RecentContainerEvent } from '@/types/api'

export type AttentionKind = 'container' | 'alert' | 'service'

export interface AttentionItem {
  key: string
  kind: AttentionKind
  /** What it happened to — a container name, a rule name, a unit name. */
  subject: string
  /** The event in machine terms; the component turns it into a sentence. */
  detail: string
  ts: number
  severity: 'warning' | 'critical'
  to: string
}

/**
 * Container events worth a person's attention.
 *
 * `die` with exit 0 is somebody stopping something on purpose and is not news;
 * `start`, `stop` and `healthy` are the system working. What is left is the
 * set that means something went wrong on its own.
 */
const INTERESTING = new Set(['oom', 'kill', 'unhealthy', 'restart'])

/** How far back an event still counts as "what changed". */
export const ATTENTION_WINDOW_MS = 24 * 60 * 60 * 1000

/** At most this many rows, so a crash loop cannot fill the page. */
export const ATTENTION_MAX = 6

function isInteresting(e: RecentContainerEvent): boolean {
  if (INTERESTING.has(e.event_type)) return true
  return e.event_type === 'die' && e.exit_code != null && e.exit_code !== 0
}

/**
 * Fold three feeds the panel already records into one short list.
 *
 * Each of these was collected on a timer and readable through an existing
 * route, and none of them appeared on the page an operator actually opens:
 * container events were persisted with exit codes and had no frontend caller
 * at all, fired alerts were reachable only through Settings, and failed units
 * were a field on a list the dashboard never fetched.
 *
 * Deduplicated per subject, because a container in a restart loop writes an
 * event every few seconds and six rows about one container tell you less than
 * one row does.
 */
export function buildAttentionItems(input: {
  events?: RecentContainerEvent[] | null
  alerts?: AlertHistoryEntry[] | null
  failedUnits?: Array<{ name: string }> | null
  now: number
  windowMs?: number
}): AttentionItem[] {
  const windowMs = input.windowMs ?? ATTENTION_WINDOW_MS
  const cutoff = input.now - windowMs
  const items: AttentionItem[] = []
  const seen = new Set<string>()

  for (const e of input.events ?? []) {
    if (!isInteresting(e) || e.ts < cutoff) continue
    const key = `container:${e.container_id}`
    if (seen.has(key)) continue
    seen.add(key)
    items.push({
      key,
      kind: 'container',
      subject: e.container_name || e.container_id.slice(0, 12),
      detail: e.exit_code != null && e.exit_code !== 0 ? `${e.event_type}:${e.exit_code}` : e.event_type,
      ts: e.ts,
      severity: e.event_type === 'oom' ? 'critical' : 'warning',
      to: '/docker',
    })
  }

  for (const a of input.alerts ?? []) {
    const ts = Date.parse(a.created_at)
    // A row whose timestamp will not parse is dropped rather than sorted to
    // 1970, where it would pin itself to the bottom of every list forever.
    if (!Number.isFinite(ts) || ts < cutoff) continue
    const key = `alert:${a.id}`
    if (seen.has(key)) continue
    seen.add(key)
    items.push({
      key,
      kind: 'alert',
      subject: a.rule_name,
      detail: a.message ?? '',
      ts,
      severity: a.severity === 'critical' ? 'critical' : 'warning',
      to: '/settings?scope=node&tab=alerts',
    })
  }

  for (const u of input.failedUnits ?? []) {
    const key = `service:${u.name}`
    if (seen.has(key)) continue
    seen.add(key)
    items.push({
      key,
      kind: 'service',
      subject: u.name,
      detail: 'failed',
      // systemd does not say when it failed on the list endpoint, so these
      // sort to the top: a failed unit is a current state, not an event that
      // happened once and is now over.
      ts: input.now,
      severity: 'critical',
      to: '/services',
    })
  }

  items.sort((a, b) => b.ts - a.ts)
  return items.slice(0, ATTENTION_MAX)
}
