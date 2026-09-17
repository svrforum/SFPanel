import { describe, expect, it } from 'vitest'
import type { AISession } from '@/types/api'
import { PTY_GROUP_KEY, activeKey, buildRail, findItem, parseActiveKey, pickActive, railNote } from './sessionRail'

const s = (id: string, cwd: string, created: string, extra: Partial<AISession> = {}): AISession => ({
  id, tool: 'claude', title: id, run_as: 'root', cwd, state: 'working', persistence: 'service', attached: false, created_at: created, ...extra,
})
const opts = { fallback: false, temporaryLabel: 'Temporary' }

describe('buildRail', () => {
  it('groups by exact cwd and orders groups by their oldest session', () => {
    const groups = buildRail([
      s('b1', '/home/alice', '2026-09-17T10:00:00Z'),
      s('a1', '/opt/stacks/app', '2026-09-17T09:00:00Z'),
      s('b2', '/home/alice', '2026-09-17T08:00:00Z'),
      s('c1', '/opt/stacks/app/', '2026-09-17T07:00:00Z'), // trailing slash is a different key, by design: the server stores what was typed
    ], [], opts)
    expect(groups.map((g) => g.key)).toEqual(['/opt/stacks/app/', '/home/alice', '/opt/stacks/app'])
    expect(groups.map((g) => g.label)).toEqual(['app', 'alice', 'app'])
    expect(groups[1].items.map((i) => i.id)).toEqual(['b1', 'b2'])
  })

  it('keeps server order inside a group, ended sessions last', () => {
    const groups = buildRail([
      s('x', '/d', '2026-09-17T09:00:00Z', { state: 'ended' }),
      s('y', '/d', '2026-09-17T09:01:00Z'),
      s('z', '/d', '2026-09-17T09:02:00Z', { state: 'shell' }),
    ], [], opts)
    expect(groups[0].items.map((i) => i.id)).toEqual(['y', 'z', 'x'])
  })

  it('appends new directories instead of reshuffling: a newer group never moves above an older one', () => {
    const older = s('o', '/old', '2026-09-17T01:00:00Z')
    const before = buildRail([older], [], opts).map((g) => g.key)
    const after = buildRail([s('n', '/new', '2026-09-17T02:00:00Z'), older], [], opts).map((g) => g.key)
    expect(before).toEqual(['/old'])
    expect(after).toEqual(['/old', '/new'])
  })

  it('shows the temporary group only when it has tabs or the page is in fallback mode', () => {
    expect(buildRail([], [], opts)).toEqual([])
    const withTabs = buildRail([s('a', '/a', '2026-09-17T01:00:00Z')], [{ id: 'term-1', title: 'Terminal 1' }], opts)
    expect(withTabs.map((g) => g.key)).toEqual(['/a', PTY_GROUP_KEY])
    expect(withTabs[1]).toMatchObject({ temporary: true, label: 'Temporary' })
    expect(withTabs[1].items[0]).toEqual({ kind: 'pty', id: 'term-1', tab: { id: 'term-1', title: 'Terminal 1' } })
    const fallback = buildRail([], [], { ...opts, fallback: true })
    expect(fallback.map((g) => g.key)).toEqual([PTY_GROUP_KEY])
    expect(fallback[0].items).toEqual([])
  })

  it('tolerates a session without a parseable created_at (an unknown session)', () => {
    const groups = buildRail([s('u', '/u', '', { unknown: true }), s('k', '/k', '2026-09-17T01:00:00Z')], [], opts)
    expect(groups.map((g) => g.key)).toEqual(['/k', '/u'])
  })
})

describe('pickActive', () => {
  const groups = buildRail([
    s('a', '/a', '2026-09-17T01:00:00Z', { last_attached_at: '2026-09-17T05:00:00Z' }),
    s('b', '/a', '2026-09-17T02:00:00Z', { last_attached_at: '2026-09-17T06:00:00Z' }),
    s('e', '/a', '2026-09-17T03:00:00Z', { state: 'ended', last_attached_at: '2026-09-17T07:00:00Z' }),
  ], [{ id: 'term-1', title: 'T' }], opts)

  it('keeps the persisted key while it exists', () => {
    expect(pickActive(groups, 'pty:term-1')).toBe('pty:term-1')
    expect(pickActive(groups, 'tmux:a')).toBe('tmux:a')
  })
  it('falls back to the live tmux session attached most recently — an ended one does not count', () => {
    expect(pickActive(groups, 'tmux:gone')).toBe('tmux:b')
    expect(pickActive(groups, null)).toBe('tmux:b')
  })
  it('falls back to the first item, then to nothing', () => {
    const noAttach = buildRail([s('a', '/a', '2026-09-17T01:00:00Z')], [], opts)
    expect(pickActive(noAttach, null)).toBe('tmux:a')
    expect(pickActive([], 'tmux:a')).toBeNull()
  })
})

describe('keys', () => {
  it('namespaces by engine and reads the old page\'s bare tab id as a PTY tab', () => {
    expect(activeKey({ kind: 'tmux', id: 'abc', session: s('abc', '/', '') })).toBe('tmux:abc')
    expect(parseActiveKey('term-3')).toBe('pty:term-3')
    expect(parseActiveKey('tmux:abc')).toBe('tmux:abc')
    expect(parseActiveKey('')).toBeNull()
    expect(parseActiveKey(null)).toBeNull()
  })
  it('finds an item by key', () => {
    const groups = buildRail([s('a', '/a', '')], [{ id: 'term-1', title: 'T' }], opts)
    expect(findItem(groups, 'pty:term-1')?.kind).toBe('pty')
    expect(findItem(groups, 'tmux:a')?.id).toBe('a')
    expect(findItem(groups, 'tmux:zzz')).toBeNull()
    expect(findItem(groups, null)).toBeNull()
  })
})

describe('railNote', () => {
  it('says nothing for a shell session at its prompt, and names the exit for an AI tool', () => {
    expect(railNote(s('a', '/', '', { tool: 'shell', state: 'shell' }), false)).toBeNull()
    expect(railNote(s('a', '/', '', { tool: 'codex', state: 'shell' }), false)).toBe('toolExited')
  })
  it('flags waiting only when the operator is not looking at it', () => {
    expect(railNote(s('a', '/', '', { state: 'waiting' }), false)).toBe('waiting')
    expect(railNote(s('a', '/', '', { state: 'waiting' }), true)).toBeNull()
    expect(railNote(s('a', '/', '', { state: 'working' }), false)).toBeNull()
    expect(railNote(s('a', '/', '', { state: 'ended' }), true)).toBe('ended')
  })
})
