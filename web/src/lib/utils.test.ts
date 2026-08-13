import { describe, expect, it } from 'vitest'
import {
  formatBytes,
  formatDate,
  formatUptime,
  getUsageColor,
  nodeStatusBadge,
  nodeStatusColor,
  pathBasename,
  pathJoin,
  pathParent,
} from './utils'

// cn / copyText / downloadBlob are excluded: they need a DOM, and these tests
// run in the node environment on purpose (no jsdom dependency).

describe('formatBytes', () => {
  const cases: [name: string, input: number, want: string][] = [
    ['zero', 0, '0 B'],
    ['bytes stay whole', 512, '512 B'],
    ['exactly one KB', 1024, '1.0 KB'],
    ['one and a half KB', 1536, '1.5 KB'],
    ['megabytes', 5 * 1024 * 1024, '5.0 MB'],
    ['gigabytes', 3.5 * 1024 * 1024 * 1024, '3.5 GB'],
    ['terabytes', 2 * 1024 ** 4, '2.0 TB'],
    ['petabytes', 1024 ** 5, '1.0 PB'],
    // NaN hits the falsy guard rather than producing "NaN B".
    ['NaN', NaN, '0 B'],
    // Negative sizes never enter the divide loop, so they render as raw bytes.
    ['negative', -5, '-5 B'],
    // PB is the largest unit; beyond it the number just keeps growing.
    ['beyond petabytes', 1024 ** 6, '1024.0 PB'],
    // Below the 1024 cutoff the loop never runs, so toFixed(0) rounds up
    // across the unit boundary and prints "1024 B" instead of "1.0 KB".
    ['rounds up past the unit boundary', 1023.5, '1024 B'],
  ]

  it.each(cases)('%s', (_name, input, want) => {
    expect(formatBytes(input)).toBe(want)
  })
})

describe('formatDate', () => {
  // Output goes through toLocaleString(), so these assertions compare against
  // other locale-formatted values rather than hard-coding a locale/timezone.

  it('treats a number as unix seconds and a string as a parseable date', () => {
    expect(formatDate(1700000000)).toBe(formatDate('2023-11-14T22:13:20.000Z'))
  })

  it('returns a dash for unparseable input', () => {
    expect(formatDate('')).toBe('-')
    expect(formatDate('not a date')).toBe('-')
    expect(formatDate(NaN)).toBe('-')
  })

  // A zero timestamp is a common "never" sentinel from the API, but it is a
  // valid Date, so it renders as the 1970 epoch rather than the dash.
  it('renders a zero timestamp as the epoch, not a dash', () => {
    expect(formatDate(0)).toBe(new Date(0).toLocaleString())
  })
})

describe('formatUptime', () => {
  const cases: [name: string, input: number, want: string][] = [
    ['zero', 0, '0m'],
    ['under a minute rounds down', 59, '0m'],
    ['whole minutes', 300, '5m'],
    ['drops the day part below 24h', 3600, '1h 0m'],
    ['hours and minutes', 3600 + 120, '1h 2m'],
    ['days, hours and minutes', 90061, '1d 1h 1m'],
    ['exactly one day', 86400, '1d 0h 0m'],
  ]

  it.each(cases)('%s', (_name, input, want) => {
    expect(formatUptime(input)).toBe(want)
  })
})

describe('getUsageColor', () => {
  const cases: [name: string, percent: number, variant: 'cpu' | 'mem' | 'swap' | undefined, want: string][] = [
    ['above 80 is red', 80.1, undefined, '#f04452'],
    // Both thresholds are strict >, so a value sitting exactly on one stays in
    // the band below it.
    ['exactly 80 is still amber', 80, undefined, '#f59e0b'],
    ['above 50 is amber', 50.1, undefined, '#f59e0b'],
    ['exactly 50 is not yet amber', 50, undefined, '#3182f6'],
    ['low cpu is blue', 10, 'cpu', '#3182f6'],
    ['low mem is green', 10, 'mem', '#00c471'],
    ['low swap is blue', 10, 'swap', '#3182f6'],
    // The threshold colors win over the variant color.
    ['high mem is red, not green', 90, 'mem', '#f04452'],
  ]

  it.each(cases)('%s', (_name, percent, variant, want) => {
    expect(getUsageColor(percent, variant)).toBe(want)
  })
})

