import { describe, it, expect } from 'vitest'
import { buildAttentionItems, ATTENTION_MAX } from './attention'
import type { AlertHistoryEntry, RecentContainerEvent } from '@/types/api'

const NOW = 1_800_000_000_000

const ev = (o: Partial<RecentContainerEvent>): RecentContainerEvent => ({
  container_id: 'abc123def456',
  container_name: 'web',
  ts: NOW - 60_000,
  event_type: 'die',
  exit_code: 1,
  detail: null,
  ...o,
})

const alert = (o: Partial<AlertHistoryEntry>): AlertHistoryEntry => ({
  id: 1,
  rule_name: 'disk',
  type: 'disk',
  severity: 'warning',
  message: 'disk above 90%',
  node_id: '',
  sent_channels: '[]',
  created_at: new Date(NOW - 120_000).toISOString(),
  ...o,
})

describe('buildAttentionItems', () => {
  it('ignores the events that mean the system is working', () => {
    const items = buildAttentionItems({
      now: NOW,
      events: [
        ev({ container_id: 'a', event_type: 'start', exit_code: null }),
        ev({ container_id: 'b', event_type: 'stop', exit_code: null }),
        ev({ container_id: 'c', event_type: 'healthy', exit_code: null }),
        // A clean stop is somebody stopping something on purpose.
        ev({ container_id: 'd', event_type: 'die', exit_code: 0 }),
      ],
    })
    expect(items).toHaveLength(0)
  })

  it('keeps a non-zero exit, an OOM, a kill, an unhealthy and a restart', () => {
    const items = buildAttentionItems({
      now: NOW,
      events: [
        ev({ container_id: 'a', event_type: 'die', exit_code: 137 }),
        ev({ container_id: 'b', event_type: 'oom', exit_code: null }),
        ev({ container_id: 'c', event_type: 'kill', exit_code: null }),
        ev({ container_id: 'd', event_type: 'unhealthy', exit_code: null }),
        ev({ container_id: 'e', event_type: 'restart', exit_code: null }),
      ],
    })
    expect(items).toHaveLength(5)
    expect(items.find((i) => i.subject === 'web' && i.detail === 'die:137')).toBeTruthy()
    // An OOM is the one that reads as critical.
    expect(items.find((i) => i.detail === 'oom')?.severity).toBe('critical')
  })

  it('shows one row per container, not one per event in a restart loop', () => {
    const events = Array.from({ length: 40 }, (_, i) =>
      ev({ container_id: 'loop', container_name: 'loopy', event_type: 'restart', exit_code: null, ts: NOW - i * 5_000 }),
    )
    const items = buildAttentionItems({ now: NOW, events })
    expect(items).toHaveLength(1)
    // The most recent one survives, not whichever happened to be scanned last.
    expect(items[0].ts).toBe(NOW)
  })

  it('drops anything older than the window', () => {
    const items = buildAttentionItems({
      now: NOW,
      events: [ev({ container_id: 'old', ts: NOW - 48 * 60 * 60 * 1000, exit_code: 2 })],
      alerts: [alert({ id: 9, created_at: new Date(NOW - 48 * 60 * 60 * 1000).toISOString() })],
    })
    expect(items).toHaveLength(0)
  })

  it('drops an alert whose timestamp will not parse instead of dating it to 1970', () => {
    const items = buildAttentionItems({ now: NOW, alerts: [alert({ created_at: 'not a date' })] })
    expect(items).toHaveLength(0)
  })

  it('puts failed units first, since they are a present state not a past event', () => {
    const items = buildAttentionItems({
      now: NOW,
      events: [ev({ container_id: 'a', exit_code: 1, ts: NOW - 1000 })],
      failedUnits: [{ name: 'borg.service' }],
    })
    expect(items[0].kind).toBe('service')
    expect(items[0].severity).toBe('critical')
  })

  it('caps the list', () => {
    const events = Array.from({ length: 20 }, (_, i) =>
      ev({ container_id: `c${i}`, container_name: `c${i}`, exit_code: 1, ts: NOW - i * 1000 }),
    )
    expect(buildAttentionItems({ now: NOW, events })).toHaveLength(ATTENTION_MAX)
  })

  it('says nothing when there is nothing to say', () => {
    expect(buildAttentionItems({ now: NOW })).toHaveLength(0)
    expect(buildAttentionItems({ now: NOW, events: null, alerts: null, failedUnits: null })).toHaveLength(0)
  })
})
