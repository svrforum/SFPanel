import { describe, expect, it } from 'vitest'
import { taskRunHref } from '@/lib/taskPrefill'
import type { AgentConnectionState, AgentSettingsView, AgentStatus, WazuhAgentStatus, ZabbixAgentStatus } from '@/types/api'
import {
  configured, connectionTone, deployHref, reapplyNodes, sameActive, sameHost, sameServers, wazuhCell, zabbixCell,
} from './agentsModel'

const settings: AgentSettingsView = { saved: true, zabbix: { server: '192.0.2.10', server_active: '192.0.2.10', host_metadata: 'linux-fleet-auto' }, wazuh: { manager: '192.0.2.10', groups: '', version: '4.14.10' } }

const installedZabbix: ZabbixAgentStatus = {
  state: 'installed', version: '1:7.0.32-1+ubuntu24.04', source: 'vendor', service: 'active',
  config: { server: '192.0.2.10', server_active: '192.0.2.10', hostname: 'node-a', host_metadata: 'linux-fleet-auto' },
  connection: { state: 'connected' },
}

const installedWazuh: WazuhAgentStatus = {
  state: 'installed', version: '4.14.10-1', held: true, service: 'active',
  config: { servers: ['192.0.2.10'], enrollment_manager: '192.0.2.10', groups: '' },
  connection: { state: 'connected', last_keepalive: '2026-11-26 10:00:00' },
}

const status = (zabbix: ZabbixAgentStatus, wazuh: WazuhAgentStatus): AgentStatus => ({
  os: { id: 'ubuntu', version_id: '24.04', codename: 'noble', pretty: 'Ubuntu 24.04 LTS' }, zabbix, wazuh, checked_at: '2026-11-26T01:00:00Z',
})

const rows = [
  { id: 'm-1', status: status(installedZabbix, { state: 'none' }) },
  { id: 'm-2', status: status({ state: 'none' }, installedWazuh) },
  { id: 'a-1', status: status({ ...installedZabbix, service: 'inactive' }, installedWazuh) },
  { id: 'a-2' },
  { id: 'a-3', status: status({ state: 'legacy', version: '1:6.0.14-1', source: 'distro' }, { state: 'unmanaged' }) },
  { id: 'a-4', status: status({ state: 'unmanaged', source: 'other', service: 'active' }, { state: 'none' }) },
]