describe('nodeStatusColor', () => {
  const cases: [status: string, want: string][] = [
    ['online', 'bg-success'],
    ['suspect', 'bg-warning'],
    ['offline', 'bg-destructive'],
    ['', 'bg-muted-foreground'],
    ['unknown', 'bg-muted-foreground'],
  ]

  it.each(cases)('%j -> %s', (status, want) => {
    expect(nodeStatusColor(status)).toBe(want)
  })
})

describe('nodeStatusBadge', () => {
  // The tinted sibling of nodeStatusColor. Both must cover the same status
  // set, and the classes have to stay literal for Tailwind's scanner.
  const cases: [status: string, want: string][] = [
    ['online', 'bg-success/10 text-success'],
    ['suspect', 'bg-warning/10 text-warning'],
    ['offline', 'bg-destructive/10 text-destructive'],
    ['', 'bg-muted text-muted-foreground'],
    ['unknown', 'bg-muted text-muted-foreground'],
  ]

  it.each(cases)('%j -> %s', (status, want) => {
    expect(nodeStatusBadge(status)).toBe(want)
  })

  it('handles exactly the statuses nodeStatusColor does', () => {
    for (const status of ['online', 'suspect', 'offline', 'joining', '']) {
      const dotIsDefault = nodeStatusColor(status) === 'bg-muted-foreground'
      const badgeIsDefault = nodeStatusBadge(status) === 'bg-muted text-muted-foreground'
      expect(badgeIsDefault).toBe(dotIsDefault)
    }
  })
})

describe('path helpers', () => {
  const basenameCases: [input: string, want: string][] = [
    ['/', '/'],
    ['/etc', 'etc'],
    ['/var/log/syslog', 'syslog'],
    ['/var/log/', 'log'],
    ['/var/log///', 'log'],
    ['relative/path', 'path'],
    ['file.txt', 'file.txt'],
    // Empty and slash-only inputs collapse to the root marker.
    ['', '/'],
    ['//', '/'],
  ]

  it.each(basenameCases)('pathBasename(%j) -> %j', (input, want) => {
    expect(pathBasename(input)).toBe(want)
  })

  const parentCases: [input: string, want: string][] = [
    ['/', '/'],
    ['/etc', '/'],
    ['/var/log/syslog', '/var/log'],
    ['/var/log/', '/var'],
    ['relative/path', 'relative'],
    // No separator left to split on, so everything above the root is the root.
    ['file.txt', '/'],
    ['', '/'],
  ]

  it.each(parentCases)('pathParent(%j) -> %j', (input, want) => {
    expect(pathParent(input)).toBe(want)
  })

  const joinCases: [name: string, parts: string[], want: string][] = [
    ['absolute base', ['/var/log', 'syslog'], '/var/log/syslog'],
    ['root base', ['/', 'etc'], '/etc'],
    ['trailing slash on the base is dropped', ['/var/log/', 'syslog'], '/var/log/syslog'],
    ['leading slash on a segment is dropped', ['/var', '/log'], '/var/log'],
    ['more than two segments', ['/var', 'log', 'nginx', 'access.log'], '/var/log/nginx/access.log'],
    ['duplicate separators collapse', ['/var//log', 'syslog'], '/var/log/syslog'],
    ['single empty part becomes root', [''], '/'],
    // A trailing slash on the last segment survives, so an empty final segment
    // leaves the result with one.
    ['empty trailing segment keeps a separator', ['/var/log', ''], '/var/log/'],
  ]

  it.each(joinCases)('pathJoin: %s', (_name, parts, want) => {
    expect(pathJoin(...parts)).toBe(want)
  })
})
