import { describe, expect, it } from 'vitest'
import { DEFAULT_SHORTCUTS, SHORTCUTS_KEY, readShortcuts } from './shortcuts'

// The pages a dashboard shortcut can open. The menu folded several of them
// into tabs; a shortcut someone saved to one of those must still be there.
function store(seed: Record<string, string>): Pick<Storage, 'getItem'> {
  const map = new Map(Object.entries(seed))
  return { getItem: (key: string) => map.get(key) ?? null }
}

const OLD = ['/running', '/docker', '/appstore', '/files', '/cron', '/logs', '/processes', '/services', '/network', '/disk', '/firewall', '/packages', '/terminal', '/cluster', '/settings']

describe('readShortcuts', () => {
  it('keeps every shortcut the old menu offered, in both modes', () => {
    for (const cluster of [true, false]) for (const to of OLD)
      expect(readShortcuts(store({ [SHORTCUTS_KEY]: JSON.stringify([to]) }), cluster)).toEqual([to])
  })

  it('drops unknown pages and duplicates, keeps order, caps at four', () => {
    expect(readShortcuts(store({ [SHORTCUTS_KEY]: JSON.stringify(['/nope', '/logs', '/files', '/logs', '/disk', '/cron', '/terminal']) }), false))
      .toEqual(['/logs', '/files', '/disk', '/cron'])
  })

  it('falls back to the defaults when nothing valid is saved', () => {
    expect(readShortcuts(store({ [SHORTCUTS_KEY]: '["/nope"]' }), true)).toEqual(DEFAULT_SHORTCUTS)
    expect(readShortcuts(store({ [SHORTCUTS_KEY]: '{bad' }), true)).toEqual(DEFAULT_SHORTCUTS)
  })

  it('keeps a saved map shortcut in both modes', () => {
    for (const cluster of [true, false])
      expect(readShortcuts(store({ [SHORTCUTS_KEY]: '["/map"]' }), cluster)).toEqual(['/map'])
  })

  it('offers the cluster-only pages only in a cluster', () => {
    expect(readShortcuts(store({ [SHORTCUTS_KEY]: '["/cluster/tasks"]' }), true)).toEqual(['/cluster/tasks'])
    expect(readShortcuts(store({ [SHORTCUTS_KEY]: '["/cluster/tasks"]' }), false)).toEqual(DEFAULT_SHORTCUTS)
  })
})
