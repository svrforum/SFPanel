import { describe, expect, it } from 'vitest'
import type { RunRecord, RunStatus, TaskView } from '@/types/api'
import { activeRuns, repoLabel, taskSections, type ListedTask } from './taskList'

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
