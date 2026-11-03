import { describe, expect, it } from 'vitest'
import type { TFunction } from 'i18next'
import en from '@/i18n/locales/en.json'
import ko from '@/i18n/locales/ko.json'
import type { AgentInstallLogin } from '@/types/api'
import { emptyInstallForm, type InstallAccountFields } from './installForm'
import {
  TYPED,
  defaultLogin,
  loginBody,
  loginChangeProblems,
  loginErrorText,
  loginLabel,
  loginNameProblem,
  loginNameRefusal,
  loginNameText,
  loginSummary,
  sortLogins,
  withLogin,
} from './installLogins'

function leaf(tree: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>(
    (node, part) => (node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined),
    tree,
  )
}
// A t that fills placeholders from the tree it reads, as i18next does.
const lookup = (tree: unknown) => ((key: string, params?: Record<string, unknown>) => {
  const v = leaf(tree, key)
  if (typeof v !== 'string') return key
  return v.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (params && name in params ? String(params[name]) : m))
}) as unknown as TFunction

const login = (id: string, name: string, over: Partial<AgentInstallLogin> = {}): AgentInstallLogin => ({
  id, name, user: 'ubuntu', method: 'password',
  has_password: true, has_key: false, has_passphrase: false, sudo_same: true, has_sudo_password: false,
  created_at: '2026-11-03T00:00:00Z', updated_at: '2026-11-03T00:00:00Z', updated_by: 'admin',
  ...over,
})

const account = (over: Partial<InstallAccountFields> = {}): InstallAccountFields => {
  const { user, method, password, key, passphrase, sudoSame, sudoPassword } = emptyInstallForm()
  return { user, method, password, key, passphrase, sudoSame, sudoPassword, ...over }
}

describe('defaultLogin', () => {
  const list = [login('l-1', 'alpha'), login('l-2', 'beta')]

  it('is the login last used, while the list still has it', () => {
    expect(defaultLogin(list, 'l-2')).toBe('l-2')
  })

  it('is the first listed when the one last used is gone, or none was', () => {
    expect(defaultLogin(list, 'l-9')).toBe('l-1')
    expect(defaultLogin(list, null)).toBe('l-1')
    expect(defaultLogin(list, '')).toBe('l-1')
  })

  it('is typing when nothing is saved', () => {
    expect(defaultLogin([], 'l-2')).toBe(TYPED)
    expect(defaultLogin([], null)).toBe(TYPED)
  })
})

describe('sortLogins and withLogin', () => {
  it('order the list as the leader does: by name without case, then ID', () => {
    const sorted = sortLogins([login('b', 'Gamma'), login('c', 'alpha'), login('a', 'Beta'), login('a0', 'beta')])
    expect(sorted.map((l) => l.id)).toEqual(['c', 'a', 'a0', 'b'])
  })

  it('put a saved login in place of the one with its ID, or in its place by name', () => {
    const list = [login('l-1', 'alpha'), login('l-2', 'beta')]
    expect(withLogin(list, login('l-1', 'zeta')).map((l) => [l.id, l.name])).toEqual([['l-2', 'beta'], ['l-1', 'zeta']])
    expect(withLogin(list, login('l-3', 'Ab')).map((l) => l.id)).toEqual(['l-3', 'l-1', 'l-2'])
  })
})

