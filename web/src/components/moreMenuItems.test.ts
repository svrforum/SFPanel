import { describe, it, expect } from 'vitest'
import { MORE_MENU_ITEMS, BOTTOM_NAV_ITEMS, NAV_ITEMS } from '@/lib/navigation'

/** The derivation MoreMenu applies in cluster mode. */
const nodeScoped = MORE_MENU_ITEMS.map((i) => (i.to === '/settings' ? '/settings?scope=node' : i.to))

describe('the drawer in cluster mode', () => {
  it('points settings at the node scope', () => {
    // Everything on the node-scoped settings page — panel update, backup,
    // restore, TLS, tuning, the audit log — had no route on a phone.
    expect(nodeScoped).toContain('/settings?scope=node')
    expect(nodeScoped).not.toContain('/settings')
  })

  it('does not repeat the bottom bar', () => {
    // NODE_MENU_ITEMS is the whole registry; deriving from it would list the
    // bottom tabs in the drawer beside themselves.
    const bottom = BOTTOM_NAV_ITEMS.map((i) => i.to)
    for (const path of nodeScoped) {
      expect(bottom).not.toContain(path)
    }
  })

  it('covers every registry entry exactly once across both surfaces', () => {
    const all = [...BOTTOM_NAV_ITEMS.map((i) => i.to), ...MORE_MENU_ITEMS.map((i) => i.to)]
    expect([...all].sort()).toEqual(NAV_ITEMS.map((i) => i.to).sort())
  })
})
