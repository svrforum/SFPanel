import { describe, expect, it } from 'vitest'
import { BOTTOM_NAV_ITEMS, MORE_MENU_ITEMS, NAV_ITEMS, NODE_MENU_ITEMS } from './navigation'

describe('NAV_ITEMS registry', () => {
  it('has a unique route per entry', () => {
    const routes = NAV_ITEMS.map((i) => i.to)
    expect(new Set(routes).size).toBe(routes.length)
  })

  it('gives every entry a label key and an icon', () => {
    for (const item of NAV_ITEMS) {
      expect(item.to.startsWith('/')).toBe(true)
      expect(item.labelKey).toMatch(/^layout\.nav\./)
      // lucide-react components are forwardRef objects, not plain functions.
      expect(item.icon).toBeTruthy()
    }
  })
})

describe('BOTTOM_NAV_ITEMS', () => {
  // BOTTOM_NAV_ITEMS is built by looking each route up with a non-null
  // assertion, so a route renamed in NAV_ITEMS without updating the order list
  // would silently produce `undefined` holes rather than a build error.
  it('resolves every route in the explicit order list', () => {
    expect(BOTTOM_NAV_ITEMS.every(Boolean)).toBe(true)
  })

  it('lists the four mobile tabs with terminal before logs', () => {
    expect(BOTTOM_NAV_ITEMS.map((i) => i.to)).toEqual(['/dashboard', '/docker', '/terminal', '/logs'])
  })

  it('matches exactly the entries flagged bottomNav in the registry', () => {
    const flagged = NAV_ITEMS.filter((i) => i.bottomNav).map((i) => i.to)
    expect(new Set(BOTTOM_NAV_ITEMS.map((i) => i.to))).toEqual(new Set(flagged))
  })
})

describe('MORE_MENU_ITEMS', () => {
  it('is the exact complement of the bottom bar', () => {
    const bottom = new Set(BOTTOM_NAV_ITEMS.map((i) => i.to))
    expect(MORE_MENU_ITEMS.some((i) => bottom.has(i.to))).toBe(false)
    expect(MORE_MENU_ITEMS.length + BOTTOM_NAV_ITEMS.length).toBe(NAV_ITEMS.length)
  })

  it('preserves registry order', () => {
    const expected = NAV_ITEMS.filter((i) => !i.bottomNav).map((i) => i.to)
    expect(MORE_MENU_ITEMS.map((i) => i.to)).toEqual(expected)
  })
})

describe('NODE_MENU_ITEMS', () => {
  it('drops the cluster page', () => {
    expect(NODE_MENU_ITEMS.map((i) => i.to)).not.toContain('/cluster')
    expect(NODE_MENU_ITEMS.length).toBe(NAV_ITEMS.length - 1)
  })

  it('scopes settings to the node', () => {
    const settings = NODE_MENU_ITEMS.filter((i) => i.to.startsWith('/settings'))
    expect(settings.map((i) => i.to)).toEqual(['/settings?scope=node'])
  })

  it('keeps every other route untouched and in registry order', () => {
    const expected = NAV_ITEMS.filter((i) => i.to !== '/cluster').map((i) =>
      i.to === '/settings' ? '/settings?scope=node' : i.to
    )
    expect(NODE_MENU_ITEMS.map((i) => i.to)).toEqual(expected)
  })

  it('does not mutate the shared registry entries', () => {
    expect(NAV_ITEMS.find((i) => i.labelKey === 'layout.nav.settings')?.to).toBe('/settings')
  })
})
