import { describe, expect, it } from 'vitest'

// Every module under src, by the path an import names it with.
const paths = Object.keys(import.meta.glob('./**/*.{ts,tsx}'))

// The spellings of one module, or of one folder, on a filesystem that ignores
// case. There an import of './TaskList' can open taskList.ts — the resolver
// tries .ts before .tsx — so two such modules in one folder built on Linux
// and broke the macOS and Windows desktop builds; two such folders are one.
// A module and a folder may differ only in case (Cluster.tsx beside cluster/):
// nothing imports a folder as a module.
function caseOnlyTwins(list: string[]): string[][] {
  const modules = new Map<string, Set<string>>()
  const folders = new Map<string, Set<string>>()
  const add = (to: Map<string, Set<string>>, name: string) => {
    const set = to.get(name.toLowerCase()) ?? new Set<string>()
    set.add(name)
    to.set(name.toLowerCase(), set)
  }
  for (const p of list) {
    const parts = p.replace(/\.tsx?$/, '').split('/')
    add(modules, parts.join('/'))
    for (let i = 2; i < parts.length; i++) add(folders, parts.slice(0, i).join('/'))
  }
  return [...modules.values(), ...folders.values()].filter((s) => s.size > 1).map((s) => [...s].sort())
}

describe('module paths', () => {
  it('differ in more than case', () => {
    expect(caseOnlyTwins(paths)).toEqual([])
  })

  it('that differ only in case are found, two modules or two folders, not a module and a folder', () => {
    expect(caseOnlyTwins(['./a/TaskList.tsx', './a/taskList.ts', './a/Other.ts'])).toEqual([['./a/TaskList', './a/taskList']])
    expect(caseOnlyTwins(['./Logs/x.ts', './logs/y.ts'])).toEqual([['./Logs', './logs']])
    expect(caseOnlyTwins(['./pages/Cluster.tsx', './pages/cluster/Tasks.tsx'])).toEqual([])
  })
})
