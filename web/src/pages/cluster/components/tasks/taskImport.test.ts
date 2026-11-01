import { describe, expect, it } from 'vitest'
import type { TaskImportFile, TaskImportPreview, TaskView } from '@/types/api'
import { failedLine, folderOf, importRequest, initialPicks, pickable, pullSources, repoUrlValid, shortSha } from './taskImport'

const COMMIT = 'fedcba9876543210fedcba9876543210fedcba98'

const git = (id: string, repo: string, branch: string, path: string): TaskView => ({
  id, name: id, read_only: true, source: { kind: 'git', repo, branch, path, sha: COMMIT },
})

const file = (path: string, state: TaskImportFile['state']): TaskImportFile => ({
  path, name: path.split('/').pop() ?? path, sha: 'a'.repeat(40), size: 100, state,
})

describe('repoUrlValid', () => {
  it('takes a GitHub repository address as the server reads one', () => {
    expect(repoUrlValid('https://github.com/acme/ops')).toBe(true)
    expect(repoUrlValid('  https://github.com/acme/ops.git ')).toBe(true)
    expect(repoUrlValid('https://github.com/acme/ops/')).toBe(false)
    expect(repoUrlValid('http://github.com/acme/ops')).toBe(false)
    expect(repoUrlValid('https://gitlab.com/acme/ops')).toBe(false)
    expect(repoUrlValid('https://github.com/acme')).toBe(false)
    expect(repoUrlValid('')).toBe(false)
  })
})

describe('folderOf', () => {
  it('is the folder a task file sits in, "" at the root', () => {
    expect(folderOf('tasks/backup.yaml')).toBe('tasks')
    expect(folderOf('ops/tasks/backup.yml')).toBe('ops/tasks')
    expect(folderOf('backup.yaml')).toBe('')
    expect(folderOf(undefined)).toBe('')
  })
})

describe('pullSources', () => {
  it('is each branch and folder the section imported from, once, in list order', () => {
    const tasks = [
      git('a', 'https://github.com/acme/ops', 'main', 'tasks/backup.yaml'),
      git('b', 'https://github.com/acme/ops', 'main', 'tasks/prune.yaml'),
      // The server matches repositories case aside: still the same folder.
      git('c', 'https://github.com/Acme/Ops', 'main', 'tasks/certs.yml'),
      git('d', 'https://github.com/acme/ops', 'staging', 'tasks/backup.yaml'),
      git('e', 'https://github.com/acme/ops', 'main', 'root.yaml'),
    ]
    expect(pullSources(tasks)).toEqual([
      { url: 'https://github.com/acme/ops', branch: 'main', path: 'tasks' },
      { url: 'https://github.com/acme/ops', branch: 'staging', path: 'tasks' },
      { url: 'https://github.com/acme/ops', branch: 'main', path: '' },
    ])
  })

  it('reads an empty branch as main, as the server does', () => {
    expect(pullSources([git('a', 'https://github.com/acme/ops', '', 'x.yaml')])).toEqual([
      { url: 'https://github.com/acme/ops', branch: 'main', path: '' },
    ])
  })
})

describe('picking files', () => {
  const preview: TaskImportPreview = {
    repo: 'https://github.com/acme/ops', branch: 'main', path: 'tasks', commit: COMMIT,
    files: [file('tasks/a.yaml', 'new'), file('tasks/b.yaml', 'unchanged'), file('tasks/c.yaml', 'changed'), file('tasks/d.yaml', 'too_large')],
    removed: [],
  }

  it('only new and changed files can be picked', () => {
    expect(preview.files.map(pickable)).toEqual([true, false, true, false])
  })

  it('starts with the new and changed files picked', () => {
    expect(initialPicks(preview)).toEqual(['tasks/a.yaml', 'tasks/c.yaml'])
  })

  it('imports from what the preview answered, at its commit, in the preview order', () => {
    expect(importRequest(preview, ['tasks/c.yaml', 'tasks/a.yaml'], '')).toEqual({
      url: 'https://github.com/acme/ops', branch: 'main', path: 'tasks', commit: COMMIT, files: ['tasks/a.yaml', 'tasks/c.yaml'],
    })
    // A file the preview cannot pick never goes, whatever is passed.
    expect(importRequest(preview, ['tasks/b.yaml', 'tasks/d.yaml', 'tasks/a.yaml'], 'ghp_x').files).toEqual(['tasks/a.yaml'])
    expect(importRequest(preview, ['tasks/a.yaml'], 'ghp_x').token).toBe('ghp_x')
  })
})

describe('shortSha', () => {
  it('is the first seven characters', () => {
    expect(shortSha(COMMIT)).toBe('fedcba9')
    expect(shortSha(undefined)).toBe('')
  })
})

describe('failedLine', () => {
  it('reads the path and the line a failed file carries', () => {
    expect(failedLine({ code: 'TASK_INVALID', message: 'line 3: x', details: { path: 'tasks/a.yaml', line: '3' } })).toEqual({ path: 'tasks/a.yaml', line: 3 })
    expect(failedLine({ code: 'GIT_PATH_NOT_FOUND', message: 'x', details: { path: 'tasks/b.yaml' } })).toEqual({ path: 'tasks/b.yaml', line: null })
    expect(failedLine({ code: 'TASK_INVALID', message: 'x', details: { path: 'c.yaml', line: 'zero' } })).toEqual({ path: 'c.yaml', line: null })
    expect(failedLine({ code: 'TASK_INVALID', message: 'x' })).toEqual({ path: '', line: null })
  })
})
