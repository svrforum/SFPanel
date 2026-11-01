import { describe, expect, it } from 'vitest'
import type { TFunction } from 'i18next'
import {
  blankStep,
  blankTask,
  copyName,
  documentTooLarge,
  formatDuration,
  hasYamlComments,
  inputNameValid,
  joinNames,
  modelTargets,
  moveItem,
  packagesAction,
  parseDuration,
  splitNames,
  withPackagesAction,
} from './taskForm'

const MIN = 60_000_000_000
const HOUR = 60 * MIN

describe('formatDuration', () => {
  it('writes durations as the server does', () => {
    expect(formatDuration(600_000_000_000)).toBe('10m')
    expect(formatDuration(5_400_000_000_000)).toBe('1h30m')
    expect(formatDuration(2 * HOUR)).toBe('2h')
    expect(formatDuration(90_000_000_000)).toBe('1m30s')
    expect(formatDuration(HOUR + 30_000_000_000)).toBe('1h0m30s')
    expect(formatDuration(1_500_000_000)).toBe('1.5s')
    expect(formatDuration(500_000_000)).toBe('500ms')
    expect(formatDuration(1_500)).toBe('1.5µs')
    expect(formatDuration(7)).toBe('7ns')
    expect(formatDuration(0)).toBe('0s')
  })
})

describe('parseDuration', () => {
  it('reads Go duration syntax', () => {
    expect(parseDuration('90s')).toBe(90_000_000_000)
    expect(parseDuration('10m')).toBe(600_000_000_000)
    expect(parseDuration('1h30m')).toBe(5_400_000_000_000)
    expect(parseDuration('1.5h')).toBe(5_400_000_000_000)
    expect(parseDuration('.5h')).toBe(30 * MIN)
    expect(parseDuration('250ms')).toBe(250_000_000)
    expect(parseDuration('3us')).toBe(3_000)
    expect(parseDuration('3µs')).toBe(3_000)
    expect(parseDuration('12ns')).toBe(12)
    expect(parseDuration(' 2h ')).toBe(2 * HOUR)
    expect(parseDuration('0')).toBe(0)
  })

  it('refuses what Go refuses, and a sign the editor has no use for', () => {
    for (const bad of ['', '10', 'm', '1x', '1h 30m', '.h', '1.2.3s', 'ten minutes', '-5m', '+5m', '1d']) {
      expect(parseDuration(bad), bad).toBeNull()
    }
  })

  it('reads back what formatDuration writes', () => {
    for (const ns of [1, 999, 1_500, 250_000_000, 1_500_000_000, 90_000_000_000, 600_000_000_000, 5_400_000_000_000, 3_661_000_000_000, 49 * HOUR]) {
      expect(parseDuration(formatDuration(ns)), String(ns)).toBe(ns)
    }
  })
})

describe('hasYamlComments', () => {
  it('finds a comment on its own line and after a value', () => {
    expect(hasYamlComments('# deploys the web stack\nname: x\n')).toBe(true)
    expect(hasYamlComments('name: x # the name\n')).toBe(true)
    expect(hasYamlComments('steps:\n  - command: { run: ls }  # list\n')).toBe(true)
    // A plain scalar ends at " #": YAML reads the rest as a comment.
    expect(hasYamlComments('steps:\n  - command:\n      run: echo "a #b"\n')).toBe(true)
  })

  it('does not take a # inside a value for one', () => {
    expect(hasYamlComments('steps:\n  - command:\n      run: echo "#"\n')).toBe(false)
    expect(hasYamlComments('steps:\n  - command:\n      run: echo a#b\n')).toBe(false)
    expect(hasYamlComments("description: 'a # b'\n")).toBe(false)
    expect(hasYamlComments('description: "a # b"\n')).toBe(false)
    expect(hasYamlComments('description: "spans\n  # two lines"\nname: x\n')).toBe(false)
    expect(hasYamlComments("steps:\n  - packages: { install: ['a #b', \"c #d\"] }\n")).toBe(false)
    expect(hasYamlComments('url: http://example.com/#top\n')).toBe(false)
    expect(hasYamlComments('name: x\n')).toBe(false)
  })

  it('reads a block scalar as content, comment lines and all', () => {
    const script = 'steps:\n  - command:\n      script: |\n        # a shell comment\n        echo hi\n\n        # another\n  - reboot: {}\n'
    expect(hasYamlComments(script)).toBe(false)
    // After the block ends, a comment is a comment again.
    expect(hasYamlComments(script + '# trailing\n')).toBe(true)
    // A comment after the block indicator is one.
    expect(hasYamlComments('steps:\n  - command:\n      script: |-  # keep\n        echo hi\n')).toBe(true)
  })
})

