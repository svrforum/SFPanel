import { describe, expect, it } from 'vitest'
import { appAccessUrl } from './appAccess'

describe('appAccessUrl', () => {
  it('opens a plain app over http whatever the panel is served over', () => {
    expect(appAccessUrl({}, '192.0.2.10', '3001')).toBe('http://192.0.2.10:3001')
    expect(appAccessUrl({ scheme: 'http' }, 'panel.lan', '3001')).toBe('http://panel.lan:3001')
  })

  it('opens an app that serves TLS itself over https', () => {
    expect(appAccessUrl({ scheme: 'https' }, '192.0.2.10', '81')).toBe('https://192.0.2.10:81')
  })
})
