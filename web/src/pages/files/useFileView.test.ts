import { describe, it, expect } from 'vitest'
import { sortEntries, isDirLike, isHiddenEntry, type SortState } from './useFileView'
import type { FileEntry } from '@/types/api'

function entry(name: string, opts: Partial<FileEntry> = {}): FileEntry {
  return {
    name,
    path: `/tmp/${name}`,
    isDir: false,
    size: 0,
    modTime: '2026-01-01T00:00:00Z',
    mode: '-rw-r--r--',
    kind: 'text',
    ...opts,
  }
}

const asc = (key: SortState['key']): SortState => ({ key, direction: 'asc' })
const desc = (key: SortState['key']): SortState => ({ key, direction: 'desc' })

describe('sortEntries', () => {
  // Navigation is this page's primary job, so folders stay findable no matter
  // which column is active. Sorting strictly by size would scatter them
  // through the list — they all report the same handful of bytes.
  it('keeps directories first whatever the column', () => {
    const entries = [
      entry('zebra.txt', { size: 900 }),
      entry('alpha', { isDir: true, size: 4096 }),
      entry('beta.txt', { size: 10 }),
      entry('omega', { isDir: true, size: 4096 }),
    ]
    for (const sort of [asc('name'), desc('name'), asc('size'), desc('size'), asc('modTime'), desc('modTime')]) {
      const names = sortEntries(entries, sort).map((e) => e.name)
      const dirCount = names.slice(0, 2).filter((n) => n === 'alpha' || n === 'omega').length
      expect(dirCount, `sort=${sort.key}/${sort.direction} put a file above a folder`).toBe(2)
    }
  })

  it('orders by name in both directions', () => {
    const entries = [entry('c.txt'), entry('a.txt'), entry('b.txt')]
    expect(sortEntries(entries, asc('name')).map((e) => e.name)).toEqual(['a.txt', 'b.txt', 'c.txt'])
    expect(sortEntries(entries, desc('name')).map((e) => e.name)).toEqual(['c.txt', 'b.txt', 'a.txt'])
  })

  it('orders by size', () => {
    const entries = [entry('mid', { size: 500 }), entry('big', { size: 9000 }), entry('small', { size: 1 })]
    expect(sortEntries(entries, desc('size')).map((e) => e.name)).toEqual(['big', 'mid', 'small'])
    expect(sortEntries(entries, asc('size')).map((e) => e.name)).toEqual(['small', 'mid', 'big'])
  })

  // The order that could not be produced before, and the reason to add sorting
  // at all: finding the file you just changed.
  it('orders by modification time', () => {
    const entries = [
      entry('old.txt', { modTime: '2020-01-01T00:00:00Z' }),
      entry('new.txt', { modTime: '2026-08-01T00:00:00Z' }),
      entry('mid.txt', { modTime: '2023-05-01T00:00:00Z' }),
    ]
    expect(sortEntries(entries, desc('modTime')).map((e) => e.name)).toEqual(['new.txt', 'mid.txt', 'old.txt'])
  })

  it('breaks ties by name so the order does not shuffle between renders', () => {
    const same = '2026-01-01T00:00:00Z'
    const entries = [entry('c.txt', { modTime: same }), entry('a.txt', { modTime: same }), entry('b.txt', { modTime: same })]
    const once = sortEntries(entries, desc('modTime')).map((e) => e.name)
    const twice = sortEntries(entries, desc('modTime')).map((e) => e.name)
    expect(once).toEqual(['a.txt', 'b.txt', 'c.txt'])
    expect(once).toEqual(twice)
  })

  // /bin, /lib and /sbin are links to directories on a merged-/usr system:
  // the server says so (targetIsDir), and they belong with the folders.
  it('keeps a link to a directory with the directories', () => {
    const entries = [
      entry('a.txt'),
      entry('bin', { isDir: false, linkTarget: 'usr/bin', targetIsDir: true, size: 7 }),
      entry('etc', { isDir: true }),
      entry('vmlinuz', { isDir: false, linkTarget: 'boot/vmlinuz', targetIsDir: false }),
    ]
    expect(sortEntries(entries, asc('name')).map((e) => e.name)).toEqual(['bin', 'etc', 'a.txt', 'vmlinuz'])
    expect(sortEntries(entries, desc('size')).slice(0, 2).map((e) => e.name)).toEqual(['bin', 'etc'])
  })

  it('does not mutate its input', () => {
    const entries = [entry('b.txt'), entry('a.txt')]
    sortEntries(entries, asc('name'))
    expect(entries.map((e) => e.name)).toEqual(['b.txt', 'a.txt'])
  })
})

describe('isDirLike', () => {
  it('takes a link to a directory for one, and a link to a file for a file', () => {
    expect(isDirLike(entry('etc', { isDir: true }))).toBe(true)
    expect(isDirLike(entry('bin', { linkTarget: 'usr/bin', targetIsDir: true }))).toBe(true)
    expect(isDirLike(entry('vmlinuz', { linkTarget: 'boot/vmlinuz', targetIsDir: false }))).toBe(false)
    // A dangling link, or a server from before targetIsDir, says nothing.
    expect(isDirLike(entry('gone', { linkTarget: 'nowhere' }))).toBe(false)
  })
})

describe('isHiddenEntry', () => {
  it('treats dotfiles as hidden and nothing else', () => {
    expect(isHiddenEntry(entry('.env'))).toBe(true)
    expect(isHiddenEntry(entry('.git', { isDir: true }))).toBe(true)
    expect(isHiddenEntry(entry('docker-compose.yml'))).toBe(false)
    // A dot inside the name is not a dotfile.
    expect(isHiddenEntry(entry('backup.tar.gz'))).toBe(false)
  })
})