describe('loginNameProblem', () => {
  const list = [login('l-1', 'ops'), login('l-2', '기본 계정')]

  it('takes 1 to 64 characters once trimmed, counted as characters, not as UTF-16 units', () => {
    expect(loginNameProblem('', list)).toBe('empty')
    expect(loginNameProblem(' \t 　', list)).toBe('empty')
    expect(loginNameProblem('a'.repeat(64), list)).toBeNull()
    expect(loginNameProblem(`  ${'a'.repeat(64)}  `, list)).toBeNull()
    expect(loginNameProblem('a'.repeat(65), list)).toBe('long')
    expect(loginNameProblem('계'.repeat(64), list)).toBeNull()
    expect(loginNameProblem('🔑'.repeat(64), list)).toBeNull()
    expect(loginNameProblem('🔑'.repeat(65), list)).toBe('long')
  })

  it("trims as the leader's strings.TrimSpace does, not as String.trim", () => {
    // U+0085 is space to Go, trimmed before the control check; U+FEFF is not.
    expect(loginNameProblem('\u0085ops-2\u0085', list)).toBeNull()
    expect(loginNameProblem('﻿', list)).toBeNull()
    expect(loginBody('\u0085 ops-2 ﻿', account()).name).toBe('ops-2 ﻿')
  })

  it('refuses a control character, as the leader reads one', () => {
    expect(loginNameProblem('web\tlogin', list)).toBe('control')
    expect(loginNameProblem('web\u007flogin', list)).toBe('control')
    expect(loginNameProblem('web\u0085login', list)).toBe('control')
    // Not a control character to the leader: a format character, a symbol.
    expect(loginNameProblem('web‍login·©', list)).toBeNull()
  })

  it('refuses a name another login has, without regard to case; the login changed keeps its own', () => {
    expect(loginNameProblem('OPS', list)).toBe('taken')
    expect(loginNameProblem(' 기본 계정 ', list)).toBe('taken')
    expect(loginNameProblem('Ops', list, 'l-1')).toBeNull()
    expect(loginNameProblem('ops-2', list)).toBeNull()
  })

  it('words each problem in both languages', () => {
    for (const p of ['empty', 'long', 'control', 'taken'] as const) {
      expect(loginNameText(p, lookup(en)), p).not.toMatch(/cluster\.tasks|\{\{|[가-힣]/)
      expect(loginNameText(p, lookup(ko)), p).not.toMatch(/cluster\.tasks|\{\{/)
    }
    expect(loginNameText('taken', lookup(en))).toBe('Another saved login already has this name. Names that differ only in case count as the same.')
  })
})

describe('loginLabel and loginSummary', () => {
  it('lists a login as name (user · method)', () => {
    expect(loginLabel(login('l-1', '기본 계정'), lookup(en))).toBe('기본 계정 (ubuntu · Password)')
    expect(loginLabel(login('l-2', 'ops', { user: 'ops', method: 'key' }), lookup(ko))).toBe('ops (ops · 개인 키)')
  })

  it('says the user, the method and the sudo password of a login chosen', () => {
    const t = lookup(en)
    expect(loginSummary(login('l-1', 'a'), t)).toBe('ubuntu · Password · sudo takes the login password')
    expect(loginSummary(login('l-1', 'a', { sudo_same: false, has_sudo_password: true }), t)).toBe('ubuntu · Password · sudo password saved')
    expect(loginSummary(login('l-1', 'a', { method: 'key', sudo_same: false, has_key: true, has_password: false }), t)).toBe(
      'ubuntu · Private key · no sudo password',
    )
    expect(loginSummary(login('l-1', 'a', { method: 'tailscale', user: 'root', sudo_same: false, has_sudo_password: true }), t)).toBe(
      'root · Tailscale SSH · root needs no sudo',
    )
    expect(loginSummary(login('l-1', 'a'), lookup(ko))).toBe('ubuntu · 비밀번호 · sudo에 접속 비밀번호 사용')
  })

  it('shows a method this screen does not know as sent', () => {
    expect(loginLabel(login('l-1', 'a', { method: 'kerberos' as AgentInstallLogin['method'] }), lookup(en))).toBe('a (ubuntu · kerberos)')
  })
})

describe('loginBody', () => {
  it("is the name, trimmed, and the account as the start sends it: the chosen method's secrets alone", () => {
    expect(loginBody(' 기본 계정 ', account({ user: ' ubuntu ', password: 'pw', key: 'KEY', sudoSame: true }))).toEqual({
      name: '기본 계정', user: 'ubuntu', method: 'password', password: 'pw', sudo_same: true,
    })
    expect(loginBody('ops', account({ user: 'ops', method: 'key', password: 'pw', key: 'KEY', passphrase: 'pp', sudoPassword: 'sp' }))).toEqual({
      name: 'ops', user: 'ops', method: 'key', key: 'KEY', passphrase: 'pp', sudo_password: 'sp',
    })
    expect(loginBody('ts', account({ user: 'root', method: 'tailscale', sudoSame: false, sudoPassword: 'sp' }))).toEqual({
      name: 'ts', user: 'root', method: 'tailscale',
    })
  })

  it('leaves out a secret left empty, which a change keeps from the stored login, and a key of blanks', () => {
    const body = loginBody('ops', account({ user: 'ops', method: 'password', password: '', sudoSame: false, sudoPassword: '' }))
    expect(body).toEqual({ name: 'ops', user: 'ops', method: 'password' })
    expect(Object.keys(body)).not.toContain('password')
    expect(loginBody('ops', account({ user: 'ops', method: 'key', key: ' \n', passphrase: '' }))).toEqual({ name: 'ops', user: 'ops', method: 'key' })
    // A password goes as typed, blanks and all.
    expect(loginBody('ops', account({ user: 'ops', password: ' p w ', sudoSame: false }))).toMatchObject({ password: ' p w ' })
  })
})

describe('loginChangeProblems', () => {
  const list = [login('l-1', 'ops'), login('l-2', 'web', { method: 'key', has_password: false, has_key: true, has_passphrase: true, sudo_same: false })]
  const change = (l: AgentInstallLogin, over: Partial<InstallAccountFields & { name: string }> = {}) => ({
    ...account({ user: l.user, method: l.method, sudoSame: l.sudo_same }), name: l.name, ...over,
  })

  it('takes a secret left empty where the stored login has one', () => {
    expect(loginChangeProblems(change(list[0]), list[0], list)).toEqual({ name: null, fields: [] })
    expect(loginChangeProblems(change(list[1]), list[1], list)).toEqual({ name: null, fields: [] })
  })

  it('asks for the secret a method needs when none is stored for it', () => {
    expect(loginChangeProblems(change(list[0], { method: 'key' }), list[0], list).fields).toEqual(['key'])
    expect(loginChangeProblems(change(list[1], { method: 'password' }), list[1], list).fields).toEqual(['password'])
    expect(loginChangeProblems(change(list[1], { method: 'tailscale' }), list[1], list).fields).toEqual([])
    expect(loginChangeProblems(change(list[0], { user: ' ' }), list[0], list).fields).toEqual(['user'])
  })

  it("checks the name as a new one is, the login's own name aside", () => {
    expect(loginChangeProblems(change(list[0], { name: 'OPS' }), list[0], list).name).toBeNull()
    expect(loginChangeProblems(change(list[0], { name: 'Web' }), list[0], list).name).toBe('taken')
    expect(loginChangeProblems(change(list[0], { name: '' }), list[0], list).name).toBe('empty')
  })
})

describe('refusals of a saved login', () => {
  const refusal = (status: number, code?: string, details?: Record<string, string>, message = 'Worded by its code.') =>
    Object.assign(new Error(message), { status, code, details })

  it('reads a 413 with no code as the login being too large, not as the 1 MiB of other requests', () => {
    expect(loginErrorText(refusal(413, undefined, undefined, 'HTTP 413'), lookup(en))).toBe(
      'The login is too large to save: a saved login takes at most 64 KiB.',
    )
    expect(loginErrorText(refusal(503, 'INTERNAL_ERROR'), lookup(en))).toBe('The cluster leader is changing or catching up. Try again in a few seconds.')
    expect(loginErrorText(refusal(409, 'AGENT_INSTALL_LOGIN_LIMIT', { max: '32' }), lookup(en))).toBe('Worded by its code.')
  })

  it('puts a name taken, or one the leader does not take, on the name', () => {
    expect(loginNameRefusal(refusal(409, 'AGENT_INSTALL_LOGIN_EXISTS', { field: 'name' }), lookup(en))).toBe(
      'Another saved login already has this name. Names that differ only in case count as the same.',
    )
    expect(loginNameRefusal(refusal(400, 'AGENT_INSTALL_INVALID', { field: 'name', max: '64' }), lookup(ko))).toBe(
      '로그인 이름은 1~64자이고 제어 문자를 넣을 수 없습니다.',
    )
    expect(loginNameRefusal(refusal(400, 'AGENT_INSTALL_INVALID', { field: 'user' }), lookup(en))).toBeNull()
    expect(loginNameRefusal(refusal(409, 'AGENT_INSTALL_LOGIN_LIMIT', { max: '32' }), lookup(en))).toBeNull()
    expect(loginNameRefusal(new Error('network'), lookup(en))).toBeNull()
  })
})
