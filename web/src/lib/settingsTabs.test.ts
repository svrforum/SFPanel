import { describe, expect, it } from 'vitest'
import { SETTINGS_TABS, settingsRedirect, settingsTab, settingsTabScope } from './settingsTabs'

describe('settingsTab', () => {
  it('opens the tab the address names', () => {
    expect(settingsTab('?tab=alerts')).toBe('alerts'); expect(settingsTab('?tab=https&node=m-2')).toBe('https')
  })
  it('opens account for none or an unknown one', () => { expect(settingsTab('')).toBe('account'); expect(settingsTab('?tab=nope')).toBe('account') })
  it('reads the old node scope as the system tab, unless a tab is named', () => {
    expect(settingsTab('?scope=node')).toBe('system'); expect(settingsTab('?scope=node&tab=audit')).toBe('audit')
  })
})

describe('settingsTabScope', () => {
  it('keeps the account on every server, the language in the browser, the rest on one server', () => {
    expect(SETTINGS_TABS.map(settingsTabScope)).toEqual(['fleet', 'browser', 'server', 'server', 'server', 'server'])
  })
})

describe('settingsRedirect', () => {
  it('writes the tab the old node scope meant', () => expect(settingsRedirect('?scope=node')).toBe('?tab=system'))
  it('writes the system tab over a tab that is none of the six', () => {
    expect(settingsRedirect('?scope=node&tab=nope')).toBe('?tab=system')
    expect(settingsTab('?scope=node&tab=nope')).toBe('system')
  })
  it('drops the scope and keeps the tab and the server, in order', () => {
    expect(settingsRedirect('?scope=node&tab=audit&node=a-1')).toBe('?tab=audit&node=a-1')
    expect(settingsRedirect('?node=a-1&scope=node')).toBe('?node=a-1&tab=system')
  })
  it('drops any other scope without choosing a tab', () => expect(settingsRedirect('?scope=cluster')).toBe(''))
  it('leaves a current address alone', () => { expect(settingsRedirect('?tab=alerts&node=a-1')).toBeNull(); expect(settingsRedirect('')).toBeNull() })
})