describe('an agent cell', () => {
  it('an active service is not a connection', () => {
    const cell = zabbixCell({ state: 'installed', service: 'active', config: { server: '192.0.2.19', server_active: '192.0.2.19', hostname: 'agent-1', host_metadata: '' }, connection: { state: 'unreachable', detail: 'dial tcp 192.0.2.19:10051: i/o timeout' } }, settings)
    expect(cell.install).toBe('running')
    expect(cell.connection?.tone).toBe('destructive')
    expect(cell.match).toBe('different')
    expect(cell.fileValue).toBe('Server=192.0.2.19 · ServerActive=192.0.2.19')
  })

  it('carries why it is not connected and when it last was', () => {
    expect(zabbixCell({ ...installedZabbix, connection: { state: 'unreachable', detail: 'dial tcp 192.0.2.19:10051: i/o timeout' } }, settings).connection)
      .toEqual({ state: 'unreachable', tone: 'destructive', detail: 'dial tcp 192.0.2.19:10051: i/o timeout' })
    expect(wazuhCell(installedWazuh, settings).connection).toEqual({ state: 'connected', tone: 'success', lastKeepalive: '2026-11-26 10:00:00' })
  })

  it('only connected is success', () => {
    const tones = Object.fromEntries((['connected', 'pending', 'disconnected', 'never', 'stale', 'unreachable', 'not_registered', 'failing', 'not_running', 'no_server', 'unknown'] as const).map((s) => [s, connectionTone(s)]))
    expect(tones).toEqual({ connected: 'success', pending: 'warning', never: 'warning', not_registered: 'warning', stale: 'warning', unreachable: 'destructive', disconnected: 'destructive', failing: 'destructive', not_running: 'destructive', no_server: 'muted', unknown: 'muted' })
  })

  it('a state a newer node sends is not success', () => {
    expect(connectionTone('reconnecting' as AgentConnectionState)).toBe('muted')
  })

  it('an installed agent runs only while its service is active', () => {
    expect(zabbixCell(installedZabbix, settings).install).toBe('running')
    expect(zabbixCell({ ...installedZabbix, service: 'inactive' }, settings).install).toBe('stopped')
    expect(wazuhCell({ ...installedWazuh, service: 'failed' }, settings).install).toBe('stopped')
    expect(wazuhCell({ ...installedWazuh, service: undefined }, settings).install).toBe('stopped')
  })

  it('names the states it leaves alone as they are', () => {
    expect(zabbixCell({ state: 'none' }, settings).install).toBe('none')
    expect(zabbixCell({ state: 'legacy', version: '1:6.0.14-1', source: 'distro' }, settings).install).toBe('legacy')
    expect(zabbixCell({ state: 'unmanaged', source: 'other', service: 'active' }, settings).install).toBe('unmanaged')
    expect(wazuhCell({ state: 'unmanaged' }, settings).install).toBe('unmanaged')
  })

  it('gives an agent that is not a package no package source', () => {
    // Go names an agent outside its package "other" for want of a version: no repository to show.
    expect(zabbixCell({ state: 'unmanaged', source: 'other', service: 'active' }, settings).source).toBeUndefined()
    expect(zabbixCell({ state: 'legacy', source: 'other' }, settings).source).toBeUndefined()
    expect(zabbixCell({ state: 'legacy', version: '1:6.0.14-1', source: 'distro' }, settings)).toMatchObject({ version: '1:6.0.14-1', source: 'distro' })
    expect(zabbixCell(installedZabbix, settings)).toMatchObject({ version: '1:7.0.32-1+ubuntu24.04', source: 'vendor' })
  })

  it('says whether Wazuh is held at its version', () => {
    expect(wazuhCell(installedWazuh, settings)).toMatchObject({ version: '4.14.10-1', held: true })
    expect(wazuhCell({ ...installedWazuh, held: undefined }, settings).held).toBe(false)
  })

  it('matches Zabbix when both of its values are the central ones', () => {
    const cell = zabbixCell({ ...installedZabbix, config: { ...installedZabbix.config!, server: ' 192.0.2.10 ', server_active: '192.0.2.10:10051' } }, settings)
    expect(cell.match).toBe('same')
    expect(cell.fileValue).toBeUndefined()
  })

  it('a Zabbix whose active address alone differs does not match', () => {
    const cell = zabbixCell({ ...installedZabbix, config: { ...installedZabbix.config!, server_active: '192.0.2.19' } }, settings)
    expect(cell.match).toBe('different')
    expect(cell.fileValue).toBe('Server=192.0.2.10 · ServerActive=192.0.2.19')
  })

  it('a Wazuh with one matching manager matches, two do not', () => {
    expect(wazuhCell(installedWazuh, settings).match).toBe('same')
    const two = wazuhCell({ ...installedWazuh, config: { ...installedWazuh.config!, servers: ['192.0.2.10', '192.0.2.19'] } }, settings)
    expect(two.match).toBe('different')
    expect(two.fileValue).toBe('192.0.2.10, 192.0.2.19')
    expect(wazuhCell({ ...installedWazuh, config: { ...installedWazuh.config!, servers: ['192.0.2.19'] } }, settings)).toMatchObject({ match: 'different', fileValue: '192.0.2.19' })
  })

  it('no central value reads as unset', () => expect(zabbixCell(installedZabbix, { ...settings, zabbix: { server: '', server_active: '', host_metadata: '' } }).match).toBe('unset'))

  it('values not read yet, or an agent half set, read as unset', () => {
    expect(zabbixCell(installedZabbix, null).match).toBe('unset')
    expect(zabbixCell(installedZabbix, { ...settings, zabbix: { ...settings.zabbix, server_active: '' } }).match).toBe('unset')
    expect(wazuhCell(installedWazuh, { ...settings, wazuh: { ...settings.wazuh, manager: '' } }).match).toBe('unset')
  })

  it('an agent without its config file has nothing to match', () => {
    expect(zabbixCell({ ...installedZabbix, config: undefined }, settings).match).toBe('noConfig')
    expect(wazuhCell({ ...installedWazuh, config: undefined }, settings).match).toBe('noConfig')
  })

  it('keeps why ossec.conf could not be read, which is not a missing file', () => {
    const cell = wazuhCell({ ...installedWazuh, config: undefined, config_error: 'ossec.conf is not well-formed XML' }, settings)
    expect(cell.configError).toBe('ossec.conf is not well-formed XML')
    expect(wazuhCell(installedWazuh, settings).configError).toBeUndefined()
  })
})

