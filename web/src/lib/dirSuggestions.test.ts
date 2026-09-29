import { describe, expect, it } from 'vitest'
import { dirName, filterDirs, mergeDirs } from './dirSuggestions'

const dirs = {
  recent: ['/opt/stacks/SFPanel', '/opt/stacks/blog'],
  stacks: ['/opt/stacks/blog', '/opt/stacks/immich', '/opt/stacks/nginxproxyguard', '/opt/stacks/nginxproxyguard_web'],
  home: '/root',
}

describe('mergeDirs', () => {
  it('lists a directory once even when it is both recent and a stack', () => {
    const merged = mergeDirs(dirs)
    expect(merged.map((s) => s.path)).toEqual([
      '/opt/stacks/SFPanel', '/opt/stacks/blog', '/opt/stacks/immich',
      '/opt/stacks/nginxproxyguard', '/opt/stacks/nginxproxyguard_web', '/root',
    ])
    expect(merged.find((s) => s.path === '/opt/stacks/blog')?.sources).toEqual(['recent', 'stack'])
  })
  it('keeps nothing when the server sent nothing', () => {
    expect(mergeDirs(null)).toEqual([])
    expect(mergeDirs({ recent: [], stacks: [], home: '' })).toEqual([])
  })
})

describe('filterDirs', () => {
  const merged = mergeDirs(dirs)
  it('keeps everything for an empty query', () => {
    expect(filterDirs(merged, '  ')).toHaveLength(merged.length)
  })
  it('matches inside a directory name', () => {
    expect(filterDirs(merged, 'proxy').map(dirNameOf)).toEqual(['nginxproxyguard', 'nginxproxyguard_web'])
  })
  it('ranks a name that starts with the query above one that only contains it', () => {
    // The merged order puts my-web first; the prefix match still leads.
    const list = mergeDirs({ recent: ['/a/my-web', '/a/webapp'], stacks: [], home: '' })
    expect(filterDirs(list, 'web').map(dirNameOf)).toEqual(['webapp', 'my-web'])
  })
  it('ranks a name match above a match somewhere else in the path', () => {
    // Every stack path contains "stacks"; the directory actually named that leads.
    const list = mergeDirs({ recent: ['/srv/stacks-old/app'], stacks: ['/opt/stacks'], home: '' })
    expect(filterDirs(list, 'stacks').map((s) => s.path)).toEqual(['/opt/stacks', '/srv/stacks-old/app'])
  })
  it('falls back to the path, in merged order, when no name matches', () => {
    expect(filterDirs(merged, 'opt/').map((s) => s.path)).toEqual(merged.filter((s) => s.path.startsWith('/opt/')).map((s) => s.path))
    expect(filterDirs(merged, 'st').map((s) => s.path)).toEqual(merged.filter((s) => s.path.includes('st')).map((s) => s.path))
  })
  it('matches a typed path as a path, prefix first', () => {
    expect(filterDirs(merged, '/opt/stacks/n').map(dirNameOf)).toEqual(['nginxproxyguard', 'nginxproxyguard_web'])
    expect(filterDirs(merged, '/ro').map((s) => s.path)).toEqual(['/root'])
  })
  it('is case-insensitive', () => {
    expect(filterDirs(merged, 'sfp').map((s) => s.path)).toEqual(['/opt/stacks/SFPanel'])
  })
})

describe('dirName', () => {
  it('is the last segment, and "/" for the root', () => {
    expect(dirName('/opt/stacks/blog')).toBe('blog')
    expect(dirName('/opt/stacks/blog/')).toBe('blog')
    expect(dirName('/')).toBe('/')
  })
})

function dirNameOf(s: { path: string }) { return dirName(s.path) }
