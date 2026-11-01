import { describe, expect, it } from 'vitest'
import type { RunRecord, RunStatus, TaskView } from '@/types/api'
import { READ_AGAIN_MS, activeRuns, listLacks, listShows, readUntil, repoLabel, taskSections, type ListedTask } from './taskList'

const listed = (id: string, kind: TaskView['source']['kind'], name: string, description = '', repo?: string): ListedTask => ({
  task: { id, name, source: { kind, repo }, read_only: kind !== 'user' },
  name,
  description,
})

const TASKS = [
  listed('builtin/package-update', 'builtin', 'Package update', 'Upgrades every installed package'),
  listed('builtin/command', 'builtin', 'Run a command', 'Runs the command you type as root'),
  listed('u1', 'user', 'Deploy web', 'Pulls the web stack'),
  listed('g1', 'git', 'Nightly backup', 'Copies /srv', 'https://github.com/acme/ops'),
  listed('g2', 'git', 'Rotate certs', '', 'https://github.com/acme/infra'),
  // The same repository, typed in another case: one section.
  listed('g3', 'git', 'Prune images', '', 'https://github.com/Acme/Ops'),
]

const names = (xs: ListedTask[]) => xs.map((x) => x.name)

describe('taskSections', () => {
  it('splits built-in, own and Git tasks, Git by repository', () => {
    const s = taskSections(TASKS, '')
    expect(names(s.builtin)).toEqual(['Package update', 'Run a command'])
    expect(names(s.mine)).toEqual(['Deploy web'])
    expect(s.git.map((r) => [r.label, names(r.tasks)])).toEqual([
      ['acme/ops', ['Nightly backup', 'Prune images']],
      ['acme/infra', ['Rotate certs']],
    ])
  })

  it('matches the name or the description, case aside', () => {
    expect(names(taskSections(TASKS, '  PACKAGE ').builtin)).toEqual(['Package update'])
    const byDescription = taskSections(TASKS, 'as root')
    expect(names(byDescription.builtin)).toEqual(['Run a command'])
    expect(byDescription.mine).toEqual([])
    expect(byDescription.git).toEqual([])
    expect(taskSections(TASKS, 'backup').git.map((r) => r.label)).toEqual(['acme/ops'])
  })
})

describe('repoLabel', () => {
  it('reads owner/repo off a GitHub URL', () => {
    expect(repoLabel('https://github.com/acme/ops')).toBe('acme/ops')
    expect(repoLabel('https://github.com/acme/ops/')).toBe('acme/ops')
    expect(repoLabel('https://github.com/acme/ops.git')).toBe('acme/ops')
    expect(repoLabel(undefined)).toBe('')
  })
})

describe('activeRuns', () => {
  it('keeps the runs not yet ended', () => {
    const run = (id: string, status: RunStatus) => ({ id, status }) as RunRecord
    const all: RunStatus[] = ['running', 'stopping', 'handing_over', 'succeeded', 'failed', 'stopped']
    expect(activeRuns(all.map((s) => run(s, s))).map((r) => r.id)).toEqual(['running', 'stopping', 'handing_over'])
  })
})

// A write goes on to the leader; the list is read from the manager this
// browser talks to, which as a follower applies the commit a second or two
// later. The page reads again until the list shows what was written.
describe('readUntil', () => {
  function reads<T>(answers: T[]) {
    const log: string[] = []
    let n = 0
    return {
      log,
      read: async () => {
        const v = answers[Math.min(n++, answers.length - 1)]
        log.push(`read ${String(v)}`)
        return v
      },
      sleep: async (ms: number) => {
        log.push(`wait ${ms}`)
      },
    }
  }

  it('waits for nothing when the first read holds', async () => {
    const r = reads(['new'])
    expect(await readUntil(r.read, (v) => v === 'new', READ_AGAIN_MS, r.sleep)).toBe('new')
    expect(r.log).toEqual(['read new'])
  })

  it('reads again after each delay until the value holds', async () => {
    const r = reads(['old', 'old', 'new', 'old'])
    expect(await readUntil(r.read, (v) => v === 'new', [500, 1000, 2000, 2000], r.sleep)).toBe('new')
    expect(r.log).toEqual(['read old', 'wait 500', 'read old', 'wait 1000', 'read new'])
  })

  it('answers the last read once every delay has been waited, once each', async () => {
    const r = reads(['a', 'b', 'c', 'd', 'e', 'f'])
    expect(await readUntil(r.read, () => false, [500, 1000, 2000, 2000], r.sleep)).toBe('e')
    expect(r.log).toEqual(['read a', 'wait 500', 'read b', 'wait 1000', 'read c', 'wait 2000', 'read d', 'wait 2000', 'read e'])
  })

  // A follower learns of a commit 1 to 2 s after it (measured on a test
  // cluster: 1.1 to 2.0 s), so the list is read every half second until
  // 2.5 s: the write shows within half a second of the follower having it.
  it('reads every half second through the commit\'s 1 to 2 s, then at 3.5 and 5.5 s', () => {
    expect(READ_AGAIN_MS).toEqual([500, 500, 500, 500, 500, 1000, 2000])
  })
})

describe('what a write waits for', () => {
  const task = (id: string, updated_at?: string): TaskView => ({ id, name: id, source: { kind: 'user' }, read_only: false, updated_at })
  const before = [task('a', '2026-01-01T00:00:00Z'), task('b', '2026-01-01T00:00:00Z')]

  it('a save or an import: every task written, with the time it was written', () => {
    const written = [task('b', '2026-01-02T00:00:00Z'), task('c', '2026-01-02T00:00:00Z')]
    expect(listShows(written)(before)).toBe(false)
    expect(listShows(written)([...before, written[1]])).toBe(false)
    expect(listShows(written)([before[0], ...written])).toBe(true)
    expect(listShows([])(before)).toBe(true)
  })

  it('a delete: the task gone', () => {
    expect(listLacks('b')(before)).toBe(false)
    expect(listLacks('b')([before[0]])).toBe(true)
  })
})
