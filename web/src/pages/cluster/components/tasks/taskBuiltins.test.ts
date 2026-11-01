import { describe, expect, it } from 'vitest'
import type { TFunction } from 'i18next'
import en from '@/i18n/locales/en.json'
import ko from '@/i18n/locales/ko.json'
import { builtinKey, builtinText } from './taskBuiltins'

// t that answers with the key it was asked for, so a test sees which key.
const keyT = ((key: string) => key) as unknown as TFunction

function leaf(tree: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>(
    (node, part) => (node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined),
    tree,
  )
}
const lookup = (tree: unknown) => ((key: string) => {
  const v = leaf(tree, key)
  return typeof v === 'string' ? v : key
}) as unknown as TFunction

// The fields each built-in has (internal/tasks/builtin/*.yaml).
const FIELDS: Record<string, string[]> = {
  'builtin/package-update': ['name', 'description', 'steps.0'],
  'builtin/package-install': ['name', 'description', 'inputs.packages', 'steps.0'],
  'builtin/service-restart': ['name', 'description', 'inputs.service', 'steps.0'],
  'builtin/command': ['name', 'description', 'inputs.command', 'steps.0'],
  'builtin/reboot': ['name', 'description', 'steps.0'],
}

describe('builtinKey', () => {
  it('names each of the five built-ins', () => {
    expect(builtinKey('builtin/package-update')).toBe('packageUpdate')
    expect(builtinKey('builtin/package-install')).toBe('packageInstall')
    expect(builtinKey('builtin/service-restart')).toBe('serviceRestart')
    expect(builtinKey('builtin/command')).toBe('command')
    expect(builtinKey('builtin/reboot')).toBe('reboot')
  })

  it('is null for anything else', () => {
    // A stored task's ID, a built-in this screen does not know yet (an older
    // or newer manager), an ID escaped once more, and look-alikes.
    for (const id of ['0a1b2c3d4e5f6071', 'builtin/agent-install', 'builtin%2Fcommand', 'command', 'builtin/command/x', 'Builtin/command', '']) {
      expect(builtinKey(id), id).toBeNull()
    }
  })
})

describe('builtinText', () => {
  it('asks for the built-in key of the field', () => {
    expect(builtinText(keyT, 'builtin/command', 'name', '명령 실행')).toBe('cluster.tasks.builtins.command.name')
    expect(builtinText(keyT, 'builtin/service-restart', 'inputs.service', 'x')).toBe('cluster.tasks.builtins.serviceRestart.inputs.service')
    expect(builtinText(keyT, 'builtin/reboot', 'steps.0', 'x')).toBe('cluster.tasks.builtins.reboot.steps.0')
  })

  it('keeps the fallback for a stored task, an unknown built-in or a field the built-in lacks', () => {
    expect(builtinText(keyT, '0a1b2c3d4e5f6071', 'name', 'Deploy web')).toBe('Deploy web')
    expect(builtinText(keyT, 'builtin/agent-install', 'name', '에이전트 설치')).toBe('에이전트 설치')
    expect(builtinText(keyT, 'builtin/package-update', 'inputs.packages', 'packages')).toBe('packages')
    expect(builtinText(keyT, 'builtin/command', 'steps.1', '두 번째')).toBe('두 번째')
  })

  // Every field each built-in has is worded in both languages, and the
  // English screen gets no Korean.
  it.each([['en', en], ['ko', ko]] as const)('words every field of every built-in in %s', (lang, tree) => {
    const t = lookup(tree)
    for (const [id, fields] of Object.entries(FIELDS)) {
      for (const field of fields) {
        const text = builtinText(t, id, field, '\u0000fallback')
        expect(text, `${id} ${field}`).not.toBe('\u0000fallback')
        expect(text, `${id} ${field}`).not.toMatch(/^cluster\.tasks\./)
        if (lang === 'en') expect(text, `${id} ${field}`).not.toMatch(/[가-힣]/)
      }
    }
  })

  it('says in the service input which name it takes', () => {
    expect(builtinText(lookup(ko), 'builtin/service-restart', 'inputs.service', '')).toBe('재시작할 서비스 (예: nginx.service)')
    expect(builtinText(lookup(en), 'builtin/service-restart', 'inputs.service', '')).toContain('nginx.service')
  })
})