describe('comparing addresses', () => {
  it('the same servers in another order and spacing match', () => expect(sameServers('192.0.2.10, zbx.example.net', 'ZBX.example.net,192.0.2.10')).toBe(true))

  it('a server more or fewer does not match', () => {
    expect(sameServers('192.0.2.10,192.0.2.11', '192.0.2.10')).toBe(false)
    expect(sameServers('192.0.2.10', '')).toBe(false)
    expect(sameServers('192.0.2.10,', '192.0.2.10')).toBe(true)
  })

  it('an active address without a port is on 10051', () => {
    expect(sameActive('192.0.2.10', '192.0.2.10:10051')).toBe(true)
    expect(sameActive('192.0.2.10:10052', '192.0.2.10')).toBe(false)
  })

  it('an active address is the same host in any case, and an empty one matches no address', () => {
    expect(sameActive(' ZBX.example.net:10051', 'zbx.example.net')).toBe(true)
    expect(sameActive('', '192.0.2.10')).toBe(false)
    expect(sameActive('192.0.2.19', '192.0.2.10')).toBe(false)
    expect(sameActive('192.0.2.10,192.0.2.11', '192.0.2.10')).toBe(false)
  })

  it('a host is the same in any case and spacing', () => {
    expect(sameHost(' ZBX.example.net ', 'zbx.example.net')).toBe(true)
    expect(sameHost('192.0.2.10', '192.0.2.19')).toBe(false)
  })
})

describe('what can be deployed', () => {
  it('Zabbix needs both of its addresses, Wazuh its manager', () => {
    expect(configured(settings, 'zabbix')).toBe(true)
    expect(configured(settings, 'wazuh')).toBe(true)
    expect(configured({ ...settings, zabbix: { ...settings.zabbix, server_active: ' ' } }, 'zabbix')).toBe(false)
    expect(configured({ ...settings, wazuh: { ...settings.wazuh, manager: '' } }, 'wazuh')).toBe(false)
    expect(configured(null, 'zabbix')).toBe(false)
  })

  it('re-apply takes the servers that have the agent installed', () => expect(reapplyNodes(rows, 'zabbix')).toEqual(['m-1', 'a-1']))

  it('re-apply leaves out a server whose agent is not its package', () => expect(reapplyNodes(rows, 'wazuh')).toEqual(['m-2', 'a-1']))

  it('deploy opens the built-in with the servers', () => expect(deployHref('zabbix', ['m-1', 'a-1'])).toBe(taskRunHref('builtin/zabbix-agent', { nodes: ['m-1', 'a-1'] })))

  it('deploys each agent with its own built-in', () => {
    expect(deployHref('wazuh', ['a-1'])).toBe('/cluster/tasks/builtin%2Fwazuh-agent?nodes=a-1')
    expect(deployHref('zabbix', ['m-1', 'a-1'])).toBe('/cluster/tasks/builtin%2Fzabbix-agent?nodes=m-1%2Ca-1')
  })
})
