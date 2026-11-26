import { describe, expect, it } from 'vitest'
import { menuHref } from './serverScope'
import { dirParam, fleetRunHrefFor, terminalDirHref } from './serverLinks'

const at = (href: string) => new URL(href, 'http://x')

describe('server links', () => {
  // A link to another server screen goes through menuHref, which keeps the
  // link's own query and adds the server shown.
  it('names the server only in a cluster, once the server is known', () => {
    expect(at(menuHref('/terminal?dir=%2Fsrv', true, 'agent-1')).searchParams.get('node')).toBe('agent-1')
    expect(at(menuHref('/terminal?dir=%2Fsrv', true, 'agent-1')).searchParams.get('dir')).toBe('/srv')
    expect(menuHref('/terminal?dir=%2Fsrv', false, 'agent-1')).toBe('/terminal?dir=%2Fsrv')
    expect(menuHref('/services', false, 'agent-1')).toBe('/services')
    expect(menuHref('/services', true, '')).toBe('/services')
  })
})

describe('terminal folder', () => {
  it('a folder goes into the address and comes back as it was', () => {
    for (const dir of ['/srv/app', '/srv/my app', '/srv/한글/a&b', '/opt/stacks/x#1', '/a%20b', '/a+b?c=d'])
      expect(dirParam(at(terminalDirHref(dir)).search)).toBe(dir)
  })

  it('keeps the terminal path', () => {
    expect(at(terminalDirHref('/srv/app')).pathname).toBe('/terminal')
  })

  it('only an absolute folder is taken', () => {
    for (const s of ['', '?dir=', '?dir=srv/app', '?dir=%2Fa%00b', '?dir=%2Fa%0Ab', '?dir=%2Fa%7Fb', '?x=%2Fsrv', `?dir=/${'a'.repeat(4096)}`])
      expect(dirParam(s), s.slice(0, 40)).toBeNull()
    expect(dirParam(`?dir=/${'a'.repeat(4095)}`)).toBe(`/${'a'.repeat(4095)}`)
  })
})

describe('group task links', () => {
  it('a group task is offered only in a cluster, for the server on screen', () => {
    expect(fleetRunHrefFor(false, 'm-1', 2)).toBeNull()
    expect(fleetRunHrefFor(null, 'm-1', 2)).toBeNull()
    expect(fleetRunHrefFor(true, '', 2)).toBeNull()
    const u = at(fleetRunHrefFor(true, 'agent-1', 2)!('builtin/service-restart', { service: 'cron.service' }))
    expect(u.pathname).toBe('/cluster/tasks/builtin%2Fservice-restart')
    expect(Object.fromEntries(u.searchParams)).toEqual({ nodes: 'agent-1', 'input.service': 'cron.service' })
  })

  // A cluster of one has no other server to add; an empty list is not known yet.
  it('a cluster of one server, or one whose list has not come, is offered none', () => {
    expect(fleetRunHrefFor(true, 'm-1', 1)).toBeNull()
    expect(fleetRunHrefFor(true, 'm-1', 0)).toBeNull()
    expect(fleetRunHrefFor(true, 'm-1', 2)).not.toBeNull()
  })

  it('a task without inputs is opened with the server alone', () => {
    const u = at(fleetRunHrefFor(true, 'agent-1', 2)!('builtin/package-update'))
    expect(u.pathname).toBe('/cluster/tasks/builtin%2Fpackage-update')
    expect(Object.fromEntries(u.searchParams)).toEqual({ nodes: 'agent-1' })
  })
})