describe('inputNameValid', () => {
  it('takes an identifier, not node', () => {
    for (const ok of ['service', '_x', 'Version2', 'a_b']) expect(inputNameValid(ok), ok).toBe(true)
    for (const bad of ['', '2fast', 'a-b', 'a.b', 'a b', 'node', 'ü']) expect(inputNameValid(bad), bad).toBe(false)
  })
})

describe('copyName', () => {
  it('names the copy through the locale', () => {
    const t = ((key: string, opts: { name: string }) => `${key}|${opts.name}`) as unknown as TFunction
    expect(copyName('명령 실행', t)).toBe('cluster.tasks.editor.copyName|명령 실행')
  })
})

describe('blank models', () => {
  // The server's format leaves a default out and reads false as set: a
  // boolean that defaults to true has to be carried, or it saves as false.
  it('a new task stops on failure', () => {
    expect(blankTask()).toEqual({ name: '', targets: {}, batch: 1, stop_on_failure: true, steps: [] })
  })

  it('a new reboot step reboots only when required', () => {
    expect(blankStep('reboot')).toEqual({ kind: 'reboot', reboot: { only_if_required: true, wait: 600_000_000_000 } })
  })

  it('every kind starts with its own settings', () => {
    expect(blankStep('command')).toEqual({ kind: 'command', command: { run: '', timeout: 600_000_000_000 } })
    expect(blankStep('service')).toEqual({ kind: 'service', service: { name: '', action: 'restart' } })
    expect(blankStep('packages')).toEqual({ kind: 'packages', packages: { update: true } })
    expect(blankStep('file')).toEqual({ kind: 'file', file: { path: '', content: '' } })
    expect(blankStep('stack')).toEqual({ kind: 'stack', stack: { name: '', compose: '', up: true } })
  })
})

describe('package names', () => {
  it('splits on spaces and commas, keeping a reference whole', () => {
    expect(splitNames('htop, curl  jq\nvim')).toEqual(['htop', 'curl', 'jq', 'vim'])
    expect(splitNames('{{ packages }} htop')).toEqual(['{{ packages }}', 'htop'])
    expect(splitNames('  ')).toEqual([])
    expect(joinNames(['{{ packages }}', 'htop'])).toBe('{{ packages }} htop')
  })

  it('keeps a name that holds a reference whole: the node judges it once expanded', () => {
    expect(splitNames('nginx-{{ suffix }} curl')).toEqual(['nginx-{{ suffix }}', 'curl'])
    expect(splitNames('{{ packages }}')).toEqual(['{{ packages }}'])
    expect(splitNames('{{ a }}-{{ b }},htop')).toEqual(['{{ a }}-{{ b }}', 'htop'])
    expect(splitNames('a,b c\nd')).toEqual(['a', 'b', 'c', 'd'])
  })

  it('moves the names to the action chosen', () => {
    expect(packagesAction({ update: true })).toBe('none')
    expect(packagesAction({ upgrade_all: true })).toBe('upgradeAll')
    expect(packagesAction({ install: ['a'] })).toBe('install')
    expect(withPackagesAction({ update: true, install: ['a'] }, 'remove')).toEqual({ update: true, remove: ['a'] })
    expect(withPackagesAction({ update: true, remove: ['a'] }, 'upgradeAll')).toEqual({ update: true, upgrade_all: true })
    expect(withPackagesAction({ upgrade_all: true }, 'install')).toEqual({ install: [] })
    expect(withPackagesAction({ update: true, upgrade: ['a'] }, 'none')).toEqual({ update: true })
  })
})

describe('targets and order', () => {
  it('leaves out an empty list', () => {
    expect(modelTargets({ groups: ['web'], kinds: [], nodes: [] })).toEqual({ groups: ['web'] })
    expect(modelTargets({ groups: [], kinds: [], nodes: [] })).toEqual({})
  })

  it('moves an item without touching the list', () => {
    const list = ['a', 'b', 'c']
    expect(moveItem(list, 0, 1)).toEqual(['b', 'a', 'c'])
    expect(moveItem(list, 2, -1)).toEqual(['a', 'c', 'b'])
    expect(moveItem(list, 0, -1)).toEqual(['a', 'b', 'c'])
    expect(list).toEqual(['a', 'b', 'c'])
  })

  it('measures the document in bytes', () => {
    expect(documentTooLarge('a'.repeat(65_536))).toBe(false)
    expect(documentTooLarge('a'.repeat(65_537))).toBe(true)
    // 21,846 three-byte characters are 65,538 bytes.
    expect(documentTooLarge('한'.repeat(21_846))).toBe(true)
  })
})
